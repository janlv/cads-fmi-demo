#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
FMU_DIR="$ROOT_DIR/fmu/models"
VENV_DIR="$SCRIPT_DIR/.venv"
REQ_FILE="$SCRIPT_DIR/requirements.txt"
STORHY_REPLICA_DIR="$SCRIPT_DIR/storhy_replicas"
STORHY_REPLICA_COMMON="$STORHY_REPLICA_DIR/storhy_replica_common.py"

usage() {
    cat <<'EOF'
Usage: create_fmu/build_python_fmus.sh [--venv <path>] [--python <python3>]

Builds the demo Python FMUs (Producer/Consumer/AE stats/STOR-HY replicas) and
drops them into fmu/models/.

Options:
  --venv        Path to the pythonfmu virtualenv (default: create_fmu/.venv)
  --python      Python interpreter to use when creating the venv (default: python3)
EOF
}

PYTHON_BIN="python3"

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --venv)
            shift
            VENV_DIR="${1:-}"
            if [[ -z "$VENV_DIR" ]]; then
                echo "[error] --venv expects a path" >&2
                exit 1
            fi
            shift || true
            ;;
        --python)
            shift
            PYTHON_BIN="${1:-}"
            if [[ -z "$PYTHON_BIN" ]]; then
                echo "[error] --python expects an interpreter path" >&2
                exit 1
            fi
            shift || true
            ;;
        *)
            echo "[error] Unknown argument: $1" >&2
            usage
            exit 1
            ;;
    esac
done

if [[ ! -d "$FMU_DIR" ]]; then
    log_error "FMU directory not found: $FMU_DIR"
    exit 1
fi

if [[ ! -d "$VENV_DIR" ]]; then
    log_step "Creating virtualenv at $VENV_DIR"
    "$PYTHON_BIN" -m venv "$VENV_DIR"
fi

source "$VENV_DIR/bin/activate"
log_step "Upgrading pip in virtualenv"
pip install --upgrade pip >/dev/null
log_step "Installing pythonfmu requirements"
pip install -r "$REQ_FILE"

log_step "Patching pythonfmu exporter (ensures libpython linkage)"
python "$SCRIPT_DIR/patch_pythonfmu_export.py"

log_step "Building Producer/Consumer/AEEventStats FMUs via pythonfmu"
python -m pythonfmu build -f "$SCRIPT_DIR/producer_fmu.py" -d "$FMU_DIR"
python -m pythonfmu build -f "$SCRIPT_DIR/consumer_fmu.py" -d "$FMU_DIR"
python -m pythonfmu build -f "$SCRIPT_DIR/ae_event_stats_fmu.py" -d "$FMU_DIR"

log_step "Building STOR-HY replica FMUs via pythonfmu"
for replica in "$STORHY_REPLICA_DIR"/*_fmu.py; do
    python -m pythonfmu build -f "$replica" -d "$FMU_DIR" "$STORHY_REPLICA_COMMON"
done

log_ok "FMUs built under $FMU_DIR"
