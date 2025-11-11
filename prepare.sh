#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
LIST_DIR="$ROOT_DIR/scripts/package-lists"
LOCAL_BASE_DIR="$ROOT_DIR/.local"
LOCAL_BIN_DIR="$LOCAL_BASE_DIR/bin"
GLOBAL_BASE_DIR="${GLOBAL_BASE_DIR:-/usr/local}"
GLOBAL_BIN_DIR="${GLOBAL_BIN_DIR:-/usr/local/bin}"

GO_VERSION_REQUIRED="${GO_VERSION_REQUIRED:-1.22.2}"
GO_INSTALL_PREFIX="${GO_INSTALL_PREFIX:-/usr/local}"
GO_DOWNLOAD_BASE="${GO_DOWNLOAD_BASE:-https://go.dev/dl}"
ARGO_VERSION_REQUIRED="${ARGO_VERSION_REQUIRED:-v3.5.6}"
KUBECTL_VERSION="${KUBECTL_VERSION:-v1.30.0}"
MINIKUBE_VERSION="${MINIKUBE_VERSION:-v1.33.1}"
MINIKUBE_DRIVER_DEFAULT="${MINIKUBE_DRIVER_DEFAULT:-podman}"
if [[ -n "${MINIKUBE_DRIVER+x}" ]]; then
    MINIKUBE_DRIVER_USER_SET=true
else
    MINIKUBE_DRIVER_USER_SET=false
fi
MINIKUBE_DRIVER="${MINIKUBE_DRIVER:-$MINIKUBE_DRIVER_DEFAULT}"
MINIKUBE_AUTO_START="${MINIKUBE_AUTO_START:-true}"
INSTALL_SCOPE="local"
ARGO_INSTALL_PATH_ENV="${ARGO_INSTALL_PATH:-}"
KUBECTL_INSTALL_PATH_ENV="${KUBECTL_INSTALL_PATH:-}"
MINIKUBE_INSTALL_PATH_ENV="${MINIKUBE_INSTALL_PATH:-}"
DEFAULT_FMIL_HOME=""
ARGO_INSTALL_PATH=""
KUBECTL_INSTALL_PATH=""
MINIKUBE_INSTALL_PATH=""

usage() {
    cat <<'EOF'
Usage: ./prepare.sh [--platform <platform>] [--local|--global]

Prepare the host for running CADS FMI Co-Sim Demo.

Supported platforms:
  linux        Debian/Ubuntu-style hosts using apt-get + Podman (rootless)
  mac          macOS with MacPorts, Colima, and Docker CLI

Installation scope:
  --local      Install FMIL and Argo CLI under the repository (default, no sudo)
  --global     Install FMIL to /usr/local/fmil and Argo CLI to /usr/local/bin/argo

Kubernetes driver selection:
  --podman     Use Podman for Minikube (default; rootless supported)
  --docker     Use Docker for Minikube

The script installs the package set listed under scripts/package-lists/ and
performs lightweight sanity checks. Running build.sh afterwards builds and
executes the demo containers. Without --platform, the tool attempts to detect
the host automatically.
EOF
}

version_ge() {
    local current="$1" required="$2"
    [[ "$(printf '%s\n' "$required" "$current" | sort -V | head -n1)" == "$required" ]]
}

detect_go_arch() {
    local arch
    arch="$(uname -m)"
    case "$arch" in
        x86_64|amd64) echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *)
            log_error "Unsupported architecture for Go tarball: $arch"
            exit 1
            ;;
    esac
}

detect_platform_slug() {
    local platform="$1"
    case "$platform" in
        linux) echo "linux" ;;
        mac) echo "darwin" ;;
        *)
            log_error "Unsupported platform slug: $platform"
            exit 1
            ;;
    esac
}

install_go() {
    ensure_cmd curl "curl required to download Go"
    ensure_cmd sudo "sudo required to install Go to $GO_INSTALL_PREFIX"
    local arch tarball url tmp
    arch="$(detect_go_arch)"
    tarball="go${GO_VERSION_REQUIRED}.linux-${arch}.tar.gz"
    url="${GO_DOWNLOAD_BASE}/${tarball}"
    tmp="$(mktemp -d)"
    log_step "Downloading Go ${GO_VERSION_REQUIRED} (${arch})"
    curl -fsSL "$url" -o "$tmp/$tarball"
    log_step "Installing Go to $GO_INSTALL_PREFIX/go"
    sudo rm -rf "${GO_INSTALL_PREFIX}/go"
    sudo tar -C "$GO_INSTALL_PREFIX" -xzf "$tmp/$tarball"
    rm -rf "$tmp"
    log_step "Go ${GO_VERSION_REQUIRED} installed. Add ${GO_INSTALL_PREFIX}/go/bin to PATH if not already present."
}

