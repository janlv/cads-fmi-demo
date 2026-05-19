#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
identity="${AGE_KEY_FILE:-$HOME/.config/age/key.txt}"
recipient_file="${AGE_RECIPIENT_FILE:-$HOME/.config/cads/age-recipient.txt}"
inbox="${CADS_KUBECONFIG_INBOX:-$ROOT_DIR/.local/kaizen/kubeconfig.age}"
inbox_file="${CADS_KUBECONFIG_INBOX_FILE:-$HOME/.config/cads/kubeconfig-inbox-path}"
output="$ROOT_DIR/.local/kaizen/kubeconfig"
timeout_seconds=600
wait_for_file=1
force=0
send_target="${CADS_SEND_TO:-}"

usage() {
    cat <<'EOF'
Usage: scripts/age_receive_kubeconfig.sh [options]

Prepare this machine to receive an encrypted Kaizen kubeconfig from another
machine, print the exact sender command, wait for the encrypted file, and
decrypt it into .local/kaizen/kubeconfig.

Options:
  --send-target USER@HOST  SSH target the sender should use for this machine.
                           Default: $USER@$(hostname -f), falling back to hostname.
                           Can also be set with CADS_SEND_TO.
  --inbox PATH             Where the sender writes the encrypted file.
                           Default: .local/kaizen/kubeconfig.age in this checkout
  -o, --out PATH           Decrypted kubeconfig path.
                           Default: .local/kaizen/kubeconfig in this checkout
  --timeout SECONDS        How long to wait for the encrypted file.
                           Default: 600
  --no-wait                Print the sender command and exit.
  --force                  Overwrite an existing inbox file or output kubeconfig.
  -h, --help               Show this help.

Run this on the receiving machine. Then run the printed command on the machine
that already has .local/kaizen/kubeconfig.
EOF
}

default_send_target() {
    local host=""
    host="$(hostname -f 2>/dev/null || hostname 2>/dev/null || true)"
    if [[ -z "$host" ]]; then
        return 1
    fi
    printf '%s@%s\n' "${USER:-$(id -un)}" "$host"
}

ensure_identity() {
    if ! command -v age-keygen >/dev/null 2>&1; then
        cat >&2 <<'EOF'
error: age-keygen is not installed.

Install age first:
  Ubuntu/Debian: sudo apt install age
  macOS:         brew install age
EOF
        exit 1
    fi

    mkdir -p "$(dirname "$identity")" "$(dirname "$recipient_file")" "$(dirname "$inbox_file")"
    if [[ ! -f "$identity" ]]; then
        umask 077
        age-keygen -o "$identity" >/dev/null 2>&1
    fi
    chmod 600 "$identity"
    age-keygen -y "$identity" >"$recipient_file"
    chmod 600 "$recipient_file"
    printf '%s\n' "$inbox" >"$inbox_file"
    chmod 600 "$inbox_file"
}

shell_quote() {
    printf '%q' "$1"
}

print_sender_command() {
    cat <<'EOF'
Run this on the machine that already has the Kaizen kubeconfig:

EOF
    printf './scripts/age_send_kubeconfig.sh %s' "$(shell_quote "$send_target")"
    printf '\n'
}

wait_for_inbox() {
    local deadline=$((SECONDS + timeout_seconds))

    echo
    echo "Waiting for encrypted kubeconfig at: $inbox"
    echo "Press Ctrl-C to stop waiting."

    while [[ ! -f "$inbox" ]]; do
        if ((SECONDS >= deadline)); then
            echo "error: timed out waiting for $inbox" >&2
            exit 1
        fi
        sleep 2
    done
}

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --send-target)
            shift
            send_target="${1:-}"
            if [[ -z "$send_target" ]]; then
                echo "error: --send-target expects USER@HOST" >&2
                exit 1
            fi
            ;;
        --send-target=*)
            send_target="${1#*=}"
            if [[ -z "$send_target" ]]; then
                echo "error: --send-target expects USER@HOST" >&2
                exit 1
            fi
            ;;
        --inbox)
            shift
            inbox="${1:-}"
            if [[ -z "$inbox" ]]; then
                echo "error: --inbox expects a path" >&2
                exit 1
            fi
            ;;
        --inbox=*)
            inbox="${1#*=}"
            if [[ -z "$inbox" ]]; then
                echo "error: --inbox expects a path" >&2
                exit 1
            fi
            ;;
        -o|--out)
            shift
            output="${1:-}"
            if [[ -z "$output" ]]; then
                echo "error: --out expects a path" >&2
                exit 1
            fi
            ;;
        --out=*)
            output="${1#*=}"
            if [[ -z "$output" ]]; then
                echo "error: --out expects a path" >&2
                exit 1
            fi
            ;;
        --timeout)
            shift
            timeout_seconds="${1:-}"
            ;;
        --timeout=*)
            timeout_seconds="${1#*=}"
            ;;
        --no-wait)
            wait_for_file=0
            ;;
        --force)
            force=1
            ;;
        *)
            echo "error: unknown argument: $1" >&2
            usage >&2
            exit 1
            ;;
    esac
    shift || true
done

if [[ ! "$timeout_seconds" =~ ^[0-9]+$ || "$timeout_seconds" -eq 0 ]]; then
    echo "error: --timeout expects a positive integer" >&2
    exit 1
fi

if [[ -z "$send_target" ]]; then
    send_target="$(default_send_target || true)"
fi
if [[ -z "$send_target" || "$send_target" != *@* || "$send_target" == *[[:space:]]* ]]; then
    echo "error: unable to derive a usable sender SSH target; pass --send-target USER@HOST" >&2
    exit 1
fi
if [[ "$inbox" == *[[:space:]]* ]]; then
    echo "error: --inbox must not contain whitespace" >&2
    exit 1
fi
case "$inbox" in
    /*) ;;
    "~/"*) inbox="$HOME/${inbox#~/}" ;;
    *) inbox="$ROOT_DIR/$inbox" ;;
esac

ensure_identity
print_sender_command

if ((wait_for_file == 0)); then
    exit 0
fi

if [[ -e "$inbox" ]]; then
    if ((force == 0)); then
        echo "error: inbox already exists: $inbox" >&2
        echo "hint: remove it or pass --force before waiting for a new transfer" >&2
        exit 1
    fi
    rm -f "$inbox"
fi

wait_for_inbox

decrypt_args=("$inbox" --out "$output")
if ((force)); then
    decrypt_args+=(--force)
fi
"$ROOT_DIR/scripts/age_decrypt_kubeconfig.sh" "${decrypt_args[@]}"
