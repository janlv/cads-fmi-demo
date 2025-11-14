#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"
source "$ROOT_DIR/scripts/lib/logging.sh"
LOCAL_BIN_DIR="$ROOT_DIR/.local/bin"
export PATH="$LOCAL_BIN_DIR:$PATH"

DEFAULT_FMIL_HOME="${FMIL_HOME:-$ROOT_DIR/.local}"
FMIL_HOME_ARG=""
MODE=""
IMAGE="cads-fmi-demo:latest"
LOG_MAX_LINES=""
MINIKUBE_PROFILE="${MINIKUBE_PROFILE:-minikube}"
MINIKUBE_IMAGE_LOAD="${MINIKUBE_IMAGE_LOAD:-true}"

INSTALL_ARGS=()
DOCKER_ARGS=()
COPY_FMU=false
PREPARE_ARGO_ENV=false

while (($#)); do
    case "$1" in
        --max-lines)
            shift
            LOG_MAX_LINES="${1:-}"
            if [ -z "$LOG_MAX_LINES" ]; then
                echo "[error] --max-lines expects a value" >&2
                exit 1
            fi
            shift
            ;;
        --mode)
            shift
            MODE="${1:-}"
            if [ -z "$MODE" ]; then
                echo "[error] --mode expects a value" >&2
                exit 1
            fi
            shift
            ;;
        --image)
            shift
            IMAGE="${1:-}"
            if [ -z "$IMAGE" ]; then
                echo "[error] --image expects a value" >&2
                exit 1
            fi
            shift
            ;;
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

BUILD_COMPOSE=true
BUILD_LOCAL_IMAGE=false

if [ -n "$MODE" ]; then
    case "$MODE" in
        argo)
            BUILD_COMPOSE=true
            BUILD_LOCAL_IMAGE=false
            PREPARE_ARGO_ENV=true
            ;;
        local)
            BUILD_COMPOSE=false
            BUILD_LOCAL_IMAGE=true
            ;;
        *)
            log_error "Unsupported mode: $MODE"
            exit 1
            ;;
    esac
else
    PREPARE_ARGO_ENV=true
fi

if [ -n "$LOG_MAX_LINES" ]; then
    if ! cads_set_log_tail_lines "$LOG_MAX_LINES"; then
        exit 1
    fi
fi

if [ -n "$FMIL_HOME_ARG" ]; then
    FMIL_HOME="$FMIL_HOME_ARG"
elif [ -z "${FMIL_HOME:-}" ]; then
    FMIL_HOME="$DEFAULT_FMIL_HOME"
fi

export FMIL_HOME

require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        log_error "Required command '$1' not found in PATH"
        exit 1
    fi
}

build_local_image() {
    require_cmd podman
    log_stream_cmd "Building local container image $IMAGE" podman build -t "$IMAGE" "$ROOT_DIR"
}

ensure_minikube_image_tag() {
    local target="$IMAGE"
    local inspect_cmd=(minikube -p "$MINIKUBE_PROFILE" ssh -- docker image inspect)
    if "${inspect_cmd[@]}" "$target" >/dev/null 2>&1; then
        return
    fi

    local repo="$target"
    local tag="latest"
    if [[ "$target" == *:* ]]; then
        repo="${target%:*}"
        tag="${target##*:}"
    fi

    local candidates=()
    candidates+=("docker.io/localhost/${repo##localhost/}:${tag}")
    candidates+=("localhost/${repo##localhost/}:${tag}")
    candidates+=("docker.io/${repo}:${tag}")

    for candidate in "${candidates[@]}"; do
        if "${inspect_cmd[@]}" "$candidate" >/dev/null 2>&1; then
            log_step "Tagging Minikube image $candidate as $target"
            if minikube -p "$MINIKUBE_PROFILE" ssh -- docker image tag "$candidate" "$target" >/dev/null 2>&1; then
                return
            else
                log_warn "Failed to tag $candidate as $target inside Minikube"
                return
            fi
        fi
    done

    log_warn "Image $target not present inside Minikube even after load/build"
}

load_image_into_minikube() {
    if [ "$MINIKUBE_IMAGE_LOAD" != true ]; then
        return
    fi
    if ! command -v minikube >/dev/null 2>&1; then
        log_warn "minikube command not found; skipping image load into cluster"
        return
    fi

    local loaded=false

    if command -v podman >/dev/null 2>&1 && podman image exists "$IMAGE" >/dev/null 2>&1; then
        log_step "Loading image $IMAGE from podman into Minikube profile ${MINIKUBE_PROFILE}"
        if podman save "$IMAGE" | minikube -p "$MINIKUBE_PROFILE" image load -; then
            loaded=true
        else
            log_warn "Failed to stream image from podman; will try alternative methods."
        fi
    fi

    if [ "$loaded" = false ] && command -v docker >/dev/null 2>&1 && docker image inspect "$IMAGE" >/dev/null 2>&1; then
        log_step "Loading image $IMAGE from docker into Minikube profile ${MINIKUBE_PROFILE}"
        if docker save "$IMAGE" | minikube -p "$MINIKUBE_PROFILE" image load -; then
            loaded=true
        else
            log_warn "Failed to stream image from docker; will try building inside Minikube."
        fi
    fi

    if [ "$loaded" = false ]; then
        log_step "Building image $IMAGE directly inside Minikube profile ${MINIKUBE_PROFILE}"
        if ! minikube -p "$MINIKUBE_PROFILE" image build -t "$IMAGE" "$ROOT_DIR"; then
            log_warn "Unable to build image inside Minikube; workflows may hit ErrImagePull"
        fi
    fi

    ensure_minikube_image_tag
}

ensure_kube_context() {
    require_cmd kubectl
    if ! kubectl config current-context >/dev/null 2>&1; then
        local cfg_hint="${KUBECONFIG:-$HOME/.kube/config}"
        log_error "kubectl cannot determine the current context."
        log_info "Ensure your kubeconfig exists and set KUBECONFIG if needed (current hint: $cfg_hint)."
        exit 1
    fi
}

prepare_argo_environment() {
    log_step "Ensuring Kubernetes context is active"
    ensure_kube_context
    log_step "Syncing custom CA certificates into Minikube (if configured)"
    "$ROOT_DIR/scripts/install_minikube_ca.sh"
    log_step "Ensuring Argo Workflows controller is installed"
    "$ROOT_DIR/scripts/ensure_argo_workflows.sh"
}

if [ "$BUILD_COMPOSE" = true ] || [ "$COPY_FMU" = true ]; then
    require_cmd docker
fi

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
    if [ "$BUILD_COMPOSE" = true ] || [ "$COPY_FMU" = true ]; then
        DOCKER_ARGS=(orchestrator)
    fi
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

if [ "$BUILD_COMPOSE" = true ]; then
    if [ ${#DOCKER_ARGS[@]} -eq 0 ]; then
        log_stream_cmd "Building docker compose targets" docker compose build
    else
        log_stream_cmd "Building docker compose targets (${DOCKER_ARGS[*]})" docker compose build "${DOCKER_ARGS[@]}"
    fi
fi

if [ "$PREPARE_ARGO_ENV" = true ]; then
    prepare_argo_environment
fi

if [ "$PREPARE_ARGO_ENV" = true ]; then
    load_image_into_minikube
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

if [ "$BUILD_LOCAL_IMAGE" = true ]; then
    build_local_image
fi