ensure_go() {
    if command -v go >/dev/null 2>&1; then
        local current
        current="$(go version | awk '{print $3}' | sed 's/^go//')"
        if version_ge "$current" "$GO_VERSION_REQUIRED"; then
            log_step "Go ${current} already satisfies requirement (${GO_VERSION_REQUIRED})"
            return
        fi
        log_warn "Go ${current} detected, but ${GO_VERSION_REQUIRED} required. Reinstalling."
    else
        log_step "Go not found; installing ${GO_VERSION_REQUIRED}"
    fi
    install_go
}

detect_argo_asset() {
    local platform="$1" arch
    arch="$(detect_go_arch)"
    case "$platform" in
        linux) echo "argo-linux-${arch}.gz" ;;
        mac) echo "argo-darwin-${arch}.gz" ;;
        *)
            log_error "Unsupported platform for Argo CLI: $platform"
            exit 1
            ;;
    esac
}

install_binary() {
    local url="$1" dest="$2" label="$3"
    local dir
    dir="$(dirname "$dest")"
    if [[ "$INSTALL_SCOPE" == "local" ]]; then
        mkdir -p "$dir"
    fi
    local tmp
    tmp="$(mktemp -d)"
    local bin="$tmp/cli"
    log_step "Downloading ${label}"
    curl -fsSL "$url" -o "$bin"
    chmod +x "$bin"
    if [[ "$INSTALL_SCOPE" == "global" && "$dir" != "$LOCAL_BIN_DIR" ]]; then
        ensure_cmd sudo "sudo required to install ${label} to $dest"
        sudo install -m 0755 "$bin" "$dest"
    else
        install -m 0755 "$bin" "$dest"
    fi
    rm -rf "$tmp"
    log_step "Installed ${label} to $dest"
}

install_argo_cli() {
    local platform="$1"
    ensure_cmd curl "curl required to download Argo CLI"
    local asset tmp bin url dest dir
    asset="$(detect_argo_asset "$platform")"
    url="https://github.com/argoproj/argo-workflows/releases/download/${ARGO_VERSION_REQUIRED}/${asset}"
    dest="$ARGO_INSTALL_PATH"
    dir="$(dirname "$dest")"
    if [[ "$INSTALL_SCOPE" == "local" ]]; then
        mkdir -p "$dir"
    fi
    tmp="$(mktemp -d)"
    log_step "Downloading Argo CLI ${ARGO_VERSION_REQUIRED} (${asset})"
    curl -fsSL "$url" -o "$tmp/argo.gz"
    gunzip "$tmp/argo.gz"
    bin="$tmp/argo"
    chmod +x "$bin"
    if [[ "$INSTALL_SCOPE" == "global" && "$dir" != "$LOCAL_BIN_DIR" ]]; then
        ensure_cmd sudo "sudo required to install Argo CLI to $dest"
        sudo install -m 0755 "$bin" "$dest"
    else
        install -m 0755 "$bin" "$dest"
    fi
    rm -rf "$tmp"
    log_step "Argo CLI ${ARGO_VERSION_REQUIRED} installed to $dest"
    if [[ "$INSTALL_SCOPE" == "local" ]]; then
        echo "    Add $(dirname "$dest") to PATH for interactive shells if needed."
    fi
}

kubectl_download_url() {
    local platform="$1" arch slug
    arch="$(detect_go_arch)"
    slug="$(detect_platform_slug "$platform")"
    echo "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/${slug}/${arch}/kubectl"
}

install_kubectl_cli() {
    local platform="$1" url
    ensure_cmd curl "curl required to download kubectl"
    url="$(kubectl_download_url "$platform")"
    install_binary "$url" "$KUBECTL_INSTALL_PATH" "kubectl ${KUBECTL_VERSION}"
}

