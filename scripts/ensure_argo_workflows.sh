#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="argo"
ARGO_VERSION="v3.5.6"
MANIFEST_URL="https://github.com/argoproj/argo-workflows/releases/download/${ARGO_VERSION}/install.yaml"
ROLL_OUT_TIMEOUT="180s"

usage() {
    cat <<'EOF'
Usage: scripts/ensure_argo_workflows.sh [--namespace name]

Installs (or verifies) the Argo Workflows controller inside the provided namespace.
EOF
}

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --namespace)
            shift
            NAMESPACE="${1:-}"
            if [[ -z "$NAMESPACE" ]]; then
                echo "[error] --namespace expects a value" >&2
                exit 1
            fi
            ;;
        *)
            echo "[error] Unknown argument: $1" >&2
            usage
            exit 1
            ;;
    esac
    shift || true
done

log() {
    printf '[argo] %s\n' "$1"
}

require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        printf '[error] Required command not found: %s\n' "$1" >&2
        exit 1
    fi
}

ensure_namespace() {
    if kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
        return
    fi
    log "Creating namespace '${NAMESPACE}' for Argo Workflows"
    kubectl create namespace "$NAMESPACE" >/dev/null 2>&1 || true
}

install_argo() {
    log "Installing Argo Workflows ${ARGO_VERSION} (namespace='${NAMESPACE}')"
    ensure_namespace
    kubectl apply -n "$NAMESPACE" -f "$MANIFEST_URL"
    log "Waiting for workflow-controller rollout (${ROLL_OUT_TIMEOUT})"
    if ! kubectl rollout status deploy/workflow-controller -n "$NAMESPACE" --timeout="$ROLL_OUT_TIMEOUT"; then
        printf '[warn] workflow-controller did not become ready within %s\n' "$ROLL_OUT_TIMEOUT" >&2
    fi
    if kubectl get deploy/argo-server -n "$NAMESPACE" >/dev/null 2>&1; then
        log "Waiting for argo-server rollout (${ROLL_OUT_TIMEOUT})"
        if ! kubectl rollout status deploy/argo-server -n "$NAMESPACE" --timeout="$ROLL_OUT_TIMEOUT"; then
            printf '[warn] argo-server did not become ready within %s\n' "$ROLL_OUT_TIMEOUT" >&2
        fi
    fi
}

main() {
    require_cmd kubectl
    if kubectl get crd workflows.argoproj.io >/dev/null 2>&1; then
        ensure_namespace
        log "Argo Workflows CRD already installed."
        return
    fi
    install_argo
}

main "$@"
