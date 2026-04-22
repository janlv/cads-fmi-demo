#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"

default_kubeconfig="$HOME/Kaizen_CADS/kubeconfig"
default_remote_image="ghcr.io/janlv/cads-fmi-demo:latest"
state_dir="$ROOT_DIR/.local/state"
state_file="$state_dir/dashboard-remote-image.env"
ARGO_SERVER="${ARGO_SERVER:-argoworkflows.cads.kzslab.dev}"
ARGO_NAMESPACE="${ARGO_NAMESPACE:-playground}"
SERVICE_ACCOUNT="${SERVICE_ACCOUNT:-playground-storhy-playground-pg-admin}"
KUBECONFIG_PATH=""
IMAGE="${CADS_WORKFLOW_IMAGE:-$default_remote_image}"
SECRET_NAME="storhy-argo-artifacts-s3-credentials"
SECRET_NAMESPACE="playground"
PREFIX=""
LIMIT="200"
FLAT=0
PATH_STYLE=0
OUTPUT=""
explicit_image=0

cads_setup_local_path "$ROOT_DIR"

usage() {
    cat <<'EOF'
Usage: ./run_list_s3_objects.sh [--image ghcr.io/org/cads-demo:tag]
                                [--kubeconfig path] [--argo-server host]
                                [--namespace name] [--service-account name]
                                [--secret-name name] [--secret-namespace name]
                                [--prefix path/] [--limit N] [--flat]
                                [--path-style] [--output manifest.yaml]

Submits a small Argo workflow to the hosted Kaizen playground that lists the
configured S3 bucket contents from inside the cluster and streams the logs.
EOF
}

load_dashboard_state() {
    cached_image=""
    cached_signature=""
    if [[ -f "$state_file" ]]; then
        # shellcheck disable=SC1090
        source "$state_file"
    fi
}

workflow_phase() {
    local name="$1"
    argo get "$name" \
        -n "$ARGO_NAMESPACE" \
        -s "$ARGO_SERVER" \
        --token "$TOKEN" \
        --argo-http1 \
        -o json | python3 -c 'import json, sys; print((json.load(sys.stdin).get("status") or {}).get("phase", ""))'
}

workflow_has_output() {
    local name="$1"
    local output=""
    output="$(
        argo logs "$name" \
            -n "$ARGO_NAMESPACE" \
            -s "$ARGO_SERVER" \
            --token "$TOKEN" \
            --argo-http1 \
            --tail 1 2>/dev/null | sed '/^[[:space:]]*$/d' | tail -n 1 || true
    )"
    [[ -n "$output" ]]
}

monitor_workflow_startup() {
    local name="$1"
    local poll_seconds=5
    local waited_seconds=0
    local startup_note_shown=0

    while true; do
        local phase=""
        phase="$(workflow_phase "$name" 2>/dev/null || true)"
        case "$phase" in
            Succeeded|Failed|Error)
                return 0
                ;;
        esac

        if workflow_has_output "$name"; then
            if (( startup_note_shown == 1 )); then
                log_subinfo "Container output detected; the S3 listing code is now running."
            fi
            return 0
        fi

        waited_seconds=$((waited_seconds + poll_seconds))
        if (( waited_seconds >= 20 && startup_note_shown == 0 )); then
            log_subinfo "Still waiting for the pod to produce its first log line; this usually means image pull or container startup in the playground."
            startup_note_shown=1
        elif (( startup_note_shown == 1 && waited_seconds % 30 == 0 )); then
            log_subinfo "Still no container output from ${name}; likely still initializing or pulling the image."
        fi

        sleep "$poll_seconds"
    done
}

parse_args() {
    while (($#)); do
        case "$1" in
            -h|--help)
                usage
                exit 0
                ;;
            --image)
                shift
                IMAGE="${1:-}"
                explicit_image=1
                ;;
            --kubeconfig)
                shift
                KUBECONFIG_PATH="${1:-}"
                ;;
            --argo-server)
                shift
                ARGO_SERVER="${1:-}"
                ;;
            --namespace)
                shift
                ARGO_NAMESPACE="${1:-}"
                ;;
            --service-account)
                shift
                SERVICE_ACCOUNT="${1:-}"
                ;;
            --secret-name)
                shift
                SECRET_NAME="${1:-}"
                ;;
            --secret-namespace)
                shift
                SECRET_NAMESPACE="${1:-}"
                ;;
            --prefix)
                shift
                PREFIX="${1:-}"
                ;;
            --limit)
                shift
                LIMIT="${1:-}"
                ;;
            --flat)
                FLAT=1
                ;;
            --path-style)
                PATH_STYLE=1
                ;;
            --output)
                shift
                OUTPUT="${1:-}"
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

parse_args "$@"

if [[ -z "${CADS_WORKFLOW_IMAGE:-}" && $explicit_image -eq 0 ]]; then
    load_dashboard_state
    if [[ -n "${cached_image:-}" ]]; then
        IMAGE="$cached_image"
        log_info "Using previously prepared remote image $IMAGE"
    fi
fi

if [[ -z "$ARGO_NAMESPACE" || -z "$SERVICE_ACCOUNT" || -z "$ARGO_SERVER" ]]; then
    log_error "Argo server, namespace, and service account must be non-empty."
    exit 1
fi

if [[ -z "$SECRET_NAME" || -z "$SECRET_NAMESPACE" ]]; then
    log_error "Secret name and namespace must be non-empty."
    exit 1
fi

if [[ ! "$LIMIT" =~ ^[0-9]+$ ]] || (( LIMIT <= 0 )); then
    log_error "--limit must be a positive integer."
    exit 1
