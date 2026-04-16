#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"
source "$ROOT_DIR/scripts/lib/tooling.sh"

if [[ "$(uname -s)" != "Linux" ]]; then
    log_error "prepare_local.sh currently supports Linux hosts only."
    exit 1
fi

LOCAL_BASE_DIR="$ROOT_DIR/.local"
LOCAL_BIN_DIR="$LOCAL_BASE_DIR/bin"
LOCAL_GO_DIR="$LOCAL_BASE_DIR/go"
LIST_DIR="$ROOT_DIR/scripts/package-lists"
APT_PACKAGE_LIST="$LIST_DIR/linux-apt.txt"
MINIKUBE_PROFILE="minikube"

mkdir -p "$LOCAL_BIN_DIR"
cads_setup_local_path "$ROOT_DIR"

usage() {
    cat <<'EOF'
Usage: ./prepare_local.sh

Prepare a Linux host for the local CADS FMI demo:
  - installs Podman + helper packages via apt
  - downloads Go, Argo CLI, kubectl, and Minikube into .local/
  - starts a Minikube profile (driver picked automatically)
EOF
}

if (($# > 0)); then
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        *)
            log_error "Unknown argument: $1"
            usage
            exit 1
            ;;
    esac
fi

cads_install_linux_packages "$APT_PACKAGE_LIST"
cads_ensure_go "$LOCAL_BASE_DIR" "$LOCAL_GO_DIR"
cads_ensure_argo_cli "$LOCAL_BIN_DIR"
cads_ensure_kubectl_cli "$LOCAL_BIN_DIR"
cads_ensure_minikube_cli "$LOCAL_BIN_DIR"
cads_ensure_minikube_cluster "$MINIKUBE_PROFILE"

cat <<'EOF'

Local environment preparation complete. Continue with:
  ./build.sh
  ./run_local.sh workflows/python_chain.yaml
EOF