current_kubectl_version() {
    local kubectl_bin=""
    if [[ -x "$KUBECTL_INSTALL_PATH" ]]; then
        kubectl_bin="$KUBECTL_INSTALL_PATH"
    elif command -v kubectl >/dev/null 2>&1; then
        kubectl_bin="$(command -v kubectl)"
    else
        return 1
    fi
    local line
    if ! line="$("$kubectl_bin" version --client=true 2>/dev/null)"; then
        return 1
    fi
    if [[ "$line" =~ Client[[:space:]]+Version:[[:space:]]+v([0-9]+\.[0-9]+\.[0-9]+) ]]; then
        printf 'v%s\n' "${BASH_REMATCH[1]}"
        return 0
    fi
    return 1
}

ensure_kubectl_cli() {
    local platform="$1" current
    if current="$(current_kubectl_version)"; then
        if [[ "$current" == "$KUBECTL_VERSION" ]]; then
            log_step "kubectl ${current} already satisfies requirement (${KUBECTL_VERSION})"
            return
        fi
        log_warn "kubectl ${current} detected, but ${KUBECTL_VERSION} required. Reinstalling."
    else
        log_step "kubectl not found; installing ${KUBECTL_VERSION}"
    fi
    install_kubectl_cli "$platform"
}

minikube_download_url() {
    local platform="$1" slug arch
    slug="$(detect_platform_slug "$platform")"
    arch="$(detect_go_arch)"
    echo "https://storage.googleapis.com/minikube/releases/${MINIKUBE_VERSION}/minikube-${slug}-${arch}"
}

install_minikube_cli() {
    local platform="$1" url
    ensure_cmd curl "curl required to download minikube"
    url="$(minikube_download_url "$platform")"
    install_binary "$url" "$MINIKUBE_INSTALL_PATH" "minikube ${MINIKUBE_VERSION}"
}

current_minikube_version() {
    local mk_bin=""
    if [[ -x "$MINIKUBE_INSTALL_PATH" ]]; then
        mk_bin="$MINIKUBE_INSTALL_PATH"
    elif command -v minikube >/dev/null 2>&1; then
        mk_bin="$(command -v minikube)"
    else
        return 1
    fi
    local line
    if ! line="$("$mk_bin" version --short 2>/dev/null)"; then
        return 1
    fi
    if [[ "$line" =~ v[0-9]+\.[0-9]+\.[0-9]+ ]]; then
        printf '%s\n' "$line"
        return 0
    fi
    return 1
}

ensure_minikube_cli() {
    local platform="$1" current
    if current="$(current_minikube_version)"; then
        if [[ "$current" == "$MINIKUBE_VERSION" ]]; then
            log_step "minikube ${current} already satisfies requirement (${MINIKUBE_VERSION})"
            return
        fi
        log_warn "minikube ${current} detected, but ${MINIKUBE_VERSION} required. Reinstalling."
    else
        log_step "minikube not found; installing ${MINIKUBE_VERSION}"
    fi
    install_minikube_cli "$platform"
}

ensure_minikube_cluster() {
    ensure_cmd minikube "minikube CLI missing after installation."
    if [[ "$MINIKUBE_AUTO_START" != "true" ]]; then
        log_step "Skipping Minikube auto-start (MINIKUBE_AUTO_START=$MINIKUBE_AUTO_START)."
        return
    fi
    local status_output
    if status_output="$(minikube status --output=json 2>/dev/null)" && [[ -n "$status_output" ]]; then
        if printf '%s' "$status_output" | grep -q '"Host":"Running"'; then
            log_step "Minikube cluster already running."
            return
        fi
    fi
    local driver="$MINIKUBE_DRIVER"
    local start_args=(--driver="${driver}")
    if [[ "$driver" == "podman" && "$(id -u)" != "0" ]]; then
        start_args+=(--rootless)
    fi
    log_step "Starting Minikube cluster (driver=${driver})"
    if ! minikube start "${start_args[@]}"; then
        if [[ "$driver" == "docker" && "$MINIKUBE_DRIVER_USER_SET" != true ]] && command -v podman >/dev/null 2>&1; then
            log_warn "Minikube start failed with docker driver; retrying with podman."
            driver="podman"
            MINIKUBE_DRIVER="podman"
            start_args=(--driver="${driver}")
            if [[ "$(id -u)" != "0" ]]; then
                start_args+=(--rootless)
            fi
            if minikube start "${start_args[@]}"; then
                return
            fi
        fi
        log_warn "Minikube failed to start automatically. Run 'minikube start --driver=${driver}' manually for details."
    fi
}

