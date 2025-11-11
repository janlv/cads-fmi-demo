#!/usr/bin/env bash
# Shared colorized logging helpers for repo scripts.

if [[ -z "${CADS_LOGGING_SH_LOADED:-}" ]]; then
    CADS_LOG_TAIL_LINES="${CADS_LOG_TAIL_LINES:-6}"
    if [[ -t 1 ]]; then
        CADS_COLOR_STEP="\033[1;34m"
        CADS_COLOR_INFO="\033[1;36m"
        CADS_COLOR_WARN="\033[1;33m"
        CADS_COLOR_OK="\033[1;32m"
        CADS_COLOR_ERROR="\033[1;31m"
        CADS_COLOR_DIM="\033[90m"
        CADS_COLOR_RESET="\033[0m"
    else
        CADS_COLOR_STEP=""
        CADS_COLOR_INFO=""
        CADS_COLOR_WARN=""
        CADS_COLOR_OK=""
        CADS_COLOR_ERROR=""
        CADS_COLOR_DIM=""
        CADS_COLOR_RESET=""
    fi

    log_step() {
        printf '%b==> %s%b\n' "$CADS_COLOR_STEP" "$1" "$CADS_COLOR_RESET"
    }

    log_info() {
        printf '%b[info]%b %s\n' "$CADS_COLOR_INFO" "$CADS_COLOR_RESET" "$1"
    }

    log_warn() {
        printf '%b[warn]%b %s\n' "$CADS_COLOR_WARN" "$CADS_COLOR_RESET" "$1" >&2
    }

    log_ok() {
        printf '%b[ok]%b %s\n' "$CADS_COLOR_OK" "$CADS_COLOR_RESET" "$1"
    }

    log_error() {
        printf '%b[error]%b %s\n' "$CADS_COLOR_ERROR" "$CADS_COLOR_RESET" "$1" >&2
    }

    _cads_log_tail_consumer() {
        local max_lines="${1:-$CADS_LOG_TAIL_LINES}"
        local prefix="   "
        local -a buffer=()
        local printed=0
        local line trimmed
        if [[ -t 1 && ${CADS_LOG_TAIL_LINES} -gt 0 ]]; then
            while IFS= read -r line; do
                trimmed="${line%$'\r'}"
                buffer+=("$trimmed")
                if ((${#buffer[@]} > max_lines)); then
                    buffer=("${buffer[@]:1}")
                fi
                if ((printed > 0)); then
                    for ((i=0; i<printed; i++)); do printf '\033[F\033[K'; done
                fi
                printed=0
                for entry in "${buffer[@]}"; do
                    printf '%s%b%s%b\n' "$prefix" "$CADS_COLOR_DIM" "$entry" "$CADS_COLOR_RESET"
                    ((printed++))
                done
            done
        else
            while IFS= read -r line; do
                trimmed="${line%$'\r'}"
                printf '%s%b%s%b\n' "$prefix" "$CADS_COLOR_DIM" "$trimmed" "$CADS_COLOR_RESET"
            done
        fi
    }

    _cads_run_with_tail() {
        local status
        local had_errexit=0
        [[ $- == *e* ]] && had_errexit=1
        if [[ -t 1 && ${CADS_LOG_TAIL_LINES} -gt 0 ]]; then
            local status_file
            status_file="$(mktemp)"
            (
                set +e
                "$@"
                printf '%s' "$?" >"$status_file"
            ) > >(_cads_log_tail_consumer "$CADS_LOG_TAIL_LINES") 2>&1
            status="$(<"$status_file")"
            rm -f "$status_file"
        else
            set +e
            "$@" 2>&1 | sed $'s/^/   /'
            status=${PIPESTATUS[0]}
        fi
        if [[ $had_errexit -eq 1 ]]; then
            set -e
        else
            set +e
        fi
        return "$status"
    }

    log_stream_cmd() {
        local description="$1"
        shift
        log_step "$description"
        _cads_run_with_tail "$@"
        local status=$?
        if ((status != 0)); then
            log_error "$description failed (exit code $status)"
        fi
        return "$status"
    }

    run_with_log_tail() {
        _cads_run_with_tail "$@"
    }

    export CADS_LOGGING_SH_LOADED=1
fi
