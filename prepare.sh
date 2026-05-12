#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"
source "$ROOT_DIR/scripts/lib/tooling.sh"

LOCAL_BASE_DIR="$ROOT_DIR/.local"
LOCAL_BIN_DIR="$LOCAL_BASE_DIR/bin"
LOCAL_GO_DIR="$LOCAL_BASE_DIR/go"
DASHBOARD_APT_PACKAGE_LIST="$ROOT_DIR/scripts/package-lists/linux-dashboard-apt.txt"
LOCAL_APT_PACKAGE_LIST="$ROOT_DIR/scripts/package-lists/linux-apt.txt"
MINIKUBE_PROFILE="${CADS_MINIKUBE_PROFILE:-minikube}"

SKIP_SYSTEM_PACKAGES=0
SKIP_RUNTIME_START=0
SKIP_CA=0
WITH_LOCAL_MINIKUBE=0
REQUIRE_CONTAINER_RUNTIME=0
QUIET=0
CERTS_DIR="$(cads_select_host_cert_dir "$ROOT_DIR")"

usage() {
    cat <<'EOF'
Usage: ./prepare.sh [options]

Prepare this computer for the CADS FMI dashboard demo.

The same preparation path is used on Linux and macOS:
  - install/check small host prerequisites where the OS supports it
  - verify age is available for encrypted kubeconfig exchange
  - install repo-local Go, Argo CLI, and kubectl under .local/
  - optionally check Podman/Docker and start a local Minikube profile

Options:
  --with-local-minikube     Start the local Minikube demo profile too.
  --require-container-runtime
                            Fail if Podman or Docker is not running.
  --cert-dir path           Use this directory for .crt/.pem company CA files.
  --skip-system-packages    Do not install Debian/Ubuntu packages.
  --skip-runtime-start      Do not try to start a stopped Podman machine.
  --skip-podman-start       Alias for --skip-runtime-start.
  --skip-ca                 Do not sync company CA files into Podman/Minikube.
  --quiet                   Suppress explanatory progress text and next steps.
  -h, --help                Show this help.

The default path is intentionally lean: it prepares the hosted dashboard client
tooling and does not require Podman/Docker. Use --with-local-minikube for the
fully local demo loop, or --require-container-runtime before build/publish work.
EOF
}

fail_age_missing() {
    cat >&2 <<'EOF'
[error] Required tool 'age' is not installed.

This demo uses age to receive the Kaizen kubeconfig safely. The kubeconfig lets
the dashboard talk to the hosted playground, so it should not be sent as plain
text over chat or email.

Install age, then run ./prepare.sh again:

  macOS with Homebrew:
    brew install age

  macOS with MacPorts:
    sudo port install age

  Debian/Ubuntu:
    sudo apt-get update
    sudo apt-get install -y age

After preparation succeeds, continue with:
  ./scripts/age_create_identity.sh
EOF
    exit 1
}

ensure_age() {
    if command -v age >/dev/null 2>&1 && command -v age-keygen >/dev/null 2>&1; then
        if [[ "$QUIET" != "1" ]]; then
            log_ok "age is installed"
        fi
        return
    fi
    fail_age_missing
}

install_linux_system_packages() {
    if [[ "$(cads_detect_os)" != "linux" || "$SKIP_SYSTEM_PACKAGES" == "1" ]]; then
        return
    fi
    if ! command -v apt-get >/dev/null 2>&1 || ! command -v dpkg-query >/dev/null 2>&1; then
        log_warn "No apt package manager detected; skipping automatic system package installation."
        return
    fi
    if ((REQUIRE_CONTAINER_RUNTIME)); then
        cads_install_linux_packages "$LOCAL_APT_PACKAGE_LIST"
    else
        cads_install_linux_packages "$DASHBOARD_APT_PACKAGE_LIST"
    fi
}

