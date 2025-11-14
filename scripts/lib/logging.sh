#!/usr/bin/env bash
# Shared colorized logging helpers for repo scripts.

if [[ "${CADS_LOGGING_SH_LOADED:-}" != "$BASHPID" ]]; then
    CADS_LOG_TAIL_LINES=6
    if [[ -t 1 ]]; then
        CADS_COLOR_STEP=$'\033[1;34m'
        CADS_COLOR_INFO=$'\033[1;36m'
        CADS_COLOR_WARN=$'\033[1;33m'
        CADS_COLOR_OK=$'\033[1;32m'
        CADS_COLOR_ERROR=$'\033[1;31m'
        CADS_COLOR_DIM=$'\033[90m'
        CADS_COLOR_RESET=$'\033[0m'
    else
        CADS_COLOR_STEP=""
        CADS_COLOR_INFO=""
        CADS_COLOR_WARN=""
        CADS_COLOR_OK=""
        CADS_COLOR_ERROR=""
        CADS_COLOR_DIM=""
        CADS_COLOR_RESET=""
    fi

    CADS_LOG_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    CADS_ROLLING_TAIL_HELPER="$CADS_LOG_LIB_DIR/rolling_tail.py"
    CADS_LOG_PREFIX="   "
    CADS_ENABLE_ROLLING_TAIL="${CADS_ENABLE_ROLLING_TAIL:-0}"

    _cads_default_tail_color() {
        local color_count
        if command -v tput >/dev/null 2>&1; then
            color_count="$(tput colors 2>/dev/null || true)"
        fi
        if [[ -n "$color_count" && "$color_count" -ge 16 ]]; then
            printf '%s' $'\033[38;5;244m'
        else
            printf '%s' $'\033[37m'
        fi
    }

    if [[ -z "${CADS_LOG_TAIL_COLOR:-}" && -t 1 ]]; then
        CADS_LOG_TAIL_COLOR="$(_cads_default_tail_color)"
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

    cads_set_log_tail_lines() {
        local value="${1:-}"
        if [[ -z "$value" ]]; then
            log_error "--max-lines expects a non-negative integer"
            return 1
        fi
        if ! [[ "$value" =~ ^[0-9]+$ ]]; then
            log_error "--max-lines expects a non-negative integer (got '$value')"
            return 1
        fi
        CADS_LOG_TAIL_LINES="$value"
        return 0
    }

    _cads_run_with_tail() {
        local status
        local had_errexit=0
        if [[ $- == *e* ]]; then
            had_errexit=1
            set +e
        else
            set +e
        fi
        local helper_can_stream=0
        if [[ "$CADS_ENABLE_ROLLING_TAIL" == "1" && -t 1 && ${CADS_LOG_TAIL_LINES} -gt 0 && -n "${CADS_ROLLING_TAIL_HELPER:-}" && -x "$CADS_ROLLING_TAIL_HELPER" ]]; then
            if command -v python3 >/dev/null 2>&1; then
                helper_can_stream=1
            fi
        fi
        local -a cmd=( "$@" )
        if [[ $helper_can_stream -eq 1 ]]; then
            CADS_LOG_PREFIX="${CADS_LOG_PREFIX:-   }" \
                CADS_TAIL_COLOR="${CADS_LOG_TAIL_COLOR:-}" \
                CADS_COLOR_RESET="$CADS_COLOR_RESET" \
                python3 "$CADS_ROLLING_TAIL_HELPER" "$CADS_LOG_TAIL_LINES" "${cmd[@]}"
            status=$?
        else
            if command -v stdbuf >/dev/null 2>&1 && [[ -z "${CADS_DISABLE_STDBUF:-}" ]]; then
                cmd=(stdbuf -oL -eL "${cmd[@]}")
            fi
            "${cmd[@]}" 2>&1 | sed $'s/^/   /'
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

    CADS_LOGGING_SH_LOADED="$BASHPID"
fi
