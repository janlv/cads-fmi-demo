#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"
source "$ROOT_DIR/scripts/lib/tooling.sh"

LOCAL_BASE_DIR="$ROOT_DIR/.local"
LOCAL_BIN_DIR="$LOCAL_BASE_DIR/bin"
LOCAL_GO_DIR="$LOCAL_BASE_DIR/go"
default_kubeconfig="$ROOT_DIR/.local/kaizen/kubeconfig"
default_local_image="cads-fmi-demo:latest"
default_remote_image="ghcr.io/janlv/cads-fmi-demo:playground"
state_dir="$ROOT_DIR/.local/state"
state_file="$state_dir/dashboard-remote-image.env"
build_state_file="$state_dir/build-image.env"
IMAGE="${CADS_WORKFLOW_IMAGE:-}"
KUBECONFIG_PATH=""
ARGO_SERVER="${ARGO_SERVER:-argoworkflows.cads.kzslab.dev}"
ARGO_NAMESPACE="${ARGO_NAMESPACE:-playground}"

mkdir -p "$LOCAL_BIN_DIR"
cads_setup_local_path "$ROOT_DIR"
if [[ -z "${CADS_WORKFLOW_IMAGE:-}" && -f "$ROOT_DIR/config/playground.env" ]]; then
    # shellcheck disable=SC1091
    source "$ROOT_DIR/config/playground.env"
fi
IMAGE="${IMAGE:-${CADS_WORKFLOW_IMAGE:-}}"

usage() {
    cat <<'EOF'
Usage: scripts/commands/prepare_remote.sh [--image ghcr.io/org/cads-demo:tag] [--kubeconfig path]
                                          [--argo-server host] [--namespace name]

Validates remote Argo access and publishes the selected image tag for hosted
playground runs.
EOF
}

load_remote_state() {
    cached_image=""
    cached_signature=""
    if [[ -f "$state_file" ]]; then
        # shellcheck disable=SC1090
        source "$state_file"
    fi
}

load_build_state() {
    last_built_image=""
    if [[ -f "$build_state_file" ]]; then
        # shellcheck disable=SC1090
        source "$build_state_file"
    fi
}

save_remote_state() {
    local image="$1"
    mkdir -p "$state_dir"
    cat >"$state_file" <<EOF
cached_image="$image"
cached_signature=""
EOF
}

derive_remote_image() {
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

    printf '%s:%s\n' "$repo" "remote-${git_sha}-$(date -u +%Y%m%d%H%M%S)"
}

local_image_exists() {
    local image="$1"
    local container_tool="$2"
    if [[ -z "$image" ]]; then
        return 1
    fi
    case "$container_tool" in
        podman)
            podman image exists "$image" >/dev/null 2>&1
            ;;
        docker)
            docker image inspect "$image" >/dev/null 2>&1
            ;;
        *)
            return 1
            ;;
    esac
}

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --image)
            shift
            IMAGE="${1:-}"
            ;;
        --kubeconfig)
            shift
            KUBECONFIG_PATH="${1:-}"
            ;;
        --argo-server)
            shift
            ARGO_SERVER="${1:-}"
            ;;
        --namespace)
            shift
            ARGO_NAMESPACE="${1:-}"
            ;;
        *)
            log_error "Unknown argument: $1"
            usage
            exit 1
            ;;
    esac
    shift || true
done

load_remote_state
load_build_state

if [[ -z "$IMAGE" ]]; then
    IMAGE="$(derive_remote_image)"
    log_info "No image tag provided; generated remote image tag for this remote preparation"
fi

log_info "Remote preparation target image: $IMAGE"

if [[ -z "$ARGO_NAMESPACE" ]]; then
    log_error "--namespace requires a non-empty value"
    exit 1
fi

cads_ensure_go "$LOCAL_BASE_DIR" "$LOCAL_GO_DIR"
cads_ensure_argo_cli "$LOCAL_BIN_DIR"
cads_ensure_kubectl_cli "$LOCAL_BIN_DIR"

if [[ -z "${ARGO_TOKEN:-}" && -z "$KUBECONFIG_PATH" && -z "${KUBECONFIG:-}" && -f "$default_kubeconfig" ]]; then
    log_info "Using default Kaizen kubeconfig at $default_kubeconfig"
    KUBECONFIG_PATH="$default_kubeconfig"
fi

KUBECONFIG_PATH="$(cads_resolve_kubeconfig "$KUBECONFIG_PATH" || true)"
TOKEN="$(cads_resolve_argo_token "$KUBECONFIG_PATH" || true)"
if [[ -z "$TOKEN" ]]; then
    log_error "Unable to resolve an Argo token. Set ARGO_TOKEN or pass --kubeconfig."
    exit 1
fi

cads_source_host_ca "$ROOT_DIR"

log_step "Validating Argo access to ${ARGO_SERVER} (${ARGO_NAMESPACE})"
argo_args=(
    list
    -n "$ARGO_NAMESPACE" \
    -s "$ARGO_SERVER" \
    --token "$TOKEN" \
    --argo-http1
)
if [[ -n "$KUBECONFIG_PATH" ]]; then
    argo_args+=(--kubeconfig "$KUBECONFIG_PATH")
fi

if ! run_with_logged_output argo "${argo_args[@]}" >/dev/null; then
    log_error "Unable to authenticate with the remote Argo server."
    exit 1
fi
log_ok "Remote Argo access verified"

container_tool="$(cads_select_container_tool || true)"
if [[ -z "$container_tool" ]]; then
    log_error "Neither podman nor docker is available to prepare the remote image."
    exit 1
fi

if ! local_image_exists "$IMAGE" "$container_tool"; then
    source_image=""
    for candidate in "${last_built_image:-}" "$default_local_image" "${CADS_WORKFLOW_IMAGE:-}" "${cached_image:-}"; do
        if [[ -n "$candidate" && "$candidate" != "$IMAGE" ]] && local_image_exists "$candidate" "$container_tool"; then
            source_image="$candidate"
            break
        fi
    done

    if [[ -z "$source_image" ]]; then
        log_error "Local image '$IMAGE' was not found, and no fallback source image is available to retag."
        log_error "Run scripts/commands/build.sh first, or pass --image with a tag that already exists locally."
        exit 1
    fi

    log_info "Remote image '$IMAGE' will be created from local image '$source_image'"
    log_step "Tagging local image $source_image as $IMAGE"
    if ! run_with_logged_output "$container_tool" tag "$source_image" "$IMAGE"; then
        log_error "Unable to retag local image '$source_image' as '$IMAGE'."
        exit 1
    fi
    log_ok "Tagged local image as $IMAGE"
else
    log_info "Using existing local image tag $IMAGE for remote publish"
fi

bash "$ROOT_DIR/scripts/publish_image.sh" --image "$IMAGE"
save_remote_state "$IMAGE"

cat <<EOF

Remote environment preparation complete. Published image:
  $IMAGE

Continue with:
  scripts/commands/run_remote.sh workflows/tests/python_chain.yaml
  scripts/commands/run_inspect_s3_object.sh artifacts/my-file

To override this prepared image explicitly:
  CADS_WORKFLOW_IMAGE=$IMAGE scripts/commands/run_remote.sh workflows/tests/python_chain.yaml
EOF