ensure_container_runtime() {
    local os_name="$1"

    if [[ "$os_name" == "darwin" && "$SKIP_RUNTIME_START" != "1" ]]; then
        if command -v podman >/dev/null 2>&1 && ! podman info >/dev/null 2>&1; then
            log_info "Podman is installed but not reachable; trying to start the default Podman machine."
            if ! log_stream_cmd "Starting Podman machine" podman machine start; then
                log_warn "Podman could not be started automatically. Docker may still be used if it is running."
            fi
        fi
    fi

    if ! cads_select_container_tool >/dev/null; then
        if [[ "$os_name" == "darwin" ]]; then
            log_error "Start Podman Desktop/Docker Desktop, or run 'podman machine start', then retry."
        else
            log_error "Install/start Podman or Docker, then retry."
        fi
        exit 1
    fi
}

sync_podman_ca_if_needed() {
    if [[ "$SKIP_CA" == "1" ]]; then
        return
    fi
    if command -v podman >/dev/null 2>&1 && podman info >/dev/null 2>&1; then
        bash "$ROOT_DIR/scripts/install_podman_ca.sh" --cert-dir "$CERTS_DIR"
    fi
}

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --with-local-minikube)
            WITH_LOCAL_MINIKUBE=1
            REQUIRE_CONTAINER_RUNTIME=1
            ;;
        --require-container-runtime)
            REQUIRE_CONTAINER_RUNTIME=1
            ;;
        --cert-dir)
            shift
            CERTS_DIR="${1:-}"
            if [[ -z "$CERTS_DIR" ]]; then
                log_error "--cert-dir expects a path"
                exit 1
            fi
            ;;
        --skip-system-packages)
            SKIP_SYSTEM_PACKAGES=1
            ;;
        --skip-runtime-start|--skip-podman-start)
            SKIP_RUNTIME_START=1
            ;;
        --skip-ca)
            SKIP_CA=1
            ;;
        --quiet)
            QUIET=1
            ;;
        *)
            log_error "Unknown argument: $1"
            usage
            exit 1
            ;;
    esac
    shift || true
done

mkdir -p "$LOCAL_BIN_DIR"
cads_setup_local_path "$ROOT_DIR"

os_name="$(cads_detect_os)"
export CADS_PREPARE_QUIET="$QUIET"

if [[ "$QUIET" != "1" ]]; then
    log_step "CADS FMI dashboard preparation"
    log_info "Preparing shared tooling for hosted dashboard and workflow runs."
    log_info "The same prepare path is used on Linux and macOS; optional build/local-cluster checks are explicit."
fi

install_linux_system_packages
ensure_age
if ((REQUIRE_CONTAINER_RUNTIME)); then
    ensure_container_runtime "$os_name"
    sync_podman_ca_if_needed
elif command -v podman >/dev/null 2>&1 && podman info >/dev/null 2>&1; then
    sync_podman_ca_if_needed
else
    if [[ "$QUIET" != "1" ]]; then
        log_info "Skipping container runtime checks; they are only needed for build/publish or local Minikube."
    fi
fi

cads_ensure_go "$LOCAL_BASE_DIR" "$LOCAL_GO_DIR"
cads_ensure_argo_cli "$LOCAL_BIN_DIR"
cads_ensure_kubectl_cli "$LOCAL_BIN_DIR"

if ((WITH_LOCAL_MINIKUBE)); then
    cads_ensure_minikube_cli "$LOCAL_BIN_DIR"
    log_info "Starting local Minikube profile '${MINIKUBE_PROFILE}'."
    cads_ensure_minikube_cluster "$MINIKUBE_PROFILE"
    if [[ "$SKIP_CA" != "1" ]]; then
        cads_sync_minikube_ca "$ROOT_DIR" "$MINIKUBE_PROFILE"
    fi
fi

if [[ "$QUIET" == "1" ]]; then
    exit 0
elif ((WITH_LOCAL_MINIKUBE)); then
    cat <<'EOF'

Preparation complete.

Hosted dashboard:
  ./run_playground.sh

Local Minikube demo:
  ./run_local_dev.sh workflows/python_chain.yaml
EOF
else
    cat <<'EOF'

Preparation complete. Continue with:
  ./run_playground.sh

Other user paths:
  ./run_publish.sh
  ./run_local_dev.sh workflows/python_chain.yaml
EOF
fi
