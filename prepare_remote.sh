#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"
source "$ROOT_DIR/scripts/lib/tooling.sh"

if [[ "$(uname -s)" != "Linux" ]]; then
    log_error "prepare_remote.sh currently supports Linux hosts only."
    exit 1
fi

LOCAL_BASE_DIR="$ROOT_DIR/.local"
LOCAL_BIN_DIR="$LOCAL_BASE_DIR/bin"
LOCAL_GO_DIR="$LOCAL_BASE_DIR/go"
IMAGE=""
KUBECONFIG_PATH=""
ARGO_SERVER="${ARGO_SERVER:-argoworkflows.cads.kzslab.dev}"
ARGO_NAMESPACE="${ARGO_NAMESPACE:-playground}"

mkdir -p "$LOCAL_BIN_DIR"
cads_setup_local_path "$ROOT_DIR"

usage() {
    cat <<'EOF'
Usage: ./prepare_remote.sh --image ghcr.io/org/cads-demo:tag [--kubeconfig path]
                           [--argo-server host] [--namespace name]

Validates remote Argo access and publishes the selected image tag for hosted
playground runs.
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

if [[ -z "$IMAGE" ]]; then
    log_error "--image is required"
    usage
    exit 1
fi

if [[ -z "$ARGO_NAMESPACE" ]]; then
    log_error "--namespace requires a non-empty value"
    exit 1
fi

cads_ensure_go "$LOCAL_BASE_DIR" "$LOCAL_GO_DIR"
cads_ensure_argo_cli "$LOCAL_BIN_DIR"
cads_ensure_kubectl_cli "$LOCAL_BIN_DIR"

KUBECONFIG_PATH="$(cads_resolve_kubeconfig "$KUBECONFIG_PATH" || true)"
TOKEN="$(cads_resolve_argo_token "$KUBECONFIG_PATH" || true)"
if [[ -z "$TOKEN" ]]; then
    log_error "Unable to resolve an Argo token. Set ARGO_TOKEN or pass --kubeconfig."
    exit 1
fi

cads_source_host_ca "$ROOT_DIR"

log_step "Validating Argo access to ${ARGO_SERVER} (${ARGO_NAMESPACE})"
if ! run_with_logged_output argo list \
    -n "$ARGO_NAMESPACE" \
    -s "$ARGO_SERVER" \
    --token "$TOKEN" \
    --argo-http1 >/dev/null; then
    log_error "Unable to authenticate with the remote Argo server."
    exit 1
fi
log_ok "Remote Argo access verified"

bash "$ROOT_DIR/scripts/publish_image.sh" --image "$IMAGE"

cat <<EOF

Remote environment preparation complete. Continue with:
  ./run_remote.sh workflows/python_chain.yaml --image $IMAGE
EOF
