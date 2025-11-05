#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

if ! command -v docker >/dev/null 2>&1; then
    echo "[error] docker command not found in PATH" >&2
    exit 1
fi

INSTALL_ARGS=()
DOCKER_ARGS=()

while (($#)); do
    case "$1" in
        --docker)
            shift
            while (($#)); do
                DOCKER_ARGS+=("$1")
                shift
            done
            break
            ;;
        *)
            INSTALL_ARGS+=("$1")
            shift
            ;;
    esac
done

if [ ${#DOCKER_ARGS[@]} -eq 0 ]; then
    DOCKER_ARGS=(orchestrator)
fi

if [ ${#INSTALL_ARGS[@]} -eq 0 ]; then
    echo "==> Staging platform resources (scripts/install_platform_resources.py)"
    "$ROOT_DIR/scripts/install_platform_resources.py"
else
    echo "==> Staging platform resources (scripts/install_platform_resources.py ${INSTALL_ARGS[*]})"
    "$ROOT_DIR/scripts/install_platform_resources.py" "${INSTALL_ARGS[@]}"
fi

if [ ${#DOCKER_ARGS[@]} -eq 0 ]; then
    echo "==> Building docker compose targets"
    docker compose build
else
    echo "==> Building docker compose targets (${DOCKER_ARGS[*]})"
    docker compose build "${DOCKER_ARGS[@]}"
fi
