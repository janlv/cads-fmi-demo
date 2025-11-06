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
COPY_FMU=false

while (($#)); do
    case "$1" in
        --copy-fmu)
            COPY_FMU=true
            shift
            ;;
        --docker)
            shift
            while (($#)); do
                case "$1" in
                    --*)
                        break
                        ;;
                    *)
                        DOCKER_ARGS+=("$1")
                        shift
                        ;;
                esac
            done
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

if $COPY_FMU; then
    echo "==> Determining image for FMU extraction"
    IMAGE_NAME=$(docker compose config --images "${DOCKER_ARGS[@]}") || IMAGE_NAME=""
    IMAGE_NAME=$(echo "$IMAGE_NAME" | head -n 1)
    if [ -z "$IMAGE_NAME" ]; then
        echo "[warn] Unable to determine image name; skipping FMU copy" >&2
    else
        TMP_CONTAINER="cads-fmi-fmu-extract-$$"
        echo "==> Copying FMUs from image ($IMAGE_NAME) to host"
        CONTAINER_ID=$(docker create --name "$TMP_CONTAINER" "$IMAGE_NAME")
        mkdir -p fmu/artifacts
        rm -rf fmu/artifacts/build
        docker cp "$CONTAINER_ID":/app/fmu/artifacts/build ./fmu/artifacts/
        docker rm "$TMP_CONTAINER" >/dev/null
        echo "==> FMUs copied to fmu/artifacts/build"
    fi
fi
