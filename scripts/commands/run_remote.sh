#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"

WORKFLOW=""
default_kubeconfig="$ROOT_DIR/.local/kaizen/kubeconfig"
default_remote_image="ghcr.io/janlv/cads-fmi-demo:playground"
if [[ -z "${CADS_WORKFLOW_IMAGE:-}" && -f "$ROOT_DIR/config/playground.env" ]]; then
    # shellcheck disable=SC1091
    source "$ROOT_DIR/config/playground.env"
fi
state_dir="$ROOT_DIR/.local/state"
state_file="$state_dir/dashboard-remote-image.env"
IMAGE="${CADS_WORKFLOW_IMAGE:-$default_remote_image}"
KUBECONFIG_PATH=""
ARGO_SERVER="${ARGO_SERVER:-argoworkflows.cads.kzslab.dev}"
ARGO_NAMESPACE="${ARGO_NAMESPACE:-playground}"
SERVICE_ACCOUNT="${SERVICE_ACCOUNT:-playground-storhy-playground-pg-admin}"
S3_CREDENTIALS_SECRET="${S3_CREDENTIALS_SECRET:-storhy-argo-artifacts-s3-credentials}"
OUTPUT=""
explicit_image=0

cads_setup_local_path "$ROOT_DIR"

usage() {
    cat <<'EOF'
Usage: scripts/commands/run_remote.sh <workflow.yaml> [--image ghcr.io/org/cads-demo:tag]
                      [--kubeconfig path] [--argo-server host]
                      [--namespace name] [--service-account name]
                      [--s3-credentials-secret name]
                      [--output path]

Generates and submits a hosted-Argo workflow manifest to the remote playground.
If --image is omitted, the script reuses CADS_WORKFLOW_IMAGE or the last image
prepared by scripts/commands/prepare_remote.sh when available.
EOF
}

load_remote_state() {
    cached_image=""
    cached_signature=""
    if [[ -f "$state_file" ]]; then
        # shellcheck disable=SC1090
        source "$state_file"
    fi
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
    WORKFLOW="$1"
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
            --s3-credentials-secret)
                shift
                S3_CREDENTIALS_SECRET="${1:-}"
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
    load_remote_state
    if [[ -n "${cached_image:-}" ]]; then
        IMAGE="$cached_image"
        log_info "Using previously prepared remote image $IMAGE"
    fi
fi

if [[ ! -f "$ROOT_DIR/$WORKFLOW" ]]; then
    log_error "Workflow file not found: $WORKFLOW"
    exit 1
fi

if [[ -z "$ARGO_NAMESPACE" || -z "$SERVICE_ACCOUNT" || -z "$ARGO_SERVER" || -z "$S3_CREDENTIALS_SECRET" ]]; then
    log_error "Argo server, namespace, service account, and S3 credentials secret must be non-empty."
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
argo_connection_args=(
    -n "$ARGO_NAMESPACE"
    -s "$ARGO_SERVER"
    --token "$TOKEN"
    --argo-http1
)
if [[ -n "$KUBECONFIG_PATH" ]]; then
    argo_connection_args+=(--kubeconfig "$KUBECONFIG_PATH")
fi

local_name="$(basename "$WORKFLOW")"
local_name="${local_name%.*}"
resource_name="cads-$(cads_sanitize_resource_name "$local_name")-$(date +%Y%m%d%H%M%S)"

remote_generator_args=(
    "$WORKFLOW"
    --image "$IMAGE"
    --service-account "$SERVICE_ACCOUNT"
    --namespace "$ARGO_NAMESPACE"
    --s3-credentials-secret "$S3_CREDENTIALS_SECRET"
)
if [[ -n "$OUTPUT" ]]; then
    remote_generator_args+=(--output "$OUTPUT")
fi

bash "$ROOT_DIR/scripts/generate_remote_workflow.sh" "${remote_generator_args[@]}"

manifest_path="$OUTPUT"
if [[ -z "$manifest_path" ]]; then
    manifest_path="$ROOT_DIR/deploy/argo/${local_name}-remote-workflow.yaml"
fi

log_step "Submitting remote workflow '$resource_name' to ${ARGO_SERVER}/${ARGO_NAMESPACE}"
set +e
argo submit "$manifest_path" \
    "${argo_connection_args[@]}" \
    --name "$resource_name" \
    --watch
status=$?
set -e

if ((status != 0)); then
    log_warn "Remote workflow submission failed; fetching status and logs for ${resource_name}"
    argo get "$resource_name" \
        "${argo_connection_args[@]}" || true
    argo logs "$resource_name" \
        "${argo_connection_args[@]}" || true
    exit "$status"
fi
