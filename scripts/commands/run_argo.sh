#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"

default_kubeconfig="$HOME/Kaizen_CADS/kubeconfig"
ARGO_SERVER="${ARGO_SERVER:-argoworkflows.cads.kzslab.dev}"
ARGO_NAMESPACE="${ARGO_NAMESPACE:-playground}"
KUBECONFIG_PATH=""
TOKEN_OVERRIDE=""
FORCE_HTTP1=1
ARGO_ARGS=()

cads_setup_local_path "$ROOT_DIR"

usage() {
    cat <<'EOF'
Usage: scripts/commands/run_argo.sh [wrapper options] <argo-subcommand> [argo args...]

Wrapper options:
  --kubeconfig path   Use this kubeconfig to resolve the Argo token.
  --argo-server host  Override the Kaizen Argo server.
  --namespace name    Override the default namespace (playground).
  --token value       Use this Argo bearer token instead of auto-resolving one.
  --no-http1          Do not add --argo-http1 automatically.
  -h, --help          Show this wrapper help.

Examples:
  scripts/commands/run_argo.sh list
  scripts/commands/run_argo.sh logs cads-list-s3-objects-20260422133557 --tail 200
  scripts/commands/run_argo.sh get cads-list-s3-objects-20260422133557 -o json
  scripts/commands/run_argo.sh submit deploy/argo/list_s3_objects.yaml --watch
EOF
}

has_passthrough_flag() {
    local short_flag="$1"
    local long_flag="$2"
    shift 2

    local arg=""
    while (($#)); do
        arg="$1"
        case "$arg" in
            "$short_flag"|"$long_flag"|"$long_flag"=*)
                return 0
                ;;
        esac
        shift || true
    done
    return 1
}

parse_args() {
    if (($# == 0)); then
        usage
        exit 1
    fi

    while (($#)); do
        case "$1" in
            -h|--help)
                usage
                exit 0
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
            --token)
                shift
                TOKEN_OVERRIDE="${1:-}"
                ;;
            --no-http1)
                FORCE_HTTP1=0
                ;;
            --)
                shift
                ARGO_ARGS+=("$@")
                break
                ;;
            *)
                ARGO_ARGS+=("$1")
                ;;
        esac
        shift || true
    done

    if ((${#ARGO_ARGS[@]} == 0)); then
        usage
        exit 1
    fi
}

parse_args "$@"

if [[ -z "$ARGO_NAMESPACE" || -z "$ARGO_SERVER" ]]; then
    log_error "Argo server and namespace must be non-empty."
    exit 1
fi

cads_require_cmd argo
cads_source_host_ca "$ROOT_DIR"

if [[ -z "${ARGO_TOKEN:-}" && -z "$TOKEN_OVERRIDE" && -z "$KUBECONFIG_PATH" && -z "${KUBECONFIG:-}" && -f "$default_kubeconfig" ]]; then
    log_info "Using default Kaizen kubeconfig at $default_kubeconfig"
    KUBECONFIG_PATH="$default_kubeconfig"
fi

KUBECONFIG_PATH="$(cads_resolve_kubeconfig "$KUBECONFIG_PATH" || true)"

inject_args=()
if ! has_passthrough_flag "-n" "--namespace" "${ARGO_ARGS[@]}"; then
    inject_args+=(-n "$ARGO_NAMESPACE")
fi
if ! has_passthrough_flag "-s" "--server" "${ARGO_ARGS[@]}"; then
    inject_args+=(-s "$ARGO_SERVER")
fi
if ! has_passthrough_flag "--token" "--token" "${ARGO_ARGS[@]}"; then
    TOKEN_OVERRIDE="${TOKEN_OVERRIDE:-$(cads_resolve_argo_token "$KUBECONFIG_PATH" || true)}"
    if [[ -z "$TOKEN_OVERRIDE" ]]; then
        log_error "Unable to resolve an Argo token. Set ARGO_TOKEN, pass --token, or pass --kubeconfig."
        exit 1
    fi
    inject_args+=(--token "$TOKEN_OVERRIDE")
fi
if (( FORCE_HTTP1 == 1 )) && ! has_passthrough_flag "--argo-http1" "--argo-http1" "${ARGO_ARGS[@]}"; then
    inject_args+=(--argo-http1)
fi

exec argo "${ARGO_ARGS[@]}" "${inject_args[@]}"
