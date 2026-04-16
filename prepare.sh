#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"

log_warn "prepare.sh is deprecated; forwarding to ./prepare_local.sh"
exec "$ROOT_DIR/prepare_local.sh" "$@"
