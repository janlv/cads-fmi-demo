#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"

if [[ -f "$ROOT_DIR/scripts/host_ca_env.sh" ]]; then
    # shellcheck disable=SC1090
    source "$ROOT_DIR/scripts/host_ca_env.sh" "$ROOT_DIR" >/dev/null 2>&1 || true
fi

export PATH="$ROOT_DIR/.local/bin:$PATH"

if [[ ! -x "$ROOT_DIR/bin/cads-workflow-service" ]]; then
    log_error "Missing bin/cads-workflow-service. Run ./build.sh first."
    exit 1
fi

default_kubeconfig="$HOME/Kaizen_CADS/kubeconfig"
have_explicit_kubeconfig=0

for arg in "$@"; do
    case "$arg" in
        --kubeconfig|--kubeconfig=*)
            have_explicit_kubeconfig=1
            break
            ;;
    esac
done

extra_args=()
if [[ -z "${ARGO_TOKEN:-}" && -z "${KUBECONFIG:-}" && $have_explicit_kubeconfig -eq 0 && -f "$default_kubeconfig" ]]; then
    log_info "Using default Kaizen kubeconfig at $default_kubeconfig"
    extra_args+=(--kubeconfig "$default_kubeconfig")
fi

exec "$ROOT_DIR/bin/cads-workflow-service" --serve --addr :8080 --workdir "$ROOT_DIR" "${extra_args[@]}" "$@"
