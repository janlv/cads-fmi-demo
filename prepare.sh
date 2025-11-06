#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIST_DIR="$ROOT_DIR/scripts/package-lists"

usage() {
    cat <<'EOF'
Usage: ./prepare.sh [--platform <platform>]

Prepare the host for running CADS FMI Co-Sim Demo.

Supported platforms:
  linux        Debian/Ubuntu-style hosts using apt-get + Podman (rootless)
  mac          macOS with MacPorts, Colima, and Docker CLI

The script installs the package set listed under scripts/package-lists/ and
performs lightweight sanity checks. Running build.sh afterwards builds and
executes the demo containers. Without --platform, the tool attempts to detect
the host automatically.
EOF
}

require_file() {
    local path="$1"
    if [[ ! -f "$path" ]]; then
        echo "[error] Missing expected file: $path" >&2
        exit 1
    fi
}

ensure_cmd() {
    local name="$1" message="${2:-}"
    if ! command -v "$name" >/dev/null 2>&1; then
        if [[ -n "$message" ]]; then
            echo "[error] $message" >&2
        else
            echo "[error] Required command '$name' not found" >&2
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
                echo "[error] --platform expects an argument (linux|mac)" >&2
                exit 1
            fi
            ;;
        linux|mac)
            if [[ -n "$PLATFORM" ]]; then
                echo "[error] Platform already specified; use --platform to override explicitly." >&2
                exit 1
            fi
            PLATFORM="$1"
            ;;
        *)
            echo "[error] Unknown argument: $1" >&2
            usage
            exit 1
            ;;
    esac
    shift
done

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
                echo "[error] Could not determine platform automatically (uname -s -> $UNAME_S)." >&2
                echo "        Re-run with --platform linux|mac." >&2
                exit 1
                ;;
        esac
        echo "[info] Detected platform: $PLATFORM"
    else
        echo "[error] Platform not specified. Use --platform linux|mac." >&2
        exit 1
    fi
fi

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
            echo "==> Installing Debian/Ubuntu packages (missing: ${MISSING[*]})"
            sudo apt-get update
            sudo apt-get install -y "${MISSING[@]}"
        else
            echo "==> Required Debian/Ubuntu packages already installed."
        fi

        echo "==> Checking rootless Podman mappings"
        SUBUID_LINE="$(grep -E "^${USER}:" /etc/subuid 2>/dev/null || true)"
        SUBGID_LINE="$(grep -E "^${USER}:" /etc/subgid 2>/dev/null || true)"
        if [[ -z "$SUBUID_LINE" || -z "$SUBGID_LINE" ]]; then
            echo "  [warn] No subordinate ID range found for '${USER}'."
            echo "         Ask your administrator to reserve a unique range, e.g.:"
            echo "         sudo usermod --add-subuids 10000000-10098999 \"$USER\""
            echo "         sudo usermod --add-subgids 10000000-10098999 \"$USER\""
        else
            echo "  [ok] /etc/subuid -> $SUBUID_LINE"
            echo "  [ok] /etc/subgid -> $SUBGID_LINE"
        fi

        if command -v podman >/dev/null 2>&1; then
            if podman info >/dev/null 2>&1; then
                echo "==> podman info succeeded; skipping 'podman system migrate'."
            else
                echo "==> Running podman system migrate"
                podman system migrate || echo "  [warn] podman system migrate failed; rerun manually for details."
            fi
        else
            echo "  [warn] podman not detected on PATH after install."
        fi

        if command -v systemctl >/dev/null 2>&1; then
            echo "==> Ensuring podman.socket is available"
            if ! systemctl --user is-active podman.socket >/dev/null 2>&1; then
                if systemctl --user enable --now podman.socket >/dev/null 2>&1; then
                    echo "  [ok] podman.socket active under the current user."
                else
                    echo "  [warn] Could not enable podman.socket automatically."
                    echo "         Run: systemctl --user enable --now podman.socket"
                fi
            else
                echo "  [ok] podman.socket already active."
            fi
        else
            echo "  [warn] systemctl not available; start the service manually when needed:"
            echo "         podman system service --time=0 unix://\${XDG_RUNTIME_DIR}/podman/podman.sock"
        fi

        cat <<'EOF'

Done. Continue with:
  ./build.sh
  docker compose up orchestrator   # or: podman compose up orchestrator
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
            echo "==> Installing MacPorts packages (missing: ${MISSING[*]})"
            sudo port install "${MISSING[@]}"
        else
            echo "==> Required MacPorts packages already installed."
        fi

        command -v colima >/dev/null 2>&1 || echo "  [warn] colima not detected on PATH."
        command -v docker >/dev/null 2>&1 || echo "  [warn] docker CLI not detected. Install it via MacPorts (package list)."

        cat <<'EOF'

Done. Continue with:
  ./build.sh
  docker compose up orchestrator

Helpful Colima/Docker commands:
  colima start
  docker context use colima
EOF
        ;;
    *)
        echo "[error] Unsupported platform: $PLATFORM" >&2
        usage
        exit 1
        ;;
esac
