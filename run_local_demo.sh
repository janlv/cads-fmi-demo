#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"
source "$ROOT_DIR/scripts/lib/tooling.sh"

IMAGE="${CADS_LOCAL_RUNNER_IMAGE:-cads-fmi-demo:dev}"
DB_PORT="${CADS_DB_PORT:-5432}"
DB_URL="postgres://cads:cads@127.0.0.1:${DB_PORT}/storhy"
STATE_FILE="$ROOT_DIR/.local/state/local-demo.env"
SERVICE_BIN="$ROOT_DIR/bin/cads-workflow-service"
mode="start"
rebuild=0
service_args=()

usage() {
    cat <<'EOF'
Usage: ./run_local_demo.sh [--rebuild] [service args...]
       ./run_local_demo.sh --next
       ./run_local_demo.sh --reset

User path: Local demo
Runs the dashboard and the workflows on this machine against a local
TimescaleDB with synthetic data. No Kaizen access needed; needs Podman (or
Docker on macOS) running.

Without options it prepares the repo-local tooling, starts the database with
the first measurement day, builds the runner image (first time and after a new
commit), and starts the dashboard at http://127.0.0.1:8080. Stop it with
Ctrl+C; rerunning reuses the database and the image.

  --rebuild   Rebuild the runner image and service even if they look current
              (use after editing workflows, FMUs or Go code without committing).
  --next      In a second terminal: load the next synthetic day and let the
              running dashboard assess it.
  --reset     Delete the database and start the assessment chain over.

Other arguments go to bin/cads-workflow-service (for example --addr :8081).

Environment:
  CADS_DB_PORT             Database port (default 5432)
  CADS_LOCAL_RUNNER_IMAGE  Runner image tag (default cads-fmi-demo:dev)
EOF
}

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --next)
            mode="next"
            ;;
        --reset)
            mode="reset"
            ;;
        --rebuild)
            rebuild=1
            ;;
        *)
            service_args+=("$1")
            ;;
    esac
    shift
done

# Set before run_db.sh so it does not ask the user to export it.
export CADS_DB_URL="$DB_URL"

case "$mode" in
    next)
        exec "$ROOT_DIR/scripts/commands/run_db.sh" --next --notify
        ;;
    reset)
        exec "$ROOT_DIR/scripts/commands/run_db.sh" --reset
        ;;
esac

# The Podman VM clock on macOS lags after the laptop sleeps; apt in the image
# build then rejects Debian's package lists and run stamps shift.
sync_podman_clock() {
    [[ "$(cads_detect_os)" == "darwin" ]] || return 0
    local vm_now host_now drift
    vm_now="$(podman machine ssh date +%s 2>/dev/null | tr -d '\r' || true)"
    [[ "$vm_now" =~ ^[0-9]+$ ]] || return 0
    host_now="$(date +%s)"
    drift=$((host_now - vm_now))
    if ((drift > 60 || drift < -60)); then
        log_info "Podman VM clock is off by ${drift}s; setting it to the host time"
        podman machine ssh "sudo date -u -s @$(date +%s)" >/dev/null 2>&1 \
            || log_warn "Could not set the Podman VM clock; see docs/troubleshooting.md"
    fi
}

image_exists() {
    "$container_tool" image inspect "$IMAGE" >/dev/null 2>&1
}

current_commit() {
    git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || echo unknown
}

built_commit() {
    [[ -f "$STATE_FILE" ]] || return 0
    sed -n "s/^built_commit=//p" "$STATE_FILE"
}

needs_build() {
    ((rebuild == 1)) && return 0
    [[ -x "$SERVICE_BIN" ]] || return 0
    image_exists || return 0
    [[ "$(built_commit)" == "$(current_commit)" ]] || return 0
    return 1
}

log_step "Preparing the local demo"
bash "$ROOT_DIR/prepare.sh" --quiet --require-container-runtime --skip-age
container_tool="$(cads_select_container_tool)"
if [[ "$container_tool" == "docker" && "$(cads_detect_os)" == "linux" ]]; then
    log_warn "Docker on Linux cannot reach the local database from the runner container; use Podman."
fi
if [[ "$container_tool" == "podman" ]]; then
    sync_podman_clock
fi

"$ROOT_DIR/scripts/commands/run_db.sh"

if needs_build; then
    log_step "Building the runner image $IMAGE and the dashboard service (first build takes several minutes)"
    "$ROOT_DIR/scripts/commands/build.sh" --image "$IMAGE"
    mkdir -p "$(dirname "$STATE_FILE")"
    printf 'built_commit=%s\nbuilt_image=%s\n' "$(current_commit)" "$IMAGE" >"$STATE_FILE"
else
    log_info "Reusing image $IMAGE and bin/cads-workflow-service (pass --rebuild after local edits)"
fi

port=8080
prev=""
if ((${#service_args[@]} > 0)); then
    for arg in "${service_args[@]}"; do
        case "$arg" in
            --addr=*) port="${arg##*:}" ;;
            *) [[ "$prev" == "--addr" ]] && port="${arg##*:}" ;;
        esac
        prev="$arg"
    done
fi

log_ok "Open http://127.0.0.1:${port} and pick Le Cheylas, then press Run assessment."
if [[ "$port" == "8080" ]]; then
    log_info "Load the next day from another terminal with: ./run_local_demo.sh --next"
else
    log_info "Load the next day from another terminal with: CADS_DASHBOARD_URL=http://127.0.0.1:${port} ./run_local_demo.sh --next"
fi
cmd=("$SERVICE_BIN" --serve --execution local --local-runner-image "$IMAGE")
if ((${#service_args[@]} > 0)); then
    cmd+=("${service_args[@]}")
fi
exec "${cmd[@]}"
