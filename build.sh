#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"
source "$ROOT_DIR/scripts/lib/logging.sh"

DEFAULT_FMIL_HOME="${FMIL_HOME:-$ROOT_DIR/.local}"
FMIL_HOME_ARG=""

if ! command -v docker >/dev/null 2>&1; then
    log_error "docker command not found in PATH"
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
        --fmil-home)
            shift
            FMIL_HOME_ARG="${1:-}"
            if [ -z "$FMIL_HOME_ARG" ]; then
                echo "[error] --fmil-home expects a path" >&2
                exit 1
            fi
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

if [ -n "$FMIL_HOME_ARG" ]; then
    FMIL_HOME="$FMIL_HOME_ARG"
elif [ -z "${FMIL_HOME:-}" ]; then
    FMIL_HOME="$DEFAULT_FMIL_HOME"
fi

export FMIL_HOME

have_fmil() {
    [ -d "$FMIL_HOME/include/FMI" ] && [ -f "$FMIL_HOME/lib/libfmilib_shared.so" ]
}

if ! have_fmil; then
    log_step "FMIL not found under \$FMIL_HOME ($FMIL_HOME); installing..."
    bash "$ROOT_DIR/scripts/install_fmil.sh" --prefix "$FMIL_HOME"
fi

export CGO_ENABLED="${CGO_ENABLED:-1}"
export CGO_CFLAGS="${CGO_CFLAGS:--I${FMIL_HOME}/include}"
export CGO_CXXFLAGS="${CGO_CXXFLAGS:--I${FMIL_HOME}/include}"
export CGO_LDFLAGS="${CGO_LDFLAGS:--L${FMIL_HOME}/lib}"

if [ ${#DOCKER_ARGS[@]} -eq 0 ]; then
    DOCKER_ARGS=(orchestrator)
fi

if [ ${#INSTALL_ARGS[@]} -eq 0 ]; then
    log_stream_cmd "Staging platform resources (scripts/install_platform_resources.py)" \
        "$ROOT_DIR/scripts/install_platform_resources.py"
else
    log_stream_cmd "Staging platform resources (scripts/install_platform_resources.py ${INSTALL_ARGS[*]})" \
        "$ROOT_DIR/scripts/install_platform_resources.py" "${INSTALL_ARGS[@]}"
fi

log_step "Building Go workflow binaries"
(
    cd "$ROOT_DIR/orchestrator/service"
    GO_ENV=(GOOS= GOARCH= CGO_ENABLED=1 GOCACHE="${GOCACHE:-/tmp/go-build}" GOMODCACHE="${GOMODCACHE:-/tmp/go-mod}")
    run_with_log_tail env "${GO_ENV[@]}" go build -o "$ROOT_DIR/bin/cads-workflow-runner" ./cmd/cads-workflow-runner
    run_with_log_tail env "${GO_ENV[@]}" go build -o "$ROOT_DIR/bin/cads-workflow-service" ./cmd/cads-workflow-service
)

if [ ${#DOCKER_ARGS[@]} -eq 0 ]; then
    log_stream_cmd "Building docker compose targets" docker compose build
else
    log_stream_cmd "Building docker compose targets (${DOCKER_ARGS[*]})" docker compose build "${DOCKER_ARGS[@]}"
fi

if $COPY_FMU; then
    log_step "Determining image for FMU extraction"
    IMAGE_NAME=$(docker compose config --images "${DOCKER_ARGS[@]}") || IMAGE_NAME=""
    IMAGE_NAME=$(echo "$IMAGE_NAME" | head -n 1)
    if [ -z "$IMAGE_NAME" ]; then
        log_warn "Unable to determine image name; skipping FMU copy"
    else
        TMP_CONTAINER="cads-fmi-fmu-extract-$$"
        TMP_DIR=$(mktemp -d)
        log_step "Copying FMUs from image ($IMAGE_NAME) to host"
        CONTAINER_ID=$(docker create --name "$TMP_CONTAINER" "$IMAGE_NAME")
        docker cp "$CONTAINER_ID":/app/fmu/models "$TMP_DIR"
        docker rm "$TMP_CONTAINER" >/dev/null
        mkdir -p fmu/models
        find "$TMP_DIR/models" -maxdepth 1 -type f -name '*.fmu' -exec cp {} fmu/models/ \;
        rm -rf "$TMP_DIR"
        log_ok "FMUs copied to fmu/models"
    fi
fi
