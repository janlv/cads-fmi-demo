#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"
source "$ROOT_DIR/scripts/lib/tooling.sh"

WORKFLOW=""
IMAGE="cads-fmi-demo:latest"
ARGO_NAMESPACE="argo"
DATA_PVC_NAME="${DATA_PVC_NAME:-cads-data-pvc}"
DATA_COLLECTION_PATH="${DATA_COLLECTION_PATH:-$ROOT_DIR/data/run-artifacts}"
DATA_PVC_HELPER_IMAGE="${DATA_PVC_HELPER_IMAGE:-busybox:1.36}"
MINIKUBE_PROFILE="minikube"

cads_setup_local_path "$ROOT_DIR"

usage() {
    cat <<'EOF'
Usage: ./run_local.sh <workflow.yaml> [--image image:tag]

Submits the workflow to the Argo controller running inside the local Minikube
cluster.
EOF
}

parse_args() {
    if (($# == 0)); then
        usage
        exit 1
    fi
    if [[ "$1" == "-h" || "$1" == "--help" ]]; then
        usage
        exit 0
    fi
    WORKFLOW="$1"
    shift || true
    while (($#)); do
        case "$1" in
            -h|--help)
                usage
                exit 0
                ;;
            --image)
                shift
                IMAGE="${1:-}"
                if [[ -z "$IMAGE" ]]; then
                    log_error "--image expects a value"
                    exit 1
                fi
                shift || true
                continue
                ;;
            *)
                log_error "Unknown argument: $1"
                usage
                exit 1
                ;;
        esac
        shift || true
    done
}

ensure_kube_context() {
    cads_require_cmd kubectl
    local current_context
    current_context="$(kubectl config current-context 2>/dev/null || true)"
    if [[ -z "$current_context" ]]; then
        log_error "kubectl cannot determine the current context. Ensure Minikube is running and kubectl is configured."
        exit 1
    fi
    if [[ "$current_context" != "$MINIKUBE_PROFILE" ]]; then
        log_error "Current kubectl context is '$current_context'. Switch to '$MINIKUBE_PROFILE' before using run_local.sh."
        exit 1
    fi
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
      command: ["sleep", "600"]
      volumeMounts:
        - name: workflow-data
          mountPath: /data
  volumes:
    - name: workflow-data
      persistentVolumeClaim:
        claimName: ${DATA_PVC_NAME}
YAML
)
    log_step "Starting helper pod to copy data from PVC ${DATA_PVC_NAME}"
    printf '%s\n' "$manifest" | kubectl apply -f - >/dev/null
    if ! kubectl wait --for=condition=Ready -n "$ARGO_NAMESPACE" "pod/${helper}" --timeout=60s >/dev/null 2>&1; then
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
    mkdir -p "$dest"
    log_step "Copying workflow data from PVC ${DATA_PVC_NAME} into ${dest}"
    if ! kubectl cp -n "$ARGO_NAMESPACE" "${helper}:/data/." "$dest" >/dev/null 2>&1; then
        log_warn "Failed to copy data from PVC ${DATA_PVC_NAME}"
        rm -rf "$dest"
    else
        log_ok "Artifacts available under ${dest}"
    fi
    kubectl delete pod "$helper" -n "$ARGO_NAMESPACE" --ignore-not-found >/dev/null 2>&1 || true
}

parse_args "$@"

if [[ ! -f "$ROOT_DIR/$WORKFLOW" ]]; then
    log_error "Workflow file not found: $WORKFLOW"
    exit 1
fi

cads_require_cmd argo
cads_ensure_minikube_cluster "$MINIKUBE_PROFILE"
ensure_kube_context
cads_sync_minikube_ca "$ROOT_DIR" "$MINIKUBE_PROFILE"
cads_ensure_argo_controller "$ROOT_DIR" "$ARGO_NAMESPACE"
cads_load_image_into_minikube "$IMAGE" "$MINIKUBE_PROFILE"

log_step "Submitting workflow '$WORKFLOW' to namespace '${ARGO_NAMESPACE}' (image: $IMAGE)"
"$ROOT_DIR/scripts/run_argo_workflow.sh" \
    --workflow "$WORKFLOW" \
    --image "$IMAGE" \
    --namespace "$ARGO_NAMESPACE"

copy_data_from_pvc