auto_select_minikube_driver() {
    if [[ "$MINIKUBE_DRIVER_USER_SET" == true ]]; then
        return
    fi
    local podman_available=false docker_available=false docker_proxied=false
    if command -v podman >/dev/null 2>&1; then
        podman_available=true
    fi
    if command -v docker >/dev/null 2>&1; then
        local docker_real
        docker_real="$(readlink -f "$(command -v docker)" 2>/dev/null || true)"
        if [[ "$docker_real" == *podman* ]]; then
            docker_proxied=true
        elif docker version >/dev/null 2>&1; then
            docker_available=true
        fi
    fi
    case "$MINIKUBE_DRIVER" in
        podman)
            if [[ "$podman_available" == false && "$docker_available" == true ]]; then
                log_info "Podman driver requested but podman not found; switching Minikube driver to docker."
                MINIKUBE_DRIVER="docker"
            fi
            ;;
        docker)
            if { [[ "$docker_available" == false ]] || [[ "$docker_proxied" == true ]]; } && [[ "$podman_available" == true ]]; then
                log_info "Docker driver unavailable or proxied by Podman; switching Minikube driver to podman."
                MINIKUBE_DRIVER="podman"
            fi
            ;;
        *)
            :
            ;;
    esac
}

current_argo_version() {
    local argo_bin=""
    if [[ -x "$ARGO_INSTALL_PATH" ]]; then
        argo_bin="$ARGO_INSTALL_PATH"
    elif command -v argo >/dev/null 2>&1; then
        argo_bin="$(command -v argo)"
    else
        return 1
    fi
    local line=""
    if ! line="$("$argo_bin" version --short 2>/dev/null | tr -d '\r')"; then
        line="$("$argo_bin" version 2>/dev/null | head -n1 | tr -d '\r')"
    fi
    if [[ "$line" =~ (v[0-9]+\.[0-9]+\.[0-9]+) ]]; then
        printf '%s\n' "${BASH_REMATCH[1]}"
        return 0
    fi
    return 1
}

ensure_argo_cli() {
    local platform="$1" current
    if current="$(current_argo_version)"; then
        if [[ "$current" == "$ARGO_VERSION_REQUIRED" ]]; then
            log_step "Argo CLI ${current} already satisfies requirement (${ARGO_VERSION_REQUIRED})"
            return
        fi
        log_warn "Argo CLI ${current} detected, but ${ARGO_VERSION_REQUIRED} required. Reinstalling."
    else
        log_step "Argo CLI not found; installing ${ARGO_VERSION_REQUIRED}"
    fi
    install_argo_cli "$platform"
}

have_fmil() {
    local prefix="$1"
    [[ -d "$prefix/include/FMI" && -f "$prefix/lib/libfmilib_shared.so" ]]
}

ensure_fmil() {
    local prefix="$1"
    if have_fmil "$prefix"; then
        log_step "FMIL already present at $prefix"
        return
    fi
    log_step "Installing FMIL to $prefix"
    FMIL_HOME="$prefix" bash "$ROOT_DIR/scripts/install_fmil.sh" --prefix "$prefix"
}

require_file() {
    local path="$1"
    if [[ ! -f "$path" ]]; then
        log_error "Missing expected file: $path"
        exit 1
    fi
}

ensure_cmd() {
    local name="$1" message="${2:-}"
    if ! command -v "$name" >/dev/null 2>&1; then
        if [[ -n "$message" ]]; then
            log_error "$message"
        else
            log_error "Required command '$name' not found"
        fi
        exit 1
    fi
}

if [[ $# -eq 0 ]]; then
    AUTO_DETECT=true
else
    AUTO_DETECT=false
fi

PLATFORM=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --platform)
            shift
            PLATFORM="${1:-}"
            if [[ -z "$PLATFORM" ]]; then
                log_error "--platform expects an argument (linux|mac)"
                exit 1
            fi
            ;;
        --local)
            INSTALL_SCOPE="local"
            ;;
        --global)
            INSTALL_SCOPE="global"
            ;;
        --podman)
            MINIKUBE_DRIVER="podman"
            MINIKUBE_DRIVER_USER_SET=true
            ;;
        --docker)
            MINIKUBE_DRIVER="docker"
            MINIKUBE_DRIVER_USER_SET=true
            ;;
        linux|mac)
            if [[ -n "$PLATFORM" ]]; then
                log_error "Platform already specified; use --platform to override explicitly."
                exit 1
            fi
            PLATFORM="$1"
            ;;
        *)
            log_error "Unknown argument: $1"
            usage
            exit 1
            ;;
    esac
    shift
