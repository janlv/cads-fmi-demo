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
OBJECT_KEY=""
BUCKET_OVERRIDE=""
PREVIEW_BYTES="4096"
PATH_STYLE=0
OUTPUT=""
explicit_image=0

cads_setup_local_path "$ROOT_DIR"

usage() {
    cat <<'EOF'
Usage: ./run_inspect_s3_object.sh <key> [--image ghcr.io/org/cads-demo:tag]
                                   [--bucket name] [--bytes N] [--path-style]
                                   [--kubeconfig path] [--argo-server host]
                                   [--namespace name] [--service-account name]
                                   [--secret-name name] [--secret-namespace name]
                                   [--output manifest.yaml]

Runs a small Argo workflow in the Kaizen playground that fetches one S3 object,
prints its metadata, and shows a small content preview in the workflow logs.
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
                log_subinfo "Container output detected; the S3 inspection code is now running."
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
    if (($# == 0)); then
        usage
        exit 1
    fi
    if [[ "$1" == "-h" || "$1" == "--help" ]]; then
        usage
        exit 0
    fi

    OBJECT_KEY="$1"
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
                explicit_image=1
                ;;
            --bucket)
                shift
                BUCKET_OVERRIDE="${1:-}"
                ;;
            --bytes)
                shift
                PREVIEW_BYTES="${1:-}"
                ;;
            --path-style)
                PATH_STYLE=1
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

if [[ -z "$OBJECT_KEY" || -z "$ARGO_NAMESPACE" || -z "$SERVICE_ACCOUNT" || -z "$ARGO_SERVER" ]]; then
    log_error "Object key, Argo server, namespace, and service account must be non-empty."
    exit 1
fi

if [[ -z "$SECRET_NAME" || -z "$SECRET_NAMESPACE" ]]; then
    log_error "Secret name and namespace must be non-empty."
    exit 1
fi

if [[ ! "$PREVIEW_BYTES" =~ ^[0-9]+$ ]] || (( PREVIEW_BYTES <= 0 )); then
    log_error "--bytes must be a positive integer."
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

RESOURCE_NAME="cads-inspect-s3-object-$(date +%Y%m%d%H%M%S)"
MANIFEST_PATH="$OUTPUT"
if [[ -z "$MANIFEST_PATH" ]]; then
    MANIFEST_PATH="$(mktemp "${TMPDIR:-/tmp}/cads-inspect-s3.XXXXXX.yaml")"
    trap 'rm -f "$MANIFEST_PATH"' EXIT
fi

log_step "Generating in-cluster S3 inspection workflow manifest"
{
    cat <<EOF
apiVersion: argoproj.io/v1alpha1
kind: Workflow
metadata:
  name: ${RESOURCE_NAME}
  namespace: ${ARGO_NAMESPACE}
spec:
  serviceAccountName: ${SERVICE_ACCOUNT}
  entrypoint: inspect-s3
  templates:
    - name: inspect-s3
      container:
        image: ${IMAGE}
        imagePullPolicy: IfNotPresent
        command: ["python3", "scripts/inspect_s3_object.py"]
        args:
          - "${OBJECT_KEY}"
          - "--no-k8s-secret"
          - "--bytes"
          - "${PREVIEW_BYTES}"
EOF
    if [[ -n "$BUCKET_OVERRIDE" ]]; then
        cat <<EOF
          - "--bucket"
          - "${BUCKET_OVERRIDE}"
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
    log_warn "Remote S3 inspection submission failed; fetching status and logs for ${RESOURCE_NAME}"
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
    log_warn "S3 inspection workflow finished with phase '${phase:-unknown}'; fetching status and logs for ${RESOURCE_NAME}"
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

log_step "Fetching S3 inspection logs for ${RESOURCE_NAME}"
argo logs "$RESOURCE_NAME" \
    -n "$ARGO_NAMESPACE" \
    -s "$ARGO_SERVER" \
    --token "$TOKEN" \
    --argo-http1

log_ok "S3 inspection workflow completed."
