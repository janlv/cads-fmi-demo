#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
LOCAL_BIN_DIR="$ROOT_DIR/.local/bin"
export PATH="$LOCAL_BIN_DIR:$PATH"
if [[ -z "${PODMAN_TMPDIR:-}" ]]; then
    PODMAN_TMPDIR="$ROOT_DIR/.podman-tmp"
    export PODMAN_TMPDIR
fi
mkdir -p "$PODMAN_TMPDIR"
chmod 700 "$PODMAN_TMPDIR" >/dev/null 2>&1 || true
WORKFLOW=""
IMAGE="cads-fmi-demo:latest"
MODE=""
LOG_MAX_LINES=""

require_cmd() {
    local name="$1"
    if ! command -v "$name" >/dev/null 2>&1; then
        log_error "Required command '$name' not found in PATH."
        exit 1
    fi
}

ensure_kube_context() {
    require_cmd kubectl
    if ! kubectl config current-context >/dev/null 2>&1; then
        local cfg_hint="${KUBECONFIG:-$HOME/.kube/config}"
        log_error "kubectl cannot determine the current context."
        log_info "Ensure your kubeconfig exists and set KUBECONFIG if needed (current hint: $cfg_hint)."
        exit 1
    fi
}

usage() {
    cat <<'USAGE'
Usage: ./run.sh <workflow.yaml> [--image image:tag] [--mode k8s|argo|local] [--max-lines N]

Runs the full CADS demo after prepare.sh/build.sh:
  - Builds/updates the container image (via build.sh)
  - Generates manifests
  - Executes the selected mode (Kubernetes Job, Argo Workflow, or local smoke test)
USAGE
}

if (($# == 0)); then
    log_error "Workflow file is required as the first argument."
    usage
    exit 1
fi

WORKFLOW="$1"
shift

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
        --max-lines)
            shift
            LOG_MAX_LINES="${1:-}"
            if [[ -z "$LOG_MAX_LINES" ]]; then
                log_error "--max-lines expects a value"
                usage
                exit 1
            fi
            ;;
        --mode)
            shift
            MODE="${1:-}"
            ;;
        *)
            log_error "Unknown argument: $1"
            usage
            exit 1
            ;;
    esac
    shift || true
done

if [[ -n "$LOG_MAX_LINES" ]]; then
    if ! cads_set_log_tail_lines "$LOG_MAX_LINES"; then
        exit 1
    fi
fi

if [[ -z "$WORKFLOW" ]]; then
    log_error "Workflow file is required."
    exit 1
fi

if [[ ! -f "$ROOT_DIR/$WORKFLOW" ]]; then
    log_error "Workflow file not found: $WORKFLOW"
    exit 1
fi

if [[ -z "$MODE" ]]; then
    MODE="argo"
fi

if [[ "$MODE" == "k8s" || "$MODE" == "argo" ]]; then
    log_step "Ensuring Kubernetes context is active"
    ensure_kube_context
fi

if [[ "$MODE" == "argo" ]]; then
    require_cmd argo
fi

pushd "$ROOT_DIR" >/dev/null
mkdir -p "$ROOT_DIR/deploy/k8s" "$ROOT_DIR/deploy/argo"

log_step "Executing workflow '$WORKFLOW' via mode '$MODE' (image: $IMAGE)"
case "$MODE" in
    k8s)
        ./scripts/run_k8s_workflow.sh --workflow "$WORKFLOW" --image "$IMAGE"
        ;;
    argo)
        ./scripts/run_argo_workflow.sh --workflow "$WORKFLOW" --image "$IMAGE"
        ;;
    local)
        require_cmd podman
        if ! podman image inspect "$IMAGE" >/dev/null 2>&1; then
            log_error "Container image '$IMAGE' not found."
            log_info "Run './build.sh --mode local --image $IMAGE' to build it."
            exit 1
        fi
        log_step "Using existing local container image $IMAGE"
        log_stream_cmd "Starting local container for workflow execution" \
            podman run --rm -v "$(pwd)/data:/app/data" "$IMAGE" \
            /app/bin/cads-workflow-runner --workflow "$WORKFLOW"
        ;;
    *)
        log_error "Unknown mode: $MODE"
        usage
        exit 1
        ;;
 esac
popd >/dev/null
