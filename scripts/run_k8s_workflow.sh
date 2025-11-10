#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="cads-fmi-demo:latest"
WORKFLOW=""

usage() {
    cat <<'USAGE'
Usage: scripts/run_k8s_workflow.sh --workflow workflows/example.yaml [--image image:tag]

Generates the Kubernetes manifest for the workflow (if needed) and applies it via kubectl.
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

"$ROOT_DIR/scripts/generate_manifests.sh" --workflow "$WORKFLOW" --image "$IMAGE"
NAME="$(basename "$WORKFLOW")"
NAME="${NAME%.*}"
K8S_FILE="$ROOT_DIR/deploy/k8s/${NAME}-job.yaml"

kubectl apply -f "$K8S_FILE"
kubectl logs -f job/cads-${NAME} --since=10s || true
