#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARGO_DIR="$ROOT_DIR/deploy/argo"
STORAGE_DIR="$ROOT_DIR/deploy/storage"
IMAGE="cads-fmi-demo:latest"
WORKFLOW=""
WORKFLOW_CONFIGMAP=""
SERVICE_ACCOUNT="argo"
DATA_MOUNT_PATH="/app/data"
DATA_PVC_NAME="cads-data-pvc"
DATA_PVC_SIZE="1Gi"

usage() {
    cat <<'USAGE'
Usage: scripts/generate_manifests.sh --workflow workflows/example.yaml [--image image:tag]

Generates the Argo Workflow and PVC manifests for the given workflow file.
USAGE
}

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --workflow)
            shift
            WORKFLOW="${1:-}"
            ;;
        --image)
            shift
            IMAGE="${1:-}"
            ;;
        --workflow-configmap)
            shift
            WORKFLOW_CONFIGMAP="${1:-}"
            ;;
        *)
            echo "[error] Unknown argument: $1" >&2
            usage
            exit 1
            ;;
    esac
    shift || true
done

if [[ -z "$WORKFLOW" ]]; then
    echo "[error] --workflow is required" >&2
    exit 1
fi

if [[ -z "$WORKFLOW_CONFIGMAP" ]]; then
    echo "[error] --workflow-configmap is required" >&2
    exit 1
fi

if [[ ! -f "$ROOT_DIR/$WORKFLOW" ]]; then
    echo "[error] Workflow file not found: $WORKFLOW" >&2
    exit 1
fi

mkdir -p "$ARGO_DIR" "$STORAGE_DIR"
BASENAME="$(basename "$WORKFLOW")"
NAME="${BASENAME%.*}"
SANITIZED_NAME="$(echo "cads-${NAME}" | tr '[:upper:]' '[:lower:]' | tr -c 'a-z0-9.-' '-')"
RESOURCE_NAME="${SANITIZED_NAME#-}"
RESOURCE_NAME="${RESOURCE_NAME%-}"
if [[ -z "$RESOURCE_NAME" ]]; then
    RESOURCE_NAME="cads-workflow"
fi
CONTAINER_WORKFLOW_DIR="/app/runtime-workflows"
CONTAINER_WORKFLOW_PATH="${CONTAINER_WORKFLOW_DIR}/${BASENAME}"

ARGO_OUT="$ARGO_DIR/${NAME}-workflow.yaml"
PVC_OUT="$STORAGE_DIR/data-pvc.yaml"

cat >"$ARGO_OUT" <<YAML
apiVersion: argoproj.io/v1alpha1
kind: Workflow
metadata:
  name: ${RESOURCE_NAME}
spec:
  serviceAccountName: ${SERVICE_ACCOUNT}
  volumes:
    - name: workflow-data
      persistentVolumeClaim:
        claimName: ${DATA_PVC_NAME}
    - name: workflow-spec
      configMap:
        name: ${WORKFLOW_CONFIGMAP}
        items:
          - key: workflow
            path: ${BASENAME}
  entrypoint: run-workflow
  templates:
    - name: run-workflow
      container:
        image: ${IMAGE}
        imagePullPolicy: IfNotPresent
        command: ["/app/bin/cads-workflow-runner"]
        args: ["--workflow", "${CONTAINER_WORKFLOW_PATH}"]
        volumeMounts:
          - name: workflow-data
            mountPath: ${DATA_MOUNT_PATH}
          - name: workflow-spec
            mountPath: ${CONTAINER_WORKFLOW_DIR}
YAML

cat >"$PVC_OUT" <<YAML
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${DATA_PVC_NAME}
  namespace: argo
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: ${DATA_PVC_SIZE}
YAML

echo "[manifests] Argo:     $ARGO_OUT"
echo "[manifests] Data PVC: $PVC_OUT"
