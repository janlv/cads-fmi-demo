#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"
source "$ROOT_DIR/scripts/lib/tooling.sh"

cads_source_host_ca "$ROOT_DIR" || true

cads_setup_local_path "$ROOT_DIR"
if [[ -z "${CADS_WORKFLOW_IMAGE:-}" && -f "$ROOT_DIR/config/playground.env" ]]; then
    # shellcheck disable=SC1091
    source "$ROOT_DIR/config/playground.env"
fi

default_kubeconfig="$ROOT_DIR/.local/kaizen/kubeconfig"
LOCAL_BASE_DIR="$ROOT_DIR/.local"
LOCAL_BIN_DIR="$LOCAL_BASE_DIR/bin"
LOCAL_GO_DIR="$LOCAL_BASE_DIR/go"
have_explicit_kubeconfig=0
have_explicit_addr=0
prepare_remote_mode="auto"
skip_build=0
connect_existing=0
remote_image="${CADS_WORKFLOW_IMAGE:-}"
resolved_kubeconfig="${KUBECONFIG:-}"
service_args=()
listen_addr=""
default_remote_image="ghcr.io/janlv/cads-fmi-demo:playground"
state_dir="$ROOT_DIR/.local/state"
state_file="$state_dir/dashboard-remote-image.env"
explicit_image=0

dashboard_source_is_clean() {
    if ! command -v git >/dev/null 2>&1; then
        return 1
    fi
    if ! git -C "$ROOT_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        return 1
    fi
    [[ -z "$(git -C "$ROOT_DIR" status --porcelain --untracked-files=all 2>/dev/null)" ]]
}

dashboard_source_signature() {
    if ! command -v git >/dev/null 2>&1; then
        return 1
    fi
    if ! git -C "$ROOT_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
        return 1
    fi
    git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null
}

load_dashboard_state() {
    cached_image=""
    cached_signature=""
    if [[ -f "$state_file" ]]; then
        # shellcheck disable=SC1090
        source "$state_file"
    fi
}

save_dashboard_state() {
    local image="$1"
    local signature="$2"
    mkdir -p "$state_dir"
    cat >"$state_file" <<EOF
cached_image="$image"
cached_signature="$signature"
EOF
}

ensure_dashboard_service_binary() {
    if [[ -x "$ROOT_DIR/bin/cads-workflow-service" ]]; then
        return
    fi

    log_info "Dashboard service binary is missing; building the local dashboard service only."
    log_info "This does not build or publish the workflow container image."
    cads_ensure_go "$LOCAL_BASE_DIR" "$LOCAL_GO_DIR"
    mkdir -p "$ROOT_DIR/bin"
    (
        cd "$ROOT_DIR/orchestrator/service"
        log_stream_cmd "Building local dashboard service" env CGO_ENABLED=0 go build -o "$ROOT_DIR/bin/cads-workflow-service" ./cmd/cads-workflow-service
    )
}

