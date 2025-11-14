#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
K8S_DIR="$ROOT_DIR/deploy/k8s"
ARGO_DIR="$ROOT_DIR/deploy/argo"
IMAGE="cads-fmi-demo:latest"
WORKFLOW=""
SERVICE_ACCOUNT="${ARGO_SERVICE_ACCOUNT:-argo}"

usage() {
    cat <<'USAGE'
Usage: scripts/generate_manifests.sh --workflow workflows/example.yaml [--image image:tag]

Generates Kubernetes Job and Argo Workflow manifests that run the specified workflow
using the container built for this repo.
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

if [[ ! -f "$ROOT_DIR/$WORKFLOW" ]]; then
    echo "[error] Workflow file not found: $WORKFLOW" >&2
    exit 1
fi

mkdir -p "$K8S_DIR" "$ARGO_DIR"
BASENAME=$(basename "$WORKFLOW")
NAME="${BASENAME%.*}"

sanitize_resource_name() {
    local value="$1"
    value="${value,,}"
    value="$(echo "$value" | tr -c 'a-z0-9.-' '-')"
    while [[ "$value" =~ ^[^a-z0-9]+ ]]; do
        value="${value#?}"
    done
    while [[ "$value" =~ [^a-z0-9]+$ ]]; do
        value="${value%?}"
    done
    if [[ -z "$value" ]]; then
        value="workflow"
    fi
    printf '%s\n' "$value"
}

SANITIZED_NAME="$(sanitize_resource_name "$NAME")"
RESOURCE_NAME="cads-${SANITIZED_NAME}"
K8S_OUT="$K8S_DIR/${NAME}-job.yaml"
ARGO_OUT="$ARGO_DIR/${NAME}-workflow.yaml"

cat >"$K8S_OUT" <<YAML
apiVersion: batch/v1
kind: Job
metadata:
  name: ${RESOURCE_NAME}
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: cads-workflow
          image: ${IMAGE}
          imagePullPolicy: IfNotPresent
          command: ["/app/bin/cads-workflow-runner"]
          args: ["--workflow", "${WORKFLOW}"]
YAML

cat >"$ARGO_OUT" <<YAML
apiVersion: argoproj.io/v1alpha1
kind: Workflow
metadata:
  name: ${RESOURCE_NAME}
spec:
  serviceAccountName: ${SERVICE_ACCOUNT}
  entrypoint: run-workflow
  templates:
    - name: run-workflow
      container:
        image: ${IMAGE}
        imagePullPolicy: IfNotPresent
        command: ["/app/bin/cads-workflow-runner"]
        args: ["--workflow", "${WORKFLOW}"]
YAML

echo "[manifests] Kubernetes: $K8S_OUT"
echo "[manifests] Argo:        $ARGO_OUT"
