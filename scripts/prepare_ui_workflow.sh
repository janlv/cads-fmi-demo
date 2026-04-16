#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"

if (($# == 0)); then
    bash "$ROOT_DIR/scripts/generate_remote_workflow.sh" --help
    exit 1
fi

if [[ "$1" == "-h" || "$1" == "--help" ]]; then
    log_warn "prepare_ui_workflow.sh is deprecated; forwarding to scripts/generate_remote_workflow.sh"
    bash "$ROOT_DIR/scripts/generate_remote_workflow.sh" --help
    exit 0
fi

workflow="$1"
shift

output_supplied=0
forwarded_args=("$workflow")
while (($#)); do
    if [[ "$1" == "--output" ]]; then
        output_supplied=1
    fi
    forwarded_args+=("$1")
    shift || true
done

if [[ $output_supplied -eq 0 ]]; then
    basename="$(basename "$workflow")"
    name="${basename%.*}"
    forwarded_args+=(--output "$ROOT_DIR/deploy/argo/${name}-ui-workflow.yaml")
fi

log_warn "prepare_ui_workflow.sh is deprecated; forwarding to scripts/generate_remote_workflow.sh"
exec bash "$ROOT_DIR/scripts/generate_remote_workflow.sh" "${forwarded_args[@]}"
