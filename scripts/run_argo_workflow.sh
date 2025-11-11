#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOCAL_BIN_DIR="$ROOT_DIR/.local/bin"
export PATH="$LOCAL_BIN_DIR:$PATH"
IMAGE="cads-fmi-demo:latest"
WORKFLOW=""
NAMESPACE="${ARGO_NAMESPACE:-argo}"

usage() {
    cat <<'USAGE'
Usage: scripts/run_argo_workflow.sh --workflow workflows/example.yaml [--image image:tag] [--namespace name]

Generates the Argo Workflow manifest for the workflow (if needed) and submits it via argo CLI.
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
        --namespace)
            shift
            NAMESPACE="${1:-}"
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

if [[ -z "$NAMESPACE" ]]; then
    echo "[error] --namespace requires a non-empty value" >&2
    exit 1
fi

"$ROOT_DIR/scripts/generate_manifests.sh" --workflow "$WORKFLOW" --image "$IMAGE"
ARGO_NAMESPACE="$NAMESPACE" "$ROOT_DIR/scripts/ensure_argo_workflows.sh"
NAME="$(basename "$WORKFLOW")"
NAME="${NAME%.*}"
ARGO_FILE="$ROOT_DIR/deploy/argo/${NAME}-workflow.yaml"

argo submit --namespace "$NAMESPACE" "$ARGO_FILE"
argo watch --namespace "$NAMESPACE" "cads-${NAME}" || true