derive_dashboard_image() {
    local source_image="${CADS_WORKFLOW_IMAGE:-$default_remote_image}"
    local repo="${source_image%@*}"
    if [[ "$repo" == *:* && "$repo" == */* ]]; then
        repo="${repo%:*}"
    fi
    if [[ -z "$repo" ]]; then
        repo="${default_remote_image%:*}"
    fi

    local git_sha="nogit"
    if command -v git >/dev/null 2>&1; then
        git_sha="$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || printf 'nogit')"
    fi

    printf '%s:%s\n' "$repo" "dashboard-${git_sha}-$(date -u +%Y%m%d%H%M%S)"
}

dashboard_listen_port() {
    local addr="$1"
    local port="${addr##*:}"
    if [[ -z "$port" || ! "$port" =~ ^[0-9]+$ ]]; then
        return 1
    fi
    printf '%s\n' "$port"
}

dashboard_process_matches() {
    local pid="$1"
    local cmdline=""
    if [[ ! -r "/proc/$pid/cmdline" ]]; then
        return 1
    fi
    cmdline="$(tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null || true)"
    [[ "$cmdline" == *"cads-workflow-service"* ]] &&
        [[ "$cmdline" == *"--serve"* ]] &&
        [[ "$cmdline" == *"--workdir $ROOT_DIR"* ]]
}

stop_existing_dashboard_session() {
    local addr="$1"
    local port=""
    local -a listening_pids=()
    local -a dashboard_pids=()
    local -a foreign_pids=()
    local pid=""

    port="$(dashboard_listen_port "$addr" || true)"
    if [[ -z "$port" ]]; then
        log_warn "Unable to parse a TCP port from '$addr'; skipping automatic dashboard shutdown."
        return 0
    fi

    if command -v lsof >/dev/null 2>&1; then
        while IFS= read -r pid; do
            if [[ -n "$pid" ]]; then
                listening_pids+=("$pid")
            fi
        done < <(lsof -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null | sort -u)
    elif command -v ss >/dev/null 2>&1; then
        while IFS= read -r pid; do
            if [[ -n "$pid" ]]; then
                listening_pids+=("$pid")
            fi
        done < <(ss -ltnp "( sport = :$port )" 2>/dev/null | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' | sort -u)
    else
        log_warn "Neither lsof nor ss is available; skipping automatic dashboard shutdown."
        return 0
    fi

    if ((${#listening_pids[@]} == 0)); then
        return 0
    fi

    for pid in "${listening_pids[@]}"; do
        if dashboard_process_matches "$pid"; then
            dashboard_pids+=("$pid")
        else
            foreign_pids+=("$pid")
        fi
    done

    if ((${#foreign_pids[@]} > 0)); then
        log_error "Address $addr is already in use by a non-dashboard process (pid(s): ${foreign_pids[*]})."
        exit 1
    fi

    if ((${#dashboard_pids[@]} == 0)); then
        return 0
    fi

    log_info "Stopping existing dashboard session(s) on $addr (pid(s): ${dashboard_pids[*]})"
    kill "${dashboard_pids[@]}" 2>/dev/null || true

    local deadline=$((SECONDS + 5))
    local -a remaining=("${dashboard_pids[@]}")
    while ((${#remaining[@]} > 0 && SECONDS < deadline)); do
        sleep 0.2
        local -a next_remaining=()
        for pid in "${remaining[@]}"; do
            if kill -0 "$pid" 2>/dev/null; then
                next_remaining+=("$pid")
            fi
        done
        remaining=("${next_remaining[@]}")
    done

    if ((${#remaining[@]} > 0)); then
        log_warn "Dashboard session(s) did not exit after SIGTERM; forcing shutdown for pid(s): ${remaining[*]}"
        kill -KILL "${remaining[@]}" 2>/dev/null || true
        sleep 0.2
    fi

    local -a stubborn=()
    for pid in "${dashboard_pids[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            stubborn+=("$pid")
        fi
    done
    if ((${#stubborn[@]} > 0)); then
        log_error "Unable to stop previous dashboard session(s): ${stubborn[*]}"
        exit 1
    fi

    log_ok "Stopped existing dashboard session(s) on $addr"
}

dashboard_url_for_addr() {
    local addr="$1"
    local host="localhost"
    local port=""

    if [[ "$addr" == :* ]]; then
        port="${addr#:}"
    elif [[ "$addr" == *:* ]]; then
        host="${addr%:*}"
        port="${addr##*:}"
        if [[ "$host" == "0.0.0.0" || "$host" == "::" || "$host" == "[::]" || -z "$host" ]]; then
            host="localhost"
        fi
        host="${host#[}"
        host="${host%]}"
    else
        port="$addr"
    fi

    if [[ -z "$port" || ! "$port" =~ ^[0-9]+$ ]]; then
        return 1
    fi
    printf 'http://%s:%s/\n' "$host" "$port"
}

print_dashboard_url() {
    local addr="$1"
    local url=""
    url="$(dashboard_url_for_addr "$addr" || true)"
    if [[ -z "$url" ]]; then
        log_warn "Unable to derive dashboard URL from address '$addr'."
        return 0
    fi
    log_info "Open the dashboard in your browser: $url"
}

usage() {
    cat <<'EOF'
Usage: scripts/commands/run_dashboard.sh [--image ghcr.io/org/cads-demo:tag] [--prepare-remote|--no-prepare-remote]
                                        [--connect-existing] [--skip-build] [--kubeconfig path] [--addr :8080]

Starts the local dashboard for the hosted Kaizen playground.

Convenience flags:
  --image IMAGE          Set CADS_WORKFLOW_IMAGE for dashboard-launched runs.
  --connect-existing     Connect to the existing Playground environment only.
                         Does not build, publish, or prepare a remote image.
  --prepare-remote       Force build/publish before launch.
  --no-prepare-remote    Skip automatic remote preparation and start immediately.
  default behavior       Automatically prepare a remote image when needed and
                         reuse the last prepared image when the git tree is clean
                         and unchanged.
  automatic stop         Stops an older dashboard session already listening on
                         the selected port before launching the new one.
  --skip-build           With --prepare-remote, skip scripts/commands/build.sh and
                         only run scripts/commands/prepare_remote.sh.

All other flags are passed through to cads-workflow-service.
EOF
}

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --image)
            shift
            remote_image="${1:-}"
            if [[ -z "$remote_image" ]]; then
                log_error "--image expects a value"
                exit 1
            fi
            explicit_image=1
            ;;
        --image=*)
            remote_image="${1#*=}"
            if [[ -z "$remote_image" ]]; then
                log_error "--image expects a value"
                exit 1
            fi
            explicit_image=1
            ;;
        --prepare-remote|--sync-image)
            prepare_remote_mode="force"
            ;;
        --connect-existing|--existing-playground)
            prepare_remote_mode="off"
            skip_build=1
            connect_existing=1
            ;;
        --no-prepare-remote)
            prepare_remote_mode="off"
            ;;
        --skip-build)
            skip_build=1
            ;;
        --kubeconfig)
            have_explicit_kubeconfig=1
            shift
            resolved_kubeconfig="${1:-}"
            if [[ -z "$resolved_kubeconfig" ]]; then
                log_error "--kubeconfig expects a value"
                exit 1
            fi
            service_args+=(--kubeconfig "$resolved_kubeconfig")
            ;;
        --kubeconfig=*)
            have_explicit_kubeconfig=1
            resolved_kubeconfig="${1#*=}"
            if [[ -z "$resolved_kubeconfig" ]]; then
                log_error "--kubeconfig expects a value"
                exit 1
            fi
            service_args+=("$1")
            ;;
        --addr)
            have_explicit_addr=1
            service_args+=("$1")
            shift
            if [[ -z "${1:-}" ]]; then
                log_error "--addr expects a value"
                exit 1
            fi
            listen_addr="$1"
            service_args+=("$1")
            ;;
        --addr=*)
            have_explicit_addr=1
            listen_addr="${1#*=}"
            if [[ -z "$listen_addr" ]]; then
                log_error "--addr expects a value"
                exit 1
            fi
            service_args+=("$1")
            ;;
        *)
            service_args+=("$1")
            ;;
    esac
    shift || true
done

if [[ -z "${ARGO_TOKEN:-}" && -z "$resolved_kubeconfig" && -f "$default_kubeconfig" ]]; then
    log_info "Using default Kaizen kubeconfig at $default_kubeconfig"
    resolved_kubeconfig="$default_kubeconfig"
fi

if (( connect_existing )) && [[ -z "$remote_image" ]]; then
    load_dashboard_state
    if [[ -n "${cached_image:-}" ]]; then
        remote_image="$cached_image"
        log_info "Using cached Playground workflow image $remote_image"
    else
        log_warn "No workflow image was configured for --connect-existing."
        log_warn "Existing runs can still be inspected, but launching new runs may use the service default image."
        log_warn "For exact parity with another machine, set config/playground.env, pass --image, or set CADS_WORKFLOW_IMAGE to the image tag used there."
    fi
fi

should_prepare_remote=0
current_signature=""
if [[ "$prepare_remote_mode" == "force" ]]; then
    should_prepare_remote=1
elif [[ "$prepare_remote_mode" == "auto" ]]; then
    if (( explicit_image )); then
        should_prepare_remote=1
    elif dashboard_source_is_clean; then
        current_signature="$(dashboard_source_signature || true)"
        load_dashboard_state
        if [[ -n "$current_signature" && -n "${cached_image:-}" && "${cached_signature:-}" == "$current_signature" ]]; then
            remote_image="$cached_image"
            log_info "Reusing previously prepared remote image for unchanged git tree"
        else
            should_prepare_remote=1
        fi
    else
        should_prepare_remote=1
    fi
fi

if (( should_prepare_remote )); then
    if [[ -z "$remote_image" ]]; then
        remote_image="$(derive_dashboard_image)"
        log_info "No image tag provided; generated remote image tag for this dashboard launch"
    fi

    if (( !skip_build )); then
        bash "$ROOT_DIR/scripts/commands/build.sh" --image "$remote_image"
    else
        log_info "Skipping scripts/commands/build.sh before remote preparation"
    fi

    prepare_args=(--image "$remote_image")
    if [[ -n "$resolved_kubeconfig" ]]; then
        prepare_args+=(--kubeconfig "$resolved_kubeconfig")
    fi
    bash "$ROOT_DIR/scripts/commands/prepare_remote.sh" "${prepare_args[@]}"

    if [[ -z "$current_signature" ]] && dashboard_source_is_clean; then
        current_signature="$(dashboard_source_signature || true)"
    fi
    save_dashboard_state "$remote_image" "$current_signature"
fi

ensure_dashboard_service_binary

extra_args=()
if [[ -z "${ARGO_TOKEN:-}" && $have_explicit_kubeconfig -eq 0 && -n "$resolved_kubeconfig" ]]; then
    extra_args+=(--kubeconfig "$resolved_kubeconfig")
fi

if [[ $have_explicit_addr -eq 0 ]]; then
    listen_addr=":8080"
    if ((${#extra_args[@]} > 0)); then
        extra_args=(--addr "$listen_addr" "${extra_args[@]}")
    else
        extra_args=(--addr "$listen_addr")
    fi
fi

if [[ -n "$remote_image" ]]; then
    export CADS_WORKFLOW_IMAGE="$remote_image"
    log_info "Dashboard will launch remote workflows with image $remote_image"
fi

if [[ -z "$listen_addr" ]]; then
    log_error "Unable to determine the dashboard listen address."
    exit 1
fi

stop_existing_dashboard_session "$listen_addr"
print_dashboard_url "$listen_addr"

cmd=("$ROOT_DIR/bin/cads-workflow-service" --serve --workdir "$ROOT_DIR")
if ((${#extra_args[@]} > 0)); then
    cmd+=("${extra_args[@]}")
fi
if ((${#service_args[@]} > 0)); then
    cmd+=("${service_args[@]}")
fi

exec "${cmd[@]}"
