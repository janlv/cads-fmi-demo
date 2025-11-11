#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${ARGO_NAMESPACE:-argo}"
AUTO_INSTALL="${ARGO_AUTO_INSTALL:-true}"
ARGO_VERSION_DEFAULT="${ARGO_VERSION_REQUIRED:-v3.5.6}"
ARGO_VERSION="${ARGO_VERSION:-$ARGO_VERSION_DEFAULT}"
MANIFEST_URL_DEFAULT="https://github.com/argoproj/argo-workflows/releases/download/${ARGO_VERSION}/install.yaml"
MANIFEST_URL="${ARGO_MANIFEST_URL:-$MANIFEST_URL_DEFAULT}"
ROLL_OUT_TIMEOUT="${ARGO_ROLLOUT_TIMEOUT:-180s}"

log() {
    printf '[argo] %s\n' "$1"
}

require_cmd() {
    local cmd="$1"
    local msg="$2"
    if ! command -v "$cmd" >/dev/null 2>&1; then
        printf '[error] %s\n' "$msg" >&2
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
    if [[ "$AUTO_INSTALL" != "true" ]]; then
        cat <<EOF >&2
[error] Argo Workflows CRD (workflows.argoproj.io) not found in the cluster.
Set ARGO_AUTO_INSTALL=true to allow automatic installation or install it manually:
  kubectl create namespace ${NAMESPACE}
  kubectl apply -n ${NAMESPACE} -f ${MANIFEST_URL}
EOF
        exit 1
    fi

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
    require_cmd kubectl "kubectl is required to verify/install Argo Workflows."

    if kubectl get crd workflows.argoproj.io >/dev/null 2>&1; then
        ensure_namespace
        log "Argo Workflows CRD already present."
        return
    fi

    install_argo
}

main "$@"