fi

cads_require_cmd argo
cads_require_cmd kubectl
cads_source_host_ca "$ROOT_DIR"

if [[ -z "${ARGO_TOKEN:-}" && -z "$KUBECONFIG_PATH" && -z "${KUBECONFIG:-}" && -f "$default_kubeconfig" ]]; then
    log_info "Using default Kaizen kubeconfig at $default_kubeconfig"
    KUBECONFIG_PATH="$default_kubeconfig"
fi

KUBECONFIG_PATH="$(cads_resolve_kubeconfig "$KUBECONFIG_PATH" || true)"
TOKEN="$(cads_resolve_argo_token "$KUBECONFIG_PATH" || true)"
if [[ -z "$TOKEN" ]]; then
    log_error "Unable to resolve an Argo token. Set ARGO_TOKEN or pass --kubeconfig."
    exit 1
fi

RESOURCE_NAME="cads-list-s3-objects-$(date +%Y%m%d%H%M%S)"
MANIFEST_PATH="$OUTPUT"
if [[ -z "$MANIFEST_PATH" ]]; then
    MANIFEST_PATH="$(mktemp "${TMPDIR:-/tmp}/cads-list-s3.XXXXXX.yaml")"
    trap 'rm -f "$MANIFEST_PATH"' EXIT
fi

log_step "Generating in-cluster S3 listing workflow manifest"
{
    cat <<EOF
apiVersion: argoproj.io/v1alpha1
kind: Workflow
metadata:
  name: ${RESOURCE_NAME}
  namespace: ${ARGO_NAMESPACE}
spec:
  serviceAccountName: ${SERVICE_ACCOUNT}
  entrypoint: list-s3
  templates:
    - name: list-s3
      container:
        image: ${IMAGE}
        imagePullPolicy: IfNotPresent
        command: ["python3", "scripts/list_s3_objects.py"]
        args:
          - "--long"
          - "--no-k8s-secret"
          - "--limit"
          - "${LIMIT}"
EOF
    if [[ -n "$PREFIX" ]]; then
        cat <<EOF
          - "--prefix"
          - "${PREFIX}"
EOF
    fi
    if (( FLAT == 1 )); then
        cat <<'EOF'
          - "--flat"
EOF
    fi
    if (( PATH_STYLE == 1 )); then
        cat <<'EOF'
          - "--path-style"
EOF
    fi
    cat <<EOF
        env:
          - name: AWS_ACCESS_KEY_ID
            valueFrom:
              secretKeyRef:
                name: ${SECRET_NAME}
                key: access_key_id
          - name: AWS_SECRET_ACCESS_KEY
            valueFrom:
              secretKeyRef:
                name: ${SECRET_NAME}
                key: secret_access_key
          - name: AWS_REGION
            valueFrom:
              secretKeyRef:
                name: ${SECRET_NAME}
                key: region
          - name: AWS_DEFAULT_REGION
            valueFrom:
              secretKeyRef:
                name: ${SECRET_NAME}
                key: region
          - name: S3_BUCKET
            valueFrom:
              secretKeyRef:
                name: ${SECRET_NAME}
                key: bucket_name
          - name: S3_ENDPOINT
            valueFrom:
              secretKeyRef:
                name: ${SECRET_NAME}
                key: endpoint
EOF
} >"$MANIFEST_PATH"

log_subok "Wrote manifest to $MANIFEST_PATH"
log_step "Submitting remote workflow '$RESOURCE_NAME' to ${ARGO_SERVER}/${ARGO_NAMESPACE}"
monitor_workflow_startup "$RESOURCE_NAME" &
monitor_pid=$!
set +e
argo submit "$MANIFEST_PATH" \
    -n "$ARGO_NAMESPACE" \
    -s "$ARGO_SERVER" \
    --token "$TOKEN" \
    --argo-http1 \
    --name "$RESOURCE_NAME" \
    --watch
status=$?
set -e
kill "$monitor_pid" 2>/dev/null || true
wait "$monitor_pid" 2>/dev/null || true

if ((status != 0)); then
    log_warn "Remote S3 listing submission failed; fetching status and logs for ${RESOURCE_NAME}"
    argo get "$RESOURCE_NAME" \
        -n "$ARGO_NAMESPACE" \
        -s "$ARGO_SERVER" \
        --token "$TOKEN" \
        --argo-http1 || true
    argo logs "$RESOURCE_NAME" \
        -n "$ARGO_NAMESPACE" \
        -s "$ARGO_SERVER" \
        --token "$TOKEN" \
        --argo-http1 || true
    exit "$status"
fi

phase="$(workflow_phase "$RESOURCE_NAME" || true)"
if [[ "$phase" != "Succeeded" ]]; then
    log_warn "S3 listing workflow finished with phase '${phase:-unknown}'; fetching status and logs for ${RESOURCE_NAME}"
    argo get "$RESOURCE_NAME" \
        -n "$ARGO_NAMESPACE" \
        -s "$ARGO_SERVER" \
        --token "$TOKEN" \
        --argo-http1 || true
    argo logs "$RESOURCE_NAME" \
        -n "$ARGO_NAMESPACE" \
        -s "$ARGO_SERVER" \
        --token "$TOKEN" \
        --argo-http1 || true
    exit 1
fi

log_step "Fetching S3 listing logs for ${RESOURCE_NAME}"
argo logs "$RESOURCE_NAME" \
    -n "$ARGO_NAMESPACE" \
    -s "$ARGO_SERVER" \
    --token "$TOKEN" \
    --argo-http1

log_ok "S3 listing workflow completed."
