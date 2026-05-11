#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"

IMAGE=""

cads_setup_local_path "$ROOT_DIR"

usage() {
    cat <<'EOF'
Usage: scripts/publish_image.sh --image ghcr.io/org/cads-demo:tag

Pushes the selected image tag using the locally available container engine.
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
            IMAGE="${1:-}"
            ;;
        *)
            log_error "Unknown argument: $1"
            usage
            exit 1
            ;;
    esac
    shift || true
done

if [[ -z "$IMAGE" ]]; then
    log_error "--image is required"
    usage
    exit 1
fi

container_tool="$(cads_select_container_tool || true)"
if [[ -z "$container_tool" ]]; then
    log_error "Neither podman nor docker is available to publish the container image."
    exit 1
fi

cads_ensure_ghcr_login "$IMAGE" "$container_tool"

if ! log_stream_cmd "Publishing container image $IMAGE (${container_tool})" "$container_tool" push "$IMAGE"; then
    if cads_is_ghcr_image "$IMAGE"; then
        log_error "GHCR push failed. Run ./prepare_ghcr.sh, or ensure the selected token can write packages."
    fi
    exit 1
fi
log_ok "Published $IMAGE"
