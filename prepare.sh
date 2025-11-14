#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"

if [[ "$(uname -s)" != "Linux" ]]; then
    log_error "prepare.sh currently supports Linux hosts only."
    exit 1
fi

LOCAL_BASE_DIR="$ROOT_DIR/.local"
LOCAL_BIN_DIR="$LOCAL_BASE_DIR/bin"
LOCAL_GO_DIR="$LOCAL_BASE_DIR/go"
LIST_DIR="$ROOT_DIR/scripts/package-lists"
APT_PACKAGE_LIST="$LIST_DIR/linux-apt.txt"

GO_VERSION="1.22.2"
ARGO_VERSION="v3.5.6"
KUBECTL_VERSION="v1.30.0"
MINIKUBE_VERSION="v1.33.1"
MINIKUBE_PROFILE="minikube"

mkdir -p "$LOCAL_BIN_DIR"
export PATH="$LOCAL_GO_DIR/bin:$LOCAL_BIN_DIR:$PATH"

usage() {
    cat <<'EOF'
Usage: ./prepare.sh

Prepare a Linux host for the CADS FMI demo:
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

require_file() {
    if [[ ! -f "$1" ]]; then
        log_error "Missing expected file: $1"
        exit 1
    fi
}

ensure_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        log_error "Required command '$1' not found."
        exit 1
    fi
}

install_linux_packages() {
    ensure_cmd sudo
    require_file "$APT_PACKAGE_LIST"
    mapfile -t packages < <(grep -vE '^\s*(#|$)' "$APT_PACKAGE_LIST")
    if ((${#packages[@]} == 0)); then
        return
    fi
    local missing=()
    for pkg in "${packages[@]}"; do
        if ! dpkg-query -W -f='${Status}' "$pkg" 2>/dev/null | grep -q "install ok installed"; then
            missing+=("$pkg")
        fi
    done
    if ((${#missing[@]} > 0)); then
        log_step "Installing Debian packages: ${missing[*]}"
        sudo apt-get update
        sudo apt-get install -y "${missing[@]}"
    else
        log_step "Debian packages already installed"
    fi
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) printf 'amd64\n' ;;
        aarch64|arm64) printf 'arm64\n' ;;
        *)
            log_error "Unsupported architecture: $(uname -m)"
            exit 1
            ;;
    esac
}

install_go() {
    local arch tarball tmp
    arch="$(detect_arch)"
    tarball="go${GO_VERSION}.linux-${arch}.tar.gz"
    tmp="$(mktemp -d)"
    log_step "Installing Go ${GO_VERSION}"
    curl -fsSL "https://go.dev/dl/${tarball}" -o "$tmp/go.tgz"
    rm -rf "$LOCAL_GO_DIR"
    tar -C "$LOCAL_BASE_DIR" -xzf "$tmp/go.tgz"
    rm -rf "$tmp"
    log_ok "Go ${GO_VERSION} installed under $LOCAL_GO_DIR"
}

ensure_go() {
    if command -v go >/dev/null 2>&1; then
        local current
        current="$(go version | awk '{print $3}' | sed 's/^go//')"
        if [[ "$current" == "$GO_VERSION" ]]; then
            log_step "Go ${current} already installed"
            return
        fi
        log_warn "Go version ${current} found but ${GO_VERSION} required; reinstalling locally."
    fi
    install_go
}

install_binary() {
    local url="$1" dest="$2"
    local tmp
    tmp="$(mktemp -d)"
    curl -fsSL "$url" -o "$tmp/bin"
    install -m 0755 "$tmp/bin" "$dest"
    rm -rf "$tmp"
}

install_argo_cli() {
    local arch asset url tmp
    arch="$(detect_arch)"
    asset="argo-linux-${arch}.gz"
    url="https://github.com/argoproj/argo-workflows/releases/download/${ARGO_VERSION}/${asset}"
    tmp="$(mktemp -d)"
    log_step "Installing Argo CLI ${ARGO_VERSION}"
    curl -fsSL "$url" -o "$tmp/argo.gz"
    gunzip "$tmp/argo.gz"
    install -m 0755 "$tmp/argo" "$LOCAL_BIN_DIR/argo"
    rm -rf "$tmp"
}

ensure_argo_cli() {
    if command -v argo >/dev/null 2>&1; then
        local current
        current="$(argo version --short 2>/dev/null | tr -d '\r' || true)"
        if [[ "$current" == "$ARGO_VERSION" ]]; then
            log_step "Argo CLI ${current} already installed"
            return
        fi
        log_warn "Argo CLI ${current} found but ${ARGO_VERSION} required; reinstalling."
    fi
    install_argo_cli
}

install_kubectl_cli() {
    local arch url
    arch="$(detect_arch)"
    url="https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${arch}/kubectl"
    log_step "Installing kubectl ${KUBECTL_VERSION}"
    install_binary "$url" "$LOCAL_BIN_DIR/kubectl"
}

ensure_kubectl_cli() {
    if command -v kubectl >/dev/null 2>&1; then
        local current
        current="$(kubectl version --client=true --short 2>/dev/null || true)"
        if [[ "$current" == *"${KUBECTL_VERSION}"* ]]; then
            log_step "kubectl ${KUBECTL_VERSION} already installed"
            return
        fi
        log_warn "kubectl version mismatch; reinstalling ${KUBECTL_VERSION}."
    fi
    install_kubectl_cli
}

install_minikube_cli() {
    local arch url
    arch="$(detect_arch)"
    url="https://storage.googleapis.com/minikube/releases/${MINIKUBE_VERSION}/minikube-linux-${arch}"
    log_step "Installing Minikube ${MINIKUBE_VERSION}"
    install_binary "$url" "$LOCAL_BIN_DIR/minikube"
}

ensure_minikube_cli() {
    if command -v minikube >/dev/null 2>&1; then
        local current
        current="$(minikube version --short 2>/dev/null || true)"
        if [[ "$current" == "$MINIKUBE_VERSION" ]]; then
            log_step "Minikube ${current} already installed"
            return
        fi
        log_warn "Minikube version mismatch; reinstalling ${MINIKUBE_VERSION}."
    fi
    install_minikube_cli
}

select_minikube_driver() {
    if command -v podman >/dev/null 2>&1; then
        printf 'podman\n'
        return
    fi
    if command -v docker >/dev/null 2>&1; then
        printf 'docker\n'
        return
    fi
    log_error "Neither Podman nor Docker is available; install one of them to run Minikube."
    exit 1
}

ensure_minikube_cluster() {
    ensure_cmd minikube
    if minikube status -p "$MINIKUBE_PROFILE" --output=json 2>/dev/null | grep -q '"Host":"Running"'; then
        log_step "Minikube profile '${MINIKUBE_PROFILE}' already running."
        return
    fi
    local driver
    driver="$(select_minikube_driver)"
    local start_args=(start -p "$MINIKUBE_PROFILE" --driver="$driver")
    if [[ "$driver" == "podman" && "$(id -u)" != "0" ]]; then
        start_args+=("--rootless")
    fi
    log_step "Starting Minikube profile '${MINIKUBE_PROFILE}' (driver=${driver})"
    if ! minikube "${start_args[@]}"; then
        log_warn "Minikube failed to start automatically. Run 'minikube ${start_args[*]}' manually for diagnostics."
    fi
}

install_linux_packages
ensure_go
ensure_argo_cli
ensure_kubectl_cli
ensure_minikube_cli
ensure_minikube_cluster

cat <<'EOF'

Environment preparation complete. Continue with:
  ./build.sh
  ./run.sh workflows/python_chain.yaml
EOF
