#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"

log_warn "run.sh is deprecated; forwarding to ./run_local.sh"
exec "$ROOT_DIR/run_local.sh" "$@"