done

if [[ -n "${FMIL_HOME:-}" ]]; then
    DEFAULT_FMIL_HOME="$FMIL_HOME"
else
    if [[ "$INSTALL_SCOPE" == "global" ]]; then
        DEFAULT_FMIL_HOME="$GLOBAL_BASE_DIR/fmil"
    else
        DEFAULT_FMIL_HOME="$LOCAL_BASE_DIR"
    fi
fi

if [[ -n "$ARGO_INSTALL_PATH_ENV" ]]; then
    ARGO_INSTALL_PATH="$ARGO_INSTALL_PATH_ENV"
else
    if [[ "$INSTALL_SCOPE" == "global" ]]; then
        ARGO_INSTALL_PATH="$GLOBAL_BIN_DIR/argo"
    else
        ARGO_INSTALL_PATH="$LOCAL_BIN_DIR/argo"
    fi
fi

if [[ -n "$KUBECTL_INSTALL_PATH_ENV" ]]; then
    KUBECTL_INSTALL_PATH="$KUBECTL_INSTALL_PATH_ENV"
else
    if [[ "$INSTALL_SCOPE" == "global" ]]; then
        KUBECTL_INSTALL_PATH="$GLOBAL_BIN_DIR/kubectl"
    else
        KUBECTL_INSTALL_PATH="$LOCAL_BIN_DIR/kubectl"
    fi
fi

if [[ -n "$MINIKUBE_INSTALL_PATH_ENV" ]]; then
    MINIKUBE_INSTALL_PATH="$MINIKUBE_INSTALL_PATH_ENV"
else
    if [[ "$INSTALL_SCOPE" == "global" ]]; then
        MINIKUBE_INSTALL_PATH="$GLOBAL_BIN_DIR/minikube"
    else
        MINIKUBE_INSTALL_PATH="$LOCAL_BIN_DIR/minikube"
    fi
fi

if [[ "$INSTALL_SCOPE" == "local" ]]; then
    mkdir -p "$LOCAL_BIN_DIR"
    case ":$PATH:" in
        *":$LOCAL_BIN_DIR:"*) ;;
        *) export PATH="$LOCAL_BIN_DIR:$PATH" ;;
    esac
fi

if [[ -z "$PLATFORM" ]]; then
    if [[ "$AUTO_DETECT" == true ]]; then
        UNAME_S="$(uname -s 2>/dev/null || echo unknown)"
        case "$UNAME_S" in
            Linux)
                PLATFORM="linux"
                ;;
            Darwin)
                PLATFORM="mac"
                ;;
            *)
                log_error "Could not determine platform automatically (uname -s -> $UNAME_S)."
                echo "        Re-run with --platform linux|mac." >&2
                exit 1
                ;;
        esac
    else
        log_error "Platform not specified. Use --platform linux|mac."
        exit 1
    fi
fi

log_info "Detected platform: $PLATFORM"
log_info "Install scope: $INSTALL_SCOPE"
log_info "Minikube driver: $MINIKUBE_DRIVER"

