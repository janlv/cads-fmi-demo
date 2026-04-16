#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"
source "$ROOT_DIR/scripts/lib/tooling.sh"

cads_setup_local_path "$ROOT_DIR"
LOCAL_BASE_DIR="$ROOT_DIR/.local"
LOCAL_BIN_DIR="$LOCAL_BASE_DIR/bin"
LOCAL_GO_DIR="$LOCAL_BASE_DIR/go"

DEFAULT_FMIL_HOME="$ROOT_DIR/.local"
FMIL_HOME_OVERRIDE=""
IMAGE="cads-fmi-demo:latest"
CONTAINER_TOOL=""

usage() {
    cat <<'EOF'
Usage: ./build.sh [--image image:tag] [--fmil-home path]

Builds the CADS FMI demo container image and the Go workflow binaries.
EOF
}

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --image)
            shift
            IMAGE="${1:-}"
            if [[ -z "$IMAGE" ]]; then
                log_error "--image expects a value"
                exit 1
            fi
            shift || true
            ;;
        --fmil-home)
            shift
            FMIL_HOME_OVERRIDE="${1:-}"
            if [[ -z "$FMIL_HOME_OVERRIDE" ]]; then
                log_error "--fmil-home expects a path"
                exit 1
            fi
            shift || true
            ;;
        *)
            log_error "Unknown argument: $1"
            usage
            exit 1
            ;;
    esac
done

if [[ -n "$FMIL_HOME_OVERRIDE" ]]; then
    FMIL_HOME="$FMIL_HOME_OVERRIDE"
elif [[ -n "${FMIL_HOME:-}" ]]; then
    FMIL_HOME="$FMIL_HOME"
else
    FMIL_HOME="$DEFAULT_FMIL_HOME"
fi
export FMIL_HOME

require_cmd() {
    cads_require_cmd "$1"
}

have_fmil() {
    [[ -d "$FMIL_HOME/include/FMI" && -f "$FMIL_HOME/lib/libfmilib_shared.so" ]]
}

ensure_fmil() {
    if have_fmil; then
        log_step "FMIL already present under $FMIL_HOME"
        return
    fi
    log_step "Installing FMIL under $FMIL_HOME"
    bash "$ROOT_DIR/scripts/install_fmil.sh" --prefix "$FMIL_HOME"
}

stage_platform_resources() {
    if ! log_stream_cmd "Staging pythonfmu resources" "$ROOT_DIR/scripts/install_platform_resources.py"; then
        exit 1
    fi
}

build_go_binaries() {
    log_step "Building Go workflow binaries"
    mkdir -p "$ROOT_DIR/bin"
    local -a go_env=(
        "GOOS="
        "GOARCH="
        "CGO_ENABLED=1"
        "GOCACHE=${GOCACHE:-/tmp/go-build}"
        "GOMODCACHE=${GOMODCACHE:-/tmp/go-mod}"
    )
    (
        cd "$ROOT_DIR/orchestrator/service"
        run_with_logged_output env "${go_env[@]}" go build -o "$ROOT_DIR/bin/cads-workflow-runner" ./cmd/cads-workflow-runner
        run_with_logged_output env "${go_env[@]}" go build -o "$ROOT_DIR/bin/cads-workflow-service" ./cmd/cads-workflow-service
    )
}

build_container_image() {
    CONTAINER_TOOL="$(cads_select_container_tool || true)"
    if [[ -z "$CONTAINER_TOOL" ]]; then
        log_error "Neither podman nor docker is available to build the container image."
        exit 1
    fi
    log_stream_cmd "Building container image $IMAGE (${CONTAINER_TOOL})" "$CONTAINER_TOOL" build -t "$IMAGE" "$ROOT_DIR"
}

ensure_fmil
export CGO_ENABLED=1
export CGO_CFLAGS="-I${FMIL_HOME}/include"
export CGO_CXXFLAGS="-I${FMIL_HOME}/include"
export CGO_LDFLAGS="-L${FMIL_HOME}/lib"

cads_ensure_go "$LOCAL_BASE_DIR" "$LOCAL_GO_DIR"
stage_platform_resources
build_go_binaries
build_container_image

log_ok "Build complete. Use ./run_local.sh or ./run_remote.sh to submit a workflow."
