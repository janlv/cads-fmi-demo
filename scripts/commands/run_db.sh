#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"

DB_CONTAINER="${CADS_DB_CONTAINER:-cads-timescaledb}"
DB_IMAGE="${CADS_DB_IMAGE:-docker.io/timescale/timescaledb:latest-pg16}"
DB_PORT="${CADS_DB_PORT:-5432}"
DB_USER="cads"
DB_PASSWORD="cads"
DB_NAME="storhy"
DB_URL="postgres://${DB_USER}:${DB_PASSWORD}@127.0.0.1:${DB_PORT}/${DB_NAME}"
VENV_DIR="$ROOT_DIR/.local/dbvenv"
READY_TIMEOUT_S="${CADS_DB_READY_TIMEOUT:-90}"
reset=0
ingest=1

usage() {
    cat <<'EOF_USAGE'
Usage: scripts/commands/run_db.sh [--reset] [--no-ingest]

Starts (or reuses) a local TimescaleDB container that stands in for the
STOR-HY measurement database, waits until it accepts connections, and ingests
every measurement batch under data/storhy/<site>/measurements/batch-*/
(directories named batch-bad-* are skipped; they exist to test rejection).
Re-running is idempotent: the running container is reused and rows that are
already loaded are skipped.

Options:
  --reset       Remove the container (and its data) first, then start fresh.
  --no-ingest   Only start the database; do not ingest batches.
  -h, --help    Show this help.

Environment:
  CADS_DB_CONTAINER   Container name (default: cads-timescaledb)
  CADS_DB_IMAGE       Image (default: docker.io/timescale/timescaledb:latest-pg16)
  CADS_DB_PORT        Host port (default: 5432)

The ingestion uses a Python venv at .local/dbvenv with psycopg and PyYAML.
Afterwards export the printed CADS_DB_URL so the runtime reads measured
channels from the database instead of the bundled fallback CSVs.
EOF_USAGE
}

while (($# > 0)); do
    case "$1" in
        --reset)
            reset=1
            ;;
        --no-ingest)
            ingest=0
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            log_error "Unknown argument: $1"
            usage
            exit 1
            ;;
    esac
    shift
done

container_tool="$(cads_select_container_tool)"

container_state() {
    "$container_tool" inspect --format '{{.State.Running}}' "$DB_CONTAINER" 2>/dev/null || true
}

if ((reset == 1)); then
    if [[ -n "$(container_state)" ]]; then
        log_step "Removing container $DB_CONTAINER (--reset)"
        "$container_tool" rm -f "$DB_CONTAINER" >/dev/null
    fi
fi

state="$(container_state)"
if [[ "$state" == "true" ]]; then
    log_info "Reusing running container $DB_CONTAINER"
elif [[ -n "$state" ]]; then
    log_step "Starting existing container $DB_CONTAINER"
    "$container_tool" start "$DB_CONTAINER" >/dev/null
else
    log_step "Creating container $DB_CONTAINER from $DB_IMAGE"
    "$container_tool" run -d --name "$DB_CONTAINER" \
        -p "127.0.0.1:${DB_PORT}:5432" \
        -e "POSTGRES_USER=$DB_USER" \
        -e "POSTGRES_PASSWORD=$DB_PASSWORD" \
        -e "POSTGRES_DB=$DB_NAME" \
        "$DB_IMAGE" >/dev/null
fi

log_step "Waiting for the database to accept connections"
# Probe over TCP inside the container: during first-time initialisation the
# entrypoint runs a temporary server on the Unix socket only.
waited=0
until "$container_tool" exec "$DB_CONTAINER" pg_isready -q -h 127.0.0.1 -U "$DB_USER" -d "$DB_NAME" >/dev/null 2>&1; do
    if ((waited >= READY_TIMEOUT_S)); then
        log_error "Database in $DB_CONTAINER not ready after ${READY_TIMEOUT_S}s; check '$container_tool logs $DB_CONTAINER'."
        exit 1
    fi
    sleep 2
    waited=$((waited + 2))
done
log_ok "Database ready on 127.0.0.1:$DB_PORT"

if ((ingest == 1)); then
    if [[ ! -x "$VENV_DIR/bin/python" ]]; then
        cads_require_cmd python3
        log_step "Creating ingestion venv at .local/dbvenv"
        python3 -m venv "$VENV_DIR"
    fi
    if ! "$VENV_DIR/bin/python" -c 'import psycopg, yaml' >/dev/null 2>&1; then
        log_stream_cmd "Installing psycopg and PyYAML into .local/dbvenv" \
            "$VENV_DIR/bin/pip" install --quiet 'psycopg[binary]>=3.1,<3.3' pyyaml
    fi

    batch_args=()
    for batch_dir in "$ROOT_DIR"/data/storhy/*/measurements/batch-*/; do
        [[ -d "$batch_dir" ]] || continue
        batch_dir="${batch_dir%/}"
        case "$(basename "$batch_dir")" in
            batch-bad-*)
                log_info "Skipping $(basename "$batch_dir") (rejection test batch)"
                continue
                ;;
        esac
        batch_args+=(--batch "$batch_dir")
    done

    if ((${#batch_args[@]} > 0)); then
        log_step "Ingesting measurement batches"
        "$VENV_DIR/bin/python" "$ROOT_DIR/scripts/ingest_measurements.py" --db-url "$DB_URL" "${batch_args[@]}"
    else
        log_warn "No measurement batches found under data/storhy/*/measurements/"
    fi
fi

log_ok "STOR-HY database is up. Point the runtime at it with:"
printf '  export CADS_DB_URL=%s\n' "$DB_URL"
