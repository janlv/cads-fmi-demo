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
DATA_PVC_MANIFEST="${DATA_PVC_MANIFEST:-$ROOT_DIR/deploy/storage/data-pvc.yaml}"
DATA_PVC_NAME="${DATA_PVC_NAME:-cads-data-pvc}"
DATA_COLLECTION_PATH="${DATA_COLLECTION_PATH:-$ROOT_DIR/data/run-artifacts}"
DATA_MOUNT_PATH="${DATA_MOUNT_PATH:-/app/data}"
DATA_PVC_HELPER_IMAGE="${DATA_PVC_HELPER_IMAGE:-busybox:1.36}"
ARGO_NAMESPACE="${ARGO_NAMESPACE:-argo}"
DATA_PVC_MANIFEST="${DATA_PVC_MANIFEST:-$ROOT_DIR/deploy/storage/data-pvc.yaml}"

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

ensure_data_pvc() {
    if [[ ! -f "$DATA_PVC_MANIFEST" ]]; then
        return
    fi
    log_step "Ensuring workflow data PVC exists (${DATA_PVC_MANIFEST})"
    kubectl apply -f "$DATA_PVC_MANIFEST"
}

copy_data_from_pvc() {
    if [[ -z "$DATA_PVC_NAME" ]]; then
        return
    fi
    local helper="cads-data-copy-$(date +%s)"
    local manifest
    manifest=$(cat <<YAML
apiVersion: v1
kind: Pod
metadata:
  name: ${helper}
  namespace: ${ARGO_NAMESPACE}
spec:
  restartPolicy: Never
  containers:
    - name: extractor
      image: ${DATA_PVC_HELPER_IMAGE}
      command: ["sleep", "300"]
      volumeMounts:
        - name: workflow-data
          mountPath: /data
  volumes:
    - name: workflow-data
      persistentVolumeClaim:
        claimName: ${DATA_PVC_NAME}
YAML
)
    echo "$manifest" | kubectl apply -f - >/dev/null
    if ! kubectl wait --for=condition=Ready "pod/${helper}" -n "$ARGO_NAMESPACE" --timeout=60s >/dev/null 2>&1; then
        log_warn "Data helper pod ${helper} did not become ready; skipping artifact copy."
        kubectl delete pod "$helper" -n "$ARGO_NAMESPACE" --ignore-not-found >/dev/null 2>&1 || true
        return
    fi
    local workflow_label
    workflow_label="$(basename "$WORKFLOW")"
    workflow_label="${workflow_label%.*}"
    local dest_root="$DATA_COLLECTION_PATH"
    mkdir -p "$dest_root"
    local dest="$dest_root/${workflow_label}-$(date +%Y%m%d%H%M%S)"
    rm -rf "$dest"
    mkdir -p "$dest"
    log_step "Copying workflow data from PVC ${DATA_PVC_NAME} via helper pod ${helper}"
    if kubectl cp -n "$ARGO_NAMESPACE" "${helper}:/data/." "$dest" >/dev/null 2>&1; then
        log_ok "Workflow data stored under ${dest}"
    else
        log_warn "Unable to copy workflow data from PVC ${DATA_PVC_NAME}; data may still reside in the claim."
        rm -rf "$dest"
    fi
    kubectl delete pod "$helper" -n "$ARGO_NAMESPACE" --ignore-not-found >/dev/null 2>&1 || true
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
    ensure_data_pvc
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
        if ./scripts/run_argo_workflow.sh --workflow "$WORKFLOW" --image "$IMAGE"; then
            copy_data_from_pvc
        else
            exit 1
        fi
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
