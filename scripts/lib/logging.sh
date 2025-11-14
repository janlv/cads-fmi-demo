#!/usr/bin/env bash
# Shared colorized logging helpers for repo scripts.

if [[ "${CADS_LOGGING_SH_LOADED:-}" != "$BASHPID" ]]; then
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

    _cads_run_with_prefix() {
        local status
        local had_errexit=0
        if [[ $- == *e* ]]; then
            had_errexit=1
            set +e
        else
            set +e
        fi
        local -a cmd=( "$@" )
        if command -v stdbuf >/dev/null 2>&1 && [[ -z "${CADS_DISABLE_STDBUF:-}" ]]; then
            cmd=(stdbuf -oL -eL "${cmd[@]}")
        fi
        "${cmd[@]}" 2>&1 | sed $'s/^/   /'
        status=${PIPESTATUS[0]}
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
        _cads_run_with_prefix "$@"
        local status=$?
        if ((status != 0)); then
            log_error "$description failed (exit code $status)"
        fi
        return "$status"
    }

    run_with_logged_output() {
        _cads_run_with_prefix "$@"
    }

    CADS_LOGGING_SH_LOADED="$BASHPID"
fi
