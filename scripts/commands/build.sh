#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"
source "$ROOT_DIR/scripts/lib/tooling.sh"

cads_setup_local_path "$ROOT_DIR"
LOCAL_BASE_DIR="$ROOT_DIR/.local"
LOCAL_BIN_DIR="$LOCAL_BASE_DIR/bin"
LOCAL_GO_DIR="$LOCAL_BASE_DIR/go"
LOCAL_GO_BUILD_CACHE="$ROOT_DIR/.local/go-build"
LOCAL_GO_MOD_CACHE="$ROOT_DIR/.local/go-mod"
STATE_DIR="$ROOT_DIR/.local/state"
BUILD_STATE_FILE="$STATE_DIR/build-image.env"

DEFAULT_FMIL_HOME="$ROOT_DIR/.local"
FMIL_HOME_OVERRIDE=""
IMAGE="cads-fmi-demo:latest"
CONTAINER_TOOL=""

usage() {
    cat <<'EOF'
Usage: scripts/commands/build.sh [--image image:tag] [--fmil-home path]

Builds the CADS FMI demo container image and the Go workflow binaries.
EOF
}

save_build_state() {
    mkdir -p "$STATE_DIR"
    cat >"$BUILD_STATE_FILE" <<EOF
last_built_image="$IMAGE"
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

log_info "Build target image: $IMAGE"

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
    [[ -d "$FMIL_HOME/include/FMI" ]] &&
        ([[ -f "$FMIL_HOME/lib/libfmilib_shared.so" ]] ||
            [[ -f "$FMIL_HOME/lib/libfmilib_shared.dylib" ]] ||
            [[ -f "$FMIL_HOME/bin/fmilib_shared.dll" ]])
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
    mkdir -p "$ROOT_DIR/bin" "$LOCAL_GO_BUILD_CACHE" "$LOCAL_GO_MOD_CACHE"
    local -a runner_env=(
        "GOOS="
        "GOARCH="
        "CGO_ENABLED=1"
        "GOCACHE=${GOCACHE:-$LOCAL_GO_BUILD_CACHE}"
        "GOMODCACHE=${GOMODCACHE:-$LOCAL_GO_MOD_CACHE}"
    )
    local -a service_env=(
        "GOOS="
        "GOARCH="
        "CGO_ENABLED=0"
        "GOCACHE=${GOCACHE:-$LOCAL_GO_BUILD_CACHE}"
        "GOMODCACHE=${GOMODCACHE:-$LOCAL_GO_MOD_CACHE}"
    )
    (
        cd "$ROOT_DIR/orchestrator/service"
        run_with_logged_output env "${runner_env[@]}" go build -o "$ROOT_DIR/bin/cads-workflow-runner" ./cmd/cads-workflow-runner
        run_with_logged_output env "${service_env[@]}" go build -o "$ROOT_DIR/bin/cads-workflow-service" ./cmd/cads-workflow-service
    )
}

build_container_image() {
    CONTAINER_TOOL="$(cads_select_container_tool || true)"
    if [[ -z "$CONTAINER_TOOL" ]]; then
        log_error "Neither podman nor docker is available to build the container image."
        exit 1
    fi
    cads_stage_host_certs "$ROOT_DIR" "$ROOT_DIR/scripts/certs"
    local certs_sha="none"
    if cads_has_cert_files "$ROOT_DIR/scripts/certs"; then
        certs_sha="$(find "$ROOT_DIR/scripts/certs" -maxdepth 1 -type f \( -name '*.crt' -o -name '*.pem' \) -exec cksum {} \; | sort | cksum | awk '{print $1 "-" $2}')"
    fi
    log_stream_cmd "Building container image $IMAGE (${CONTAINER_TOOL})" "$CONTAINER_TOOL" build \
        --build-arg "CADS_CERTS_SHA=$certs_sha" \
        --build-arg "GOLANG_VERSION=$CADS_GO_VERSION" \
        -t "$IMAGE" "$ROOT_DIR"
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

save_build_state

log_ok "Build complete for image $IMAGE. Use ./run_local_dev.sh or scripts/commands/run_remote.sh to submit a workflow."
