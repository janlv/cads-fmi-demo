#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

log() {
    printf '==> %s\n' "$1"
}

warn() {
    printf '[warn] %s\n' "$1" >&2
}

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
    log "Stopping containers via $bin compose (if running)"
    if "$bin" compose down --remove-orphans >/dev/null 2>&1; then
        log "$bin compose stack stopped"
    else
        warn "$bin compose down reported no active stack or is unavailable."
    fi
}

remove_image() {
    local bin="$1" image="$2"
    if command -v "$bin" >/dev/null 2>&1; then
        if "$bin" image inspect "$image" >/dev/null 2>&1; then
            log "Removing container image $image via $bin"
            "$bin" image rm -f "$image" >/dev/null 2>&1 || warn "Failed to remove image $image with $bin"
        fi
    fi
}

log "Cleaning generated artifacts"

cleanup_path "data"
ensure_dir "data"

cleanup_path "fmu/artifacts/build"
cleanup_path "fmu/artifacts/cache"
ensure_dir "fmu/artifacts"

if [[ -d scripts/certs ]]; then
    log "Clearing exported certificates"
    find scripts/certs -mindepth 1 -type f -delete
fi

if [[ -d ".venv" ]]; then
    log "Removing Python virtual environment (.venv)"
    cleanup_path ".venv"
fi

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
    warn "Neither docker nor podman found; skipping container cleanup."
fi

log "Clean up complete. Re-run ./prepare.sh and ./build.sh as needed."
