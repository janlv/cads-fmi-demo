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
next_batch=0
notify=0
NOTIFY_URL="${CADS_DASHBOARD_URL:-http://127.0.0.1:8080}"
explicit_batches=()

usage() {
    cat <<'EOF_USAGE'
Usage: scripts/commands/run_db.sh [--reset] [--no-ingest] [--batch DIR]...
                                  [--next] [--notify] [--notify-url URL]

Starts (or reuses) a local TimescaleDB container that stands in for the
STOR-HY measurement database, waits until it accepts connections, and ingests
every measurement batch under data/storhy/<site>/measurements/batch-*/
(directories named batch-bad-* are skipped; they exist to test rejection).
Re-running is idempotent: the running container is reused and rows that are
already loaded are skipped.

Later days live under data/storhy/<site>/measurements/upcoming/batch-<date>/
and are never loaded at startup; --next replays them one day at a time, as if
new data had just arrived.

Options:
  --reset           Remove the container (and its data) first, then start fresh.
  --no-ingest       Only start the database; do not ingest batches.
  --batch DIR       Ingest only DIR instead of the startup batches (repeatable).
  --next            Per site, ingest only the oldest upcoming/batch-<date> whose
                    data is newer than the newest measurement in the database.
  --notify          After loading, ask the dashboard service to run the site's
                    assessment workflows (POST <url>/api/sites/<site>/runs)
                    for every site that got new rows.
  --notify-url URL  Dashboard service URL for --notify (implies --notify;
                    default: $CADS_DASHBOARD_URL or http://127.0.0.1:8080).
  -h, --help        Show this help.

Environment:
  CADS_DB_CONTAINER   Container name (default: cads-timescaledb)
  CADS_DB_IMAGE       Image (default: docker.io/timescale/timescaledb:latest-pg16)
  CADS_DB_PORT        Host port (default: 5432)
  CADS_DASHBOARD_URL  Default dashboard service URL for --notify

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
        --batch)
            if (($# < 2)) || [[ -z "$2" ]]; then
                log_error "--batch expects a directory"
                exit 1
            fi
            batch_path="$2"
            if [[ "$batch_path" != /* ]]; then
                batch_path="$PWD/$batch_path"
            fi
            if [[ ! -d "$batch_path" ]]; then
                log_error "Batch directory not found: $2"
                exit 1
            fi
            explicit_batches+=("${batch_path%/}")
            shift
            ;;
        --next)
            next_batch=1
            ;;
        --notify)
            notify=1
            ;;
        --notify-url)
            if (($# < 2)) || [[ -z "$2" ]]; then
                log_error "--notify-url expects a URL"
                exit 1
            fi
            NOTIFY_URL="$2"
            notify=1
            shift
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
    if ((${#explicit_batches[@]} > 0)); then
        for batch_dir in "${explicit_batches[@]}"; do
            batch_args+=(--batch "$batch_dir")
        done
    fi

    if ((next_batch == 1)); then
        found_upcoming=0
        for upcoming_dir in "$ROOT_DIR"/data/storhy/*/measurements/upcoming/; do
            [[ -d "$upcoming_dir" ]] || continue
            site="$(basename "$(dirname "$(dirname "${upcoming_dir%/}")")")"
            if [[ ! "$site" =~ ^[a-z][a-z0-9_]*$ ]]; then
                log_warn "Skipping upcoming batches of $site (not a valid site id)"
                continue
            fi
            found_upcoming=1
            # Newest measurement day: the day of (max(time) - 1 s), so a day
            # batch ending at the next midnight counts as that day. Empty when
            # the site has no measurements table yet.
            latest_day="$("$container_tool" exec "$DB_CONTAINER" psql -U "$DB_USER" -d "$DB_NAME" -Atc \
                "select case when to_regclass('${site}.measurements') is not null then (select to_char((max(time) - interval '1 second') at time zone 'UTC', 'YYYY-MM-DD') from ${site}.measurements) end" \
                2>/dev/null || true)"
            picked=""
            for candidate in "$upcoming_dir"batch-*/; do
                [[ -d "$candidate" ]] || continue
                candidate="${candidate%/}"
                batch_day="$(basename "$candidate")"
                batch_day="${batch_day#batch-}"
                if [[ ! "$batch_day" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
                    continue
                fi
                if [[ -z "$latest_day" || "$batch_day" > "$latest_day" ]]; then
                    picked="$candidate"
                    break
                fi
            done
            if [[ -n "$picked" ]]; then
                log_info "Next upcoming batch for $site: $(basename "$picked") (data through ${latest_day:-none})"
                batch_args+=(--batch "$picked")
            else
                log_info "No upcoming batch for $site newer than ${latest_day:-the database}"
            fi
        done
        if ((found_upcoming == 0)); then
            log_warn "No upcoming batches under data/storhy/*/measurements/upcoming/"
        fi
    elif ((${#explicit_batches[@]} == 0)); then
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
    fi

    # Pass --notify explicitly (empty = off) so a stray CADS_DASHBOARD_URL in
    # the environment does not trigger runs without --notify.
    notify_arg=""
    if ((notify == 1)); then
        notify_arg="$NOTIFY_URL"
    fi

    if ((${#batch_args[@]} > 0)); then
        log_step "Ingesting measurement batches"
        "$VENV_DIR/bin/python" "$ROOT_DIR/scripts/ingest_measurements.py" --db-url "$DB_URL" \
            --notify "$notify_arg" "${batch_args[@]}"
    elif ((next_batch == 1)); then
        log_info "Nothing new to ingest"
    else
        log_warn "No measurement batches found under data/storhy/*/measurements/"
    fi
elif ((next_batch == 1 || notify == 1 || ${#explicit_batches[@]} > 0)); then
    log_warn "--no-ingest given: ignoring --batch/--next/--notify"
fi

log_ok "STOR-HY database is up. Point the runtime at it with:"
printf '  export CADS_DB_URL=%s\n' "$DB_URL"