case "$PLATFORM" in
    linux)
        ensure_cmd sudo "sudo is required to install packages."
        ensure_cmd apt-get "apt-get not found; this helper targets Debian/Ubuntu derivatives."

        PACKAGE_LIST="$LIST_DIR/linux-apt.txt"
        require_file "$PACKAGE_LIST"

        PACKAGES=()
        while IFS= read -r line || [[ -n "$line" ]]; do
            [[ -z "$line" ]] && continue
            [[ "$line" =~ ^[[:space:]]*# ]] && continue
            PACKAGES+=("$line")
        done < "$PACKAGE_LIST"
        MISSING=()
        for pkg in "${PACKAGES[@]}"; do
            if ! dpkg-query -W -f='${Status}' "$pkg" 2>/dev/null | grep -q "install ok installed"; then
                MISSING+=("$pkg")
            fi
        done

        if ((${#MISSING[@]} > 0)); then
            log_step "Installing Debian/Ubuntu packages (missing: ${MISSING[*]})"
            run_with_log_tail sudo apt-get update
            run_with_log_tail sudo apt-get install -y "${MISSING[@]}"
        else
            log_step "Required Debian/Ubuntu packages already installed."
        fi

        log_step "Checking rootless Podman mappings"
        SUBUID_LINE="$(grep -E "^${USER}:" /etc/subuid 2>/dev/null || true)"
        SUBGID_LINE="$(grep -E "^${USER}:" /etc/subgid 2>/dev/null || true)"
        if [[ -z "$SUBUID_LINE" || -z "$SUBGID_LINE" ]]; then
            log_warn "No subordinate ID range found for '${USER}'."
            echo "         Ask your administrator to reserve a unique range, e.g.:"
            echo "         sudo usermod --add-subuids 10000000-10098999 \"$USER\""
            echo "         sudo usermod --add-subgids 10000000-10098999 \"$USER\""
        else
            log_ok "/etc/subuid -> $SUBUID_LINE"
            log_ok "/etc/subgid -> $SUBGID_LINE"
        fi

        if command -v podman >/dev/null 2>&1; then
            if podman info >/dev/null 2>&1; then
                log_step "podman info succeeded; skipping 'podman system migrate'."
            else
                log_step "Running podman system migrate"
                if ! podman system migrate; then
                    log_warn "podman system migrate failed; rerun manually for details."
                fi
            fi
        else
            log_warn "podman not detected on PATH after install."
        fi

        if command -v systemctl >/dev/null 2>&1; then
            log_step "Ensuring podman.socket is available"
            if ! systemctl --user is-active podman.socket >/dev/null 2>&1; then
                if systemctl --user enable --now podman.socket >/dev/null 2>&1; then
                    log_ok "podman.socket active under the current user."
                else
                    log_warn "Could not enable podman.socket automatically."
                    echo "         Run: systemctl --user enable --now podman.socket"
                fi
            else
                log_ok "podman.socket already active."
            fi
        else
            log_warn "systemctl not available; start the service manually when needed:"
            echo "         podman system service --time=0 unix://\${XDG_RUNTIME_DIR}/podman/podman.sock"
        fi

        auto_select_minikube_driver
        ensure_go
        ensure_fmil "$DEFAULT_FMIL_HOME"
        ensure_argo_cli linux
        ensure_kubectl_cli linux
        ensure_minikube_cli linux
        ensure_minikube_cluster

        cat <<'EOF'

Done. Continue with:
  ./build.sh
  ./run.sh workflows/python_chain.yaml --mode argo
EOF
        ;;
    mac)
        ensure_cmd sudo "sudo is required to install MacPorts packages."
        ensure_cmd port "MacPorts 'port' command not found. Install MacPorts first: https://www.macports.org/"

        PACKAGE_LIST="$LIST_DIR/macports.txt"
        require_file "$PACKAGE_LIST"

        PACKAGES=()
        while IFS= read -r line || [[ -n "$line" ]]; do
            [[ -z "$line" ]] && continue
            [[ "$line" =~ ^[[:space:]]*# ]] && continue
            PACKAGES+=("$line")
        done < "$PACKAGE_LIST"
        MISSING=()
        for pkg in "${PACKAGES[@]}"; do
            if ! port installed "$pkg" 2>/dev/null | grep -q "(active)"; then
                MISSING+=("$pkg")
            fi
        done

        if ((${#MISSING[@]} > 0)); then
            log_step "Installing MacPorts packages (missing: ${MISSING[*]})"
            sudo port install "${MISSING[@]}"
        else
            log_step "Required MacPorts packages already installed."
        fi

        command -v colima >/dev/null 2>&1 || log_warn "colima not detected on PATH."
        command -v docker >/dev/null 2>&1 || log_warn "docker CLI not detected. Install it via MacPorts (package list)."
        auto_select_minikube_driver
        ensure_argo_cli mac
        ensure_kubectl_cli mac
        ensure_minikube_cli mac
        ensure_minikube_cluster

        cat <<'EOF'

Done. Continue with:
  ./build.sh
  ./run.sh workflows/python_chain.yaml --mode argo

Helpful Colima/Docker commands:
  colima start
  docker context use colima
EOF
        ;;
    *)
        log_error "Unsupported platform: $PLATFORM"
        usage
        exit 1
        ;;
esac
