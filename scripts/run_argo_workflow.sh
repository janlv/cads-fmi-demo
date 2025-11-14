#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOCAL_BIN_DIR="$ROOT_DIR/.local/bin"
LOCAL_GO_BIN="$ROOT_DIR/.local/go/bin"
export PATH="$LOCAL_GO_BIN:$LOCAL_BIN_DIR:$PATH"
IMAGE="cads-fmi-demo:latest"
WORKFLOW=""
NAMESPACE="argo"

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

bash "$ROOT_DIR/scripts/generate_manifests.sh" --workflow "$WORKFLOW" --image "$IMAGE"
NAME="$(basename "$WORKFLOW")"
NAME="${NAME%.*}"
ARGO_FILE="$ROOT_DIR/deploy/argo/${NAME}-workflow.yaml"
PVC_FILE="$ROOT_DIR/deploy/storage/data-pvc.yaml"

if [[ -f "$PVC_FILE" ]]; then
    kubectl apply -f "$PVC_FILE" >/dev/null
fi

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

RESOURCE_NAME="cads-$(sanitize_resource_name "$NAME")"

if argo get --namespace "$NAMESPACE" "$RESOURCE_NAME" >/dev/null 2>&1; then
    echo "[argo] Workflow ${RESOURCE_NAME} already exists; deleting before re-submit"
    argo delete --namespace "$NAMESPACE" "$RESOURCE_NAME" >/dev/null 2>&1 || true
fi

argo submit --namespace "$NAMESPACE" "$ARGO_FILE"
argo watch --namespace "$NAMESPACE" "$RESOURCE_NAME" || true
