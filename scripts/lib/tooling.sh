#!/usr/bin/env bash
# Shared CLI/tool installation helpers for repo scripts.

if [[ "${CADS_TOOLING_SH_LOADED:-}" != "$BASHPID" ]]; then
    CADS_GO_VERSION="${CADS_GO_VERSION:-1.22.2}"
    CADS_ARGO_VERSION="${CADS_ARGO_VERSION:-v3.5.6}"
    CADS_KUBECTL_VERSION="${CADS_KUBECTL_VERSION:-v1.30.0}"
    CADS_MINIKUBE_VERSION="${CADS_MINIKUBE_VERSION:-v1.33.1}"

    cads_detect_arch() {
        case "$(uname -m)" in
            x86_64|amd64) printf 'amd64\n' ;;
            aarch64|arm64) printf 'arm64\n' ;;
            *)
                log_error "Unsupported architecture: $(uname -m)"
                exit 1
                ;;
        esac
    }

    cads_install_linux_packages() {
        local apt_package_list="$1"
        cads_require_cmd sudo
        if [[ ! -f "$apt_package_list" ]]; then
            log_error "Missing expected file: $apt_package_list"
            exit 1
        fi
        mapfile -t packages < <(grep -vE '^\s*(#|$)' "$apt_package_list")
        if ((${#packages[@]} == 0)); then
            return
        fi
        local missing=()
        local pkg=""
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

    cads_install_binary() {
        local url="$1"
        local dest="$2"
        local tmp
        tmp="$(mktemp -d)"
        curl -fsSL "$url" -o "$tmp/bin"
        install -m 0755 "$tmp/bin" "$dest"
        rm -rf "$tmp"
    }

    cads_install_go() {
        local local_base_dir="$1"
        local local_go_dir="$2"
        local arch tarball tmp
        arch="$(cads_detect_arch)"
        tarball="go${CADS_GO_VERSION}.linux-${arch}.tar.gz"
        tmp="$(mktemp -d)"
        log_step "Installing Go ${CADS_GO_VERSION}"
        curl -fsSL "https://go.dev/dl/${tarball}" -o "$tmp/go.tgz"
        rm -rf "$local_go_dir"
        tar -C "$local_base_dir" -xzf "$tmp/go.tgz"
        rm -rf "$tmp"
        log_ok "Go ${CADS_GO_VERSION} installed under $local_go_dir"
    }

    cads_ensure_go() {
        local local_base_dir="$1"
        local local_go_dir="$2"
        if command -v go >/dev/null 2>&1; then
            local current
            current="$(go version | awk '{print $3}' | sed 's/^go//')"
            if [[ "$current" == "$CADS_GO_VERSION" ]]; then
                log_step "Go ${current} already installed"
                return
            fi
            log_warn "Go version ${current} found but ${CADS_GO_VERSION} required; reinstalling locally."
        fi
        cads_install_go "$local_base_dir" "$local_go_dir"
    }

    cads_install_argo_cli() {
        local local_bin_dir="$1"
        local arch asset url tmp
        arch="$(cads_detect_arch)"
        asset="argo-linux-${arch}.gz"
        url="https://github.com/argoproj/argo-workflows/releases/download/${CADS_ARGO_VERSION}/${asset}"
        tmp="$(mktemp -d)"
        log_step "Installing Argo CLI ${CADS_ARGO_VERSION}"
        curl -fsSL "$url" -o "$tmp/argo.gz"
        gunzip "$tmp/argo.gz"
        install -m 0755 "$tmp/argo" "$local_bin_dir/argo"
        rm -rf "$tmp"
    }

    cads_ensure_argo_cli() {
        local local_bin_dir="$1"
        if command -v argo >/dev/null 2>&1; then
            local current
            current="$(argo version --short 2>/dev/null | tr -d '\r' || true)"
            if [[ "$current" == "$CADS_ARGO_VERSION" ]]; then
                log_step "Argo CLI ${current} already installed"
                return
            fi
            log_warn "Argo CLI ${current} found but ${CADS_ARGO_VERSION} required; reinstalling."
        fi
        cads_install_argo_cli "$local_bin_dir"
    }

    cads_install_kubectl_cli() {
        local local_bin_dir="$1"
        local arch url
        arch="$(cads_detect_arch)"
        url="https://dl.k8s.io/release/${CADS_KUBECTL_VERSION}/bin/linux/${arch}/kubectl"
        log_step "Installing kubectl ${CADS_KUBECTL_VERSION}"
        cads_install_binary "$url" "$local_bin_dir/kubectl"
    }

    cads_ensure_kubectl_cli() {
        local local_bin_dir="$1"
        if command -v kubectl >/dev/null 2>&1; then
            local current
            current="$(kubectl version --client=true --short 2>/dev/null || true)"
            if [[ "$current" == *"${CADS_KUBECTL_VERSION}"* ]]; then
                log_step "kubectl ${CADS_KUBECTL_VERSION} already installed"
                return
            fi
            log_warn "kubectl version mismatch; reinstalling ${CADS_KUBECTL_VERSION}."
        fi
        cads_install_kubectl_cli "$local_bin_dir"
    }

    cads_install_minikube_cli() {
        local local_bin_dir="$1"
        local arch url
        arch="$(cads_detect_arch)"
        url="https://storage.googleapis.com/minikube/releases/${CADS_MINIKUBE_VERSION}/minikube-linux-${arch}"
        log_step "Installing Minikube ${CADS_MINIKUBE_VERSION}"
        cads_install_binary "$url" "$local_bin_dir/minikube"
    }

    cads_ensure_minikube_cli() {
        local local_bin_dir="$1"
        if command -v minikube >/dev/null 2>&1; then
            local current
            current="$(minikube version --short 2>/dev/null || true)"
            if [[ "$current" == "$CADS_MINIKUBE_VERSION" ]]; then
                log_step "Minikube ${current} already installed"
                return
            fi
            log_warn "Minikube version mismatch; reinstalling ${CADS_MINIKUBE_VERSION}."
        fi
        cads_install_minikube_cli "$local_bin_dir"
    }

    cads_select_minikube_driver() {
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

    cads_ensure_minikube_cluster() {
        local minikube_profile="$1"
        cads_require_cmd minikube
        if minikube status -p "$minikube_profile" --output=json 2>/dev/null | grep -q '"Host":"Running"'; then
            log_step "Minikube profile '${minikube_profile}' already running."
            return
        fi
        local driver
        driver="$(cads_select_minikube_driver)"
        local -a start_args=(start -p "$minikube_profile" --driver="$driver")
        if [[ "$driver" == "podman" && "$(id -u)" != "0" ]]; then
            start_args+=("--rootless")
        fi
        log_step "Starting Minikube profile '${minikube_profile}' (driver=${driver})"
        if ! minikube "${start_args[@]}"; then
            log_warn "Minikube failed to start automatically. Run 'minikube ${start_args[*]}' manually for diagnostics."
        fi
    }

    CADS_TOOLING_SH_LOADED="$BASHPID"
fi
