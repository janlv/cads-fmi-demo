#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target=""
input="${KUBECONFIG:-$ROOT_DIR/.local/kaizen/kubeconfig}"
recipient_path="${CADS_REMOTE_RECIPIENT_PATH:-~/.config/cads/age-recipient.txt}"
send_path="${CADS_SEND_KUBECONFIG_PATH:-~/cads-kubeconfig.age}"

usage() {
    cat <<'EOF'
Usage: scripts/age_send_kubeconfig.sh USER@HOST [options]

Send this machine's Kaizen kubeconfig to a receiver that is running
scripts/age_receive_kubeconfig.sh.

Standard locations:
  receiver public key:  ~/.config/cads/age-recipient.txt
  receiver inbox:       ~/cads-kubeconfig.age
  sender kubeconfig:    $KUBECONFIG or .local/kaizen/kubeconfig in this checkout

Options:
  -i, --input PATH           Kubeconfig to encrypt.
  --recipient-path PATH      Receiver-side public age key path.
                             Default: ~/.config/cads/age-recipient.txt
  --send-path PATH           Receiver-side encrypted output path.
                             Default: ~/cads-kubeconfig.age
  -h, --help                 Show this help.
EOF
}

fetch_recipient() {
    local ssh_target="$1"
    local path="$2"

    ssh -o BatchMode=no "$ssh_target" sh -s -- "$path" <<'REMOTE_SCRIPT'
set -eu
path="$1"
case "$path" in
    "~/"*) path="$HOME/${path#~/}" ;;
esac
if [ ! -f "$path" ]; then
    echo "error: receiver public age key not found: $path" >&2
    echo "hint: run scripts/age_receive_kubeconfig.sh on the receiver first" >&2
    exit 1
fi
cat "$path"
REMOTE_SCRIPT
}

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        -i|--input)
            shift
            input="${1:-}"
            if [[ -z "$input" ]]; then
                echo "error: --input expects a path" >&2
                exit 1
            fi
            ;;
        --input=*)
            input="${1#*=}"
            if [[ -z "$input" ]]; then
                echo "error: --input expects a path" >&2
                exit 1
            fi
            ;;
        --recipient-path)
            shift
            recipient_path="${1:-}"
            if [[ -z "$recipient_path" ]]; then
                echo "error: --recipient-path expects a path" >&2
                exit 1
            fi
            ;;
        --recipient-path=*)
            recipient_path="${1#*=}"
            if [[ -z "$recipient_path" ]]; then
                echo "error: --recipient-path expects a path" >&2
                exit 1
            fi
            ;;
        --send-path)
            shift
            send_path="${1:-}"
            if [[ -z "$send_path" ]]; then
                echo "error: --send-path expects a path" >&2
                exit 1
            fi
            ;;
        --send-path=*)
            send_path="${1#*=}"
            if [[ -z "$send_path" ]]; then
                echo "error: --send-path expects a path" >&2
                exit 1
            fi
            ;;
        -*)
            echo "error: unknown argument: $1" >&2
            usage >&2
            exit 1
            ;;
        *)
            if [[ -n "$target" ]]; then
                echo "error: only one USER@HOST target is supported" >&2
                usage >&2
                exit 1
            fi
            target="$1"
            ;;
    esac
    shift || true
done

if [[ -z "$target" || "$target" != *@* || "$target" == *[[:space:]]* ]]; then
    echo "error: provide receiver target as USER@HOST" >&2
    usage >&2
    exit 1
fi

if [[ ! -f "$input" ]]; then
    echo "error: sender kubeconfig not found: $input" >&2
    exit 1
fi

recipient="$(fetch_recipient "$target" "$recipient_path")"
if [[ -z "$recipient" || "$recipient" != age1* || "$recipient" == *[[:space:]]* ]]; then
    echo "error: invalid receiver public age key from ${target}:${recipient_path}" >&2
    exit 1
fi

"$ROOT_DIR/scripts/age_encrypt_kubeconfig.sh" \
    --recipient "$recipient" \
    --input "$input" \
    --send-to "$target" \
    --send-path "$send_path"
