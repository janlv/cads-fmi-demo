#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"
source "$ROOT_DIR/scripts/lib/logging.sh"

LOCAL_BIN_DIR="$ROOT_DIR/.local/bin"
LOCAL_GO_BIN="$ROOT_DIR/.local/go/bin"
export PATH="$LOCAL_GO_BIN:$LOCAL_BIN_DIR:$PATH"

DEFAULT_FMIL_HOME="$ROOT_DIR/.local"
FMIL_HOME_OVERRIDE=""
IMAGE="cads-fmi-demo:latest"
MINIKUBE_PROFILE="minikube"
ARGO_NAMESPACE="argo"
CONTAINER_TOOL=""

usage() {
    cat <<'EOF'
Usage: ./build.sh [--image image:tag] [--fmil-home path]

Builds the CADS FMI demo container image, ensures the Argo controller is
installed in Minikube, and loads the freshly built image into the cluster.
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
    if ! command -v "$1" >/dev/null 2>&1; then
        log_error "Required command '$1' not found in PATH."
        exit 1
    fi
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
    if command -v podman >/dev/null 2>&1; then
        CONTAINER_TOOL="podman"
        log_stream_cmd "Building container image $IMAGE (podman)" podman build -t "$IMAGE" "$ROOT_DIR"
        return
    fi
    if command -v docker >/dev/null 2>&1; then
        CONTAINER_TOOL="docker"
        log_stream_cmd "Building container image $IMAGE (docker)" docker build -t "$IMAGE" "$ROOT_DIR"
        return
    fi
    log_error "Neither podman nor docker is available to build the container image."
    exit 1
}

install_ca_into_minikube() {
    log_step "Syncing custom CA certificates into Minikube"
    if ! bash "$ROOT_DIR/scripts/install_minikube_ca.sh" --profile "$MINIKUBE_PROFILE"; then
        log_warn "Unable to install custom CA certificates inside Minikube; continuing without them."
    fi
}

ensure_argo_controller() {
    log_step "Ensuring Argo Workflows is installed in Minikube"
    bash "$ROOT_DIR/scripts/ensure_argo_workflows.sh" --namespace "$ARGO_NAMESPACE"
}

load_image_into_minikube() {
    if ! command -v minikube >/dev/null 2>&1; then
        log_warn "minikube command not found; skipping image load"
        return
    fi
    log_step "Loading $IMAGE into Minikube profile ${MINIKUBE_PROFILE}"
    if minikube image load -p "$MINIKUBE_PROFILE" "$IMAGE" >/dev/null 2>&1; then
        return
    fi
    log_warn "minikube image load failed; falling back to streaming the image."
    if [[ "$CONTAINER_TOOL" == "podman" ]] && command -v podman >/dev/null 2>&1; then
        if podman image exists "$IMAGE" >/dev/null 2>&1; then
            if podman save "$IMAGE" | minikube -p "$MINIKUBE_PROFILE" image load -; then
                return
            fi
        fi
    elif [[ "$CONTAINER_TOOL" == "docker" ]] && command -v docker >/dev/null 2>&1; then
        if docker image inspect "$IMAGE" >/dev/null 2>&1; then
            if docker save "$IMAGE" | minikube -p "$MINIKUBE_PROFILE" image load -; then
                return
            fi
        fi
    fi
    log_warn "Unable to preload $IMAGE into Minikube; workflows may need to pull the tag manually."
}

ensure_fmil
export CGO_ENABLED=1
export CGO_CFLAGS="-I${FMIL_HOME}/include"
export CGO_CXXFLAGS="-I${FMIL_HOME}/include"
export CGO_LDFLAGS="-L${FMIL_HOME}/lib"

stage_platform_resources
build_go_binaries
build_container_image
install_ca_into_minikube
ensure_argo_controller
load_image_into_minikube

log_ok "Build complete. Use ./run.sh <workflow.yaml> to submit a workflow."
