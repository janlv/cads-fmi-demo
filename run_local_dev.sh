#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKFLOW="workflows/python_chain.yaml"
SKIP_BUILD=0
local_args=()

usage() {
    cat <<'EOF'
Usage: ./run_local_dev.sh [workflow.yaml] [--skip-build] [run_local args...]

User path: Local Dev
Build and test one workflow/model quickly in local Minikube. This does not use
Kaizen Playground, does not publish to GHCR, and does not start a dashboard.
EOF
}

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --skip-build)
            SKIP_BUILD=1
            ;;
        --*)
            local_args+=("$1")
            ;;
        *)
            WORKFLOW="$1"
            ;;
    esac
    shift || true
done

bash "$ROOT_DIR/prepare.sh" --with-local-minikube
if (( !SKIP_BUILD )); then
    bash "$ROOT_DIR/scripts/commands/build.sh"
fi
exec "$ROOT_DIR/scripts/commands/run_local.sh" "$WORKFLOW" "${local_args[@]}"
