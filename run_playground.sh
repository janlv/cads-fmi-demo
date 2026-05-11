#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
    cat <<'EOF'
Usage: ./run_playground.sh [--image ghcr.io/org/cads-fmi-demo:tag] [dashboard args...]

User path: Playground Dashboard
Connect the local dashboard to an existing Kaizen Playground environment.

This does not build, publish to GHCR, or start Minikube.
If --image is omitted, config/playground.env or CADS_WORKFLOW_IMAGE is used.
EOF
}

args=(--connect-existing)

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        *)
            args+=("$1")
            ;;
    esac
    shift || true
done

bash "$ROOT_DIR/prepare.sh"
exec "$ROOT_DIR/scripts/commands/run_dashboard.sh" "${args[@]}"
