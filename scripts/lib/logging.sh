#!/usr/bin/env bash
# Shared colorized logging helpers for repo scripts.

_cads_logging_shell_pid="${BASHPID:-$$}"
if [[ "${CADS_LOGGING_SH_LOADED:-}" != "$_cads_logging_shell_pid" ]]; then
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

    log_substep() {
        printf '   %b-> %s%b\n' "$CADS_COLOR_STEP" "$1" "$CADS_COLOR_RESET"
    }

    log_info() {
        printf '%b[info]%b %s\n' "$CADS_COLOR_INFO" "$CADS_COLOR_RESET" "$1"
    }

    log_subinfo() {
        printf '   %b[info]%b %s\n' "$CADS_COLOR_INFO" "$CADS_COLOR_RESET" "$1"
    }

    log_warn() {
        printf '%b[warn]%b %s\n' "$CADS_COLOR_WARN" "$CADS_COLOR_RESET" "$1" >&2
    }

    log_subwarn() {
        printf '   %b[warn]%b %s\n' "$CADS_COLOR_WARN" "$CADS_COLOR_RESET" "$1" >&2
    }

    log_ok() {
        printf '%b[ok]%b %s\n' "$CADS_COLOR_OK" "$CADS_COLOR_RESET" "$1"
    }

    log_subok() {
        printf '   %b[ok]%b %s\n' "$CADS_COLOR_OK" "$CADS_COLOR_RESET" "$1"
    }

    log_error() {
        printf '%b[error]%b %s\n' "$CADS_COLOR_ERROR" "$CADS_COLOR_RESET" "$1" >&2
    }

    log_suberror() {
        printf '   %b[error]%b %s\n' "$CADS_COLOR_ERROR" "$CADS_COLOR_RESET" "$1" >&2
    }

    _cads_prefixed_tail() {
        local logfile="$1"
        local max_lines="$2"
        local line_count="0"

        if [[ ! -f "$logfile" ]]; then
            return 0
        fi

        line_count="$(wc -l <"$logfile" | tr -d '[:space:]')"
        if [[ -z "$line_count" || "$line_count" == "0" ]]; then
            return 0
        fi

        if (( line_count > max_lines )); then
            printf '   %b... %s lines captured; showing last %s ...%b\n' \
                "$CADS_COLOR_DIM" "$line_count" "$max_lines" "$CADS_COLOR_RESET"
        fi

        tail -n "$max_lines" "$logfile" | sed $'s/^/   /'
    }

    _cads_trim_display_line() {
        local line="$1"
        local max_width="${2:-0}"
        if [[ ! "$max_width" =~ ^[0-9]+$ || "$max_width" -le 0 ]]; then
            printf '%s\n' "$line"
            return 0
        fi
        if ((${#line} > max_width)); then
            printf '%s\n' "${line:0:max_width-1}…"
            return 0
        fi
        printf '%s\n' "$line"
    }

    _cads_live_tail_block() {
        local logfile="$1"
        local max_lines="$2"
        local cmd_pid="$3"
        local state_label="$4"
        local columns=0
        local content_width=0
        local -a lines=()
        local line_count=0
        local status_line=""
        local trimmed=""
        local rendered_body_lines=0
        local current_body_lines=0
        local extra_lines=0
        local i

        if command -v tput >/dev/null 2>&1; then
            columns="$(tput cols 2>/dev/null || printf '0')"
        fi
        if [[ "$columns" =~ ^[0-9]+$ && "$columns" -gt 4 ]]; then
            content_width=$((columns - 4))
        fi

        printf '   %b[%s]%b showing the last %s lines live; set CADS_LOG_FULL=1 for full output\n' \
            "$CADS_COLOR_DIM" "$state_label" "$CADS_COLOR_RESET" "$max_lines"

        while true; do
            lines=()
            while IFS= read -r line; do
                lines+=("$line")
            done < <(tail -n "$max_lines" "$logfile" 2>/dev/null || true)
            line_count="$(wc -l <"$logfile" 2>/dev/null | tr -d '[:space:]')"
            if [[ -z "$line_count" ]]; then
                line_count=0
            fi
            current_body_lines=${#lines[@]}

            printf '\033[%dA' $((rendered_body_lines + 1))
            if kill -0 "$cmd_pid" 2>/dev/null; then
                status_line="   ${CADS_COLOR_DIM}[live]${CADS_COLOR_RESET} showing the last ${max_lines} lines"
            elif (( line_count > max_lines )); then
                status_line="   ${CADS_COLOR_DIM}[done]${CADS_COLOR_RESET} ${line_count} lines captured; showing last ${max_lines}"
            elif (( line_count == 0 )); then
                status_line=""
            else
                status_line="   ${CADS_COLOR_DIM}[done]${CADS_COLOR_RESET} ${line_count} lines captured"
            fi
            if [[ -n "$status_line" ]]; then
                printf '\r\033[2K%s\n' "$status_line"
            else
                printf '\r\033[2K'
            fi

            for ((i=0; i<current_body_lines; i++)); do
                trimmed="$(_cads_trim_display_line "${lines[$i]}" "$content_width")"
                printf '\r\033[2K   %s\n' "$trimmed"
            done

            extra_lines=$((rendered_body_lines - current_body_lines))
            for ((i=0; i<extra_lines; i++)); do
                printf '\r\033[2K   \n'
            done
            if (( extra_lines > 0 )); then
                printf '\033[%dA' "$extra_lines"
            fi
            rendered_body_lines=$current_body_lines

            if ! kill -0 "$cmd_pid" 2>/dev/null; then
                break
            fi
            sleep 0.2
        done
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

        if [[ -t 1 && -z "${CADS_LOG_FULL:-}" ]]; then
            local max_lines="${CADS_LOG_TAIL_LINES:-20}"
            if [[ ! "$max_lines" =~ ^[0-9]+$ || "$max_lines" -le 0 ]]; then
                max_lines=20
            fi

            local logfile=""
            logfile="$(mktemp "${TMPDIR:-/tmp}/cads-log.XXXXXX")"
            if [[ "${CADS_LOG_LIVE_TAIL:-1}" == "0" ]]; then
                printf '   %b[capturing]%b showing the last %s lines when complete; set CADS_LOG_FULL=1 for full output\n' \
                    "$CADS_COLOR_DIM" "$CADS_COLOR_RESET" "$max_lines"
                "${cmd[@]}" >"$logfile" 2>&1
                status=$?
                _cads_prefixed_tail "$logfile" "$max_lines"
            else
                "${cmd[@]}" >"$logfile" 2>&1 &
                local cmd_pid=$!
                _cads_live_tail_block "$logfile" "$max_lines" "$cmd_pid" "capturing"
                wait "$cmd_pid"
                status=$?
            fi
            rm -f "$logfile"
        else
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

    CADS_LOGGING_SH_LOADED="$_cads_logging_shell_pid"
fi
