#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -z "${CADS_WORKFLOW_IMAGE:-}" && -f "$ROOT_DIR/config/playground.env" ]]; then
    # shellcheck disable=SC1091
    source "$ROOT_DIR/config/playground.env"
fi
IMAGE="${CADS_WORKFLOW_IMAGE:-}"
SKIP_BUILD=0
PLAYGROUND_PLATFORM="${CADS_PLAYGROUND_IMAGE_PLATFORM:-linux/amd64}"
dashboard_args=()
prepare_remote_args=()

usage() {
    cat <<'EOF'
Usage: ./run_publish.sh [--image ghcr.io/org/cads-fmi-demo:tag] [--platform linux/amd64] [--skip-build] [dashboard args...]

User path: Publish to Playground
Build locally, publish the workflow image to GHCR, prepare the Kaizen
Playground, and start the local dashboard against that published image.

If --image is omitted, config/playground.env or CADS_WORKFLOW_IMAGE is used.
This publishes the full current repo image, not one workflow file in isolation.
The Playground image is built for linux/amd64 unless --platform or
CADS_PLAYGROUND_IMAGE_PLATFORM overrides it.
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
                echo "[error] --image expects a value" >&2
                exit 1
            fi
            ;;
        --image=*)
            IMAGE="${1#*=}"
            if [[ -z "$IMAGE" ]]; then
                echo "[error] --image expects a value" >&2
                exit 1
            fi
            ;;
        --skip-build)
            SKIP_BUILD=1
            ;;
        --platform)
            shift
            PLAYGROUND_PLATFORM="${1:-}"
            if [[ -z "$PLAYGROUND_PLATFORM" ]]; then
                echo "[error] --platform expects a value" >&2
                exit 1
            fi
            ;;
        --platform=*)
            PLAYGROUND_PLATFORM="${1#*=}"
            if [[ -z "$PLAYGROUND_PLATFORM" ]]; then
                echo "[error] --platform expects a value" >&2
                exit 1
            fi
            ;;
        --kubeconfig|--argo-server)
            flag="$1"
            shift
            value="${1:-}"
            if [[ -z "$value" ]]; then
                echo "[error] $flag expects a value" >&2
                exit 1
            fi
            prepare_remote_args+=("$flag" "$value")
            dashboard_args+=("$flag" "$value")
            ;;
        --namespace)
            shift
            value="${1:-}"
            if [[ -z "$value" ]]; then
                echo "[error] --namespace expects a value" >&2
                exit 1
            fi
            prepare_remote_args+=(--namespace "$value")
            dashboard_args+=(--argo-namespace "$value")
            ;;
        --kubeconfig=*|--argo-server=*)
            prepare_remote_args+=("$1")
            dashboard_args+=("$1")
            ;;
        --namespace=*)
            value="${1#*=}"
            prepare_remote_args+=("$1")
            dashboard_args+=("--argo-namespace=$value")
            ;;
        *)
            dashboard_args+=("$1")
            ;;
    esac
    shift || true
done

if [[ -z "$IMAGE" ]]; then
    echo "[error] No image configured. Set CADS_WORKFLOW_IMAGE, edit config/playground.env, or pass --image." >&2
    usage
    exit 1
fi

run_prepare_remote() {
    local -a cmd=(bash "$ROOT_DIR/scripts/commands/prepare_remote.sh" --image "$IMAGE")
    if ((${#prepare_remote_args[@]} > 0)); then
        cmd+=("${prepare_remote_args[@]}")
    fi
    "${cmd[@]}"
}

run_dashboard() {
    local -a cmd=("$ROOT_DIR/scripts/commands/run_dashboard.sh" --connect-existing --image "$IMAGE")
    if ((${#dashboard_args[@]} > 0)); then
        cmd+=("${dashboard_args[@]}")
    fi
    exec "${cmd[@]}"
}

bash "$ROOT_DIR/prepare.sh" --require-container-runtime
if (( !SKIP_BUILD )); then
    bash "$ROOT_DIR/scripts/commands/build.sh" --image "$IMAGE" --platform "$PLAYGROUND_PLATFORM"
fi
bash "$ROOT_DIR/scripts/commands/prepare_ghcr.sh" --image "$IMAGE" --quiet
run_prepare_remote
run_dashboard
