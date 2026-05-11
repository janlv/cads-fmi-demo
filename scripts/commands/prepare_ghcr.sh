#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"

if [[ -z "${CADS_WORKFLOW_IMAGE:-}" && -f "$ROOT_DIR/config/playground.env" ]]; then
    # shellcheck disable=SC1091
    source "$ROOT_DIR/config/playground.env"
fi
DEFAULT_IMAGE="${CADS_WORKFLOW_IMAGE:-ghcr.io/janlv/cads-fmi-demo:playground}"
IMAGE="$DEFAULT_IMAGE"
USERNAME="${GHCR_USERNAME:-}"
INTERACTIVE=true

cads_setup_local_path "$ROOT_DIR"

usage() {
    cat <<'EOF'
Usage: scripts/commands/prepare_ghcr.sh [--image ghcr.io/org/repo:tag] [--username name] [--no-interactive]

Authenticates Podman or Docker to GitHub Container Registry (GHCR). GHCR is
GitHub's container image storage; the hosted Kaizen playground pulls the demo
workflow image from there.

Credential sources, in order:
  1. GHCR_TOKEN
  2. GITHUB_TOKEN
  3. gh auth token

If gh is available but its token cannot publish packages, this script can refresh
the GitHub CLI scopes interactively.
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
            ;;
        --username)
            shift
            USERNAME="${1:-}"
            ;;
        --no-interactive)
            INTERACTIVE=false
            ;;
        *)
            log_error "Unknown argument: $1"
            usage
            exit 1
            ;;
    esac
    shift || true
done

if [[ -z "$IMAGE" ]]; then
    log_error "--image requires a non-empty value"
    exit 1
fi
if ! cads_is_ghcr_image "$IMAGE"; then
    log_error "Image must point at ghcr.io: $IMAGE"
    exit 1
fi

container_tool="$(cads_select_container_tool || true)"
if [[ -z "$container_tool" ]]; then
    log_error "Neither podman nor docker is available for GHCR authentication."
    exit 1
fi

if [[ -n "$USERNAME" ]]; then
    export GHCR_USERNAME="$USERNAME"
fi

token="$(cads_resolve_ghcr_token || true)"

if [[ -z "$token" && "$INTERACTIVE" == true && -t 0 && -t 1 && -z "${GHCR_TOKEN:-}" && -z "${GITHUB_TOKEN:-}" && -x "$(command -v gh 2>/dev/null || true)" ]]; then
    log_step "Refreshing GitHub CLI auth for GHCR package publishing"
    if gh auth status --hostname github.com >/dev/null 2>&1; then
        gh auth refresh -h github.com -s write:packages
    else
        gh auth login -h github.com -s write:packages
    fi
    token="$(cads_resolve_ghcr_token || true)"
fi

if [[ -n "$token" ]] && cads_ensure_ghcr_login "$IMAGE" "$container_tool"; then
    log_ok "GHCR login is ready for $container_tool"
    cat <<EOF

You can now publish the current dashboard image with:
  ./run_publish.sh --skip-build

Or publish a specific local tag with:
  scripts/publish_image.sh --image $IMAGE
EOF
    exit 0
fi

cat >&2 <<'EOF'

Unable to prepare GHCR login automatically.

Use one of these options:
  gh auth refresh -h github.com -s write:packages
  scripts/commands/prepare_ghcr.sh

or:
  export GHCR_USERNAME=<github-user>
  export GHCR_TOKEN=<token-with-write-packages>
  scripts/commands/prepare_ghcr.sh
EOF
exit 1
