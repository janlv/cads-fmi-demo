#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"
source "$ROOT_DIR/scripts/lib/logging.sh"

MINIKUBE_PROFILE="${MINIKUBE_PROFILE:-minikube}"

usage() {
    cat <<'EOF'
Usage: ./clean.sh

Removes generated artifacts (.local toolchain, bin/, caches, images) and
deletes the local Minikube profile so subsequent ./prepare.sh and ./build.sh
runs start from a blank slate.
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

cleanup_path() {
    local path="$1"
    if [[ -e "$path" ]]; then
        rm -rf "$path"
    fi
}

ensure_dir() {
    local path="$1"
    mkdir -p "$path"
}

stop_compose() {
    local bin="$1"
    if ! command -v "$bin" >/dev/null 2>&1; then
        return
    fi
    log_step "Stopping containers via $bin compose (if running)"
    if "$bin" compose down --remove-orphans >/dev/null 2>&1; then
        log_info "$bin compose stack stopped"
    else
        log_warn "$bin compose down reported no active stack or is unavailable."
    fi
}

remove_image() {
    local bin="$1" image="$2"
    if command -v "$bin" >/dev/null 2>&1; then
        if "$bin" image inspect "$image" >/dev/null 2>&1; then
            log_step "Removing container image $image via $bin"
            "$bin" image rm -f "$image" >/dev/null 2>&1 || log_warn "Failed to remove image $image with $bin"
        fi
    fi
}

log_step "Cleaning generated artifacts"

cleanup_path "data"
ensure_dir "data"

cleanup_path "create_fmu/artifacts/build"
cleanup_path "create_fmu/artifacts/cache"
ensure_dir "create_fmu/artifacts"

if [[ -d scripts/certs ]]; then
    log_step "Clearing exported certificates"
    find scripts/certs -mindepth 1 -type f -delete
fi

for VENV in ".venv" "create_fmu/.venv"; do
    if [[ -d "$VENV" ]]; then
        log_step "Removing Python virtual environment ($VENV)"
        cleanup_path "$VENV"
    fi
done

DOCKER_BIN=""
if command -v docker >/dev/null 2>&1; then
    DOCKER_BIN="docker"
    stop_compose "docker"
elif command -v podman >/dev/null 2>&1; then
    DOCKER_BIN="podman"
    stop_compose "podman"
fi

if [[ -n "$DOCKER_BIN" ]]; then
    remove_image "$DOCKER_BIN" "cads-fmi-demo:latest"
    remove_image "$DOCKER_BIN" "localhost/cads-fmi-demo:latest"
else
    log_warn "Neither docker nor podman found; skipping container cleanup."
fi

cleanup_path ".podman-tmp"

if command -v minikube >/dev/null 2>&1; then
    log_step "Deleting Minikube profile '${MINIKUBE_PROFILE}'"
    if ! minikube delete -p "$MINIKUBE_PROFILE"; then
        log_warn "Minikube delete failed; profile may not exist."
    fi
else
    log_warn "minikube command not found; skipping profile cleanup."
fi

if [[ -d "$ROOT_DIR/bin" ]]; then
    log_step "Removing Go binaries under bin/"
    cleanup_path "bin"
fi

if [[ -d "$ROOT_DIR/.local" ]]; then
    log_step "Removing local toolchain (.local)"
    cleanup_path ".local"
fi

log_ok "Clean up complete. Re-run ./prepare.sh and ./build.sh as needed."
