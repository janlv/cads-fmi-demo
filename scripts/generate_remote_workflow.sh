#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"

ARGO_DIR="$ROOT_DIR/deploy/argo"

IMAGE="ghcr.io/janlv/cads-fmi-demo:latest"
WORKFLOW=""
SERVICE_ACCOUNT="playground-storhy-playground-pg-admin"
NAMESPACE="playground"
OUTPUT=""

usage() {
    cat <<'USAGE'
Usage: scripts/generate_remote_workflow.sh workflows/foo.yaml [--image ghcr.io/...]
                                                             [--service-account name]
                                                             [--namespace name]
                                                             [--output deploy/argo/foo-remote-workflow.yaml]

Generates a PVC/configmap-free Argo Workflow manifest for hosted Argo instances
which only expose the demo container filesystem.

The script intentionally omits any persistent volume mounts and assumes the
referenced workflow file already exists inside the container image at the same
relative path.
USAGE
}

if (($# == 0)); then
    usage
    exit 1
fi

if [[ "$1" == "-h" || "$1" == "--help" ]]; then
    usage
    exit 0
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
        --service-account)
            shift
            SERVICE_ACCOUNT="${1:-}"
            ;;
        --namespace)
            shift
            NAMESPACE="${1:-}"
            ;;
        --output)
            shift
            OUTPUT="${1:-}"
            ;;
        *)
            echo "[error] Unknown argument: $1" >&2
            usage
            exit 1
            ;;
    esac
    shift || true
done

if [[ ! -f "$ROOT_DIR/$WORKFLOW" ]]; then
    echo "[error] Workflow file not found: $WORKFLOW" >&2
    exit 1
fi

BASENAME="$(basename "$WORKFLOW")"
NAME="${BASENAME%.*}"
RESOURCE_NAME="cads-$(cads_sanitize_resource_name "$NAME")"

if [[ -z "$OUTPUT" ]]; then
    OUTPUT="$ARGO_DIR/${NAME}-remote-workflow.yaml"
fi

mkdir -p "$(dirname "$OUTPUT")"

cat >"$OUTPUT" <<YAML
apiVersion: argoproj.io/v1alpha1
kind: Workflow
metadata:
  name: ${RESOURCE_NAME}
  namespace: ${NAMESPACE}
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

echo "[remote-workflow] Generated $OUTPUT"
