#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target=""
input="${KUBECONFIG:-$ROOT_DIR/.local/kaizen/kubeconfig}"
recipient_path="${CADS_REMOTE_RECIPIENT_PATH:-~/.config/cads/age-recipient.txt}"
receiver_inbox_path="${CADS_REMOTE_INBOX_PATH:-~/.config/cads/kubeconfig-inbox-path}"
send_path="${CADS_SEND_KUBECONFIG_PATH:-}"
explicit_send_path=0

usage() {
    cat <<'EOF'
Usage: scripts/age_send_kubeconfig.sh USER@HOST [options]

Send this machine's Kaizen kubeconfig to a receiver that is running
scripts/age_receive_kubeconfig.sh.

Standard locations:
  receiver public key:  ~/.config/cads/age-recipient.txt
  receiver inbox:       discovered from ~/.config/cads/kubeconfig-inbox-path
                         and defaults to the receiver checkout's
                         .local/kaizen/kubeconfig.age
  sender kubeconfig:    $KUBECONFIG or .local/kaizen/kubeconfig in this checkout

Options:
  -i, --input PATH           Kubeconfig to encrypt.
  --recipient-path PATH      Receiver-side public age key path.
                             Default: ~/.config/cads/age-recipient.txt
  --receiver-inbox-path PATH Receiver-side file containing the repo-local inbox.
                             Default: ~/.config/cads/kubeconfig-inbox-path
  --send-path PATH           Receiver-side encrypted output path.
                             Default: discovered from receiver
  -h, --help                 Show this help.
EOF
}

fetch_receiver_config() {
    local ssh_target="$1"
    local recipient_file_path="$2"
    local inbox_file_path="$3"

    ssh -o BatchMode=no "$ssh_target" sh -s -- "$recipient_file_path" "$inbox_file_path" <<'REMOTE_SCRIPT'
set -eu
recipient_path="$1"
inbox_path="$2"
case "$recipient_path" in
    "~/"*) recipient_path="$HOME/${recipient_path#~/}" ;;
esac
case "$inbox_path" in
    "~/"*) inbox_path="$HOME/${inbox_path#~/}" ;;
esac
if [ ! -f "$recipient_path" ]; then
    echo "error: receiver public age key not found: $recipient_path" >&2
    echo "hint: run scripts/age_receive_kubeconfig.sh on the receiver first" >&2
    exit 1
fi
sed -n '1p' "$recipient_path"
if [ -f "$inbox_path" ]; then
    sed -n '1p' "$inbox_path"
fi
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
        --receiver-inbox-path)
            shift
            receiver_inbox_path="${1:-}"
            if [[ -z "$receiver_inbox_path" ]]; then
                echo "error: --receiver-inbox-path expects a path" >&2
                exit 1
            fi
            ;;
        --receiver-inbox-path=*)
            receiver_inbox_path="${1#*=}"
            if [[ -z "$receiver_inbox_path" ]]; then
                echo "error: --receiver-inbox-path expects a path" >&2
                exit 1
            fi
            ;;
        --send-path)
            shift
            send_path="${1:-}"
            explicit_send_path=1
            if [[ -z "$send_path" ]]; then
                echo "error: --send-path expects a path" >&2
                exit 1
            fi
            ;;
        --send-path=*)
            send_path="${1#*=}"
            explicit_send_path=1
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

receiver_config="$(fetch_receiver_config "$target" "$recipient_path" "$receiver_inbox_path")"
recipient="$(printf '%s\n' "$receiver_config" | sed -n '1p')"
discovered_send_path="$(printf '%s\n' "$receiver_config" | sed -n '2p')"
if [[ -z "$recipient" || "$recipient" != age1* || "$recipient" == *[[:space:]]* ]]; then
    echo "error: invalid receiver public age key from ${target}:${recipient_path}" >&2
    exit 1
fi
if [[ -z "$send_path" && -n "$discovered_send_path" ]]; then
    send_path="$discovered_send_path"
fi
if [[ -z "$send_path" ]]; then
    echo "error: receiver inbox path was not advertised by ${target}" >&2
    echo "hint: rerun scripts/age_receive_kubeconfig.sh on the receiver, or pass --send-path" >&2
    exit 1
fi
if ((explicit_send_path == 0)); then
    echo "Using receiver inbox: $send_path"
fi

"$ROOT_DIR/scripts/age_encrypt_kubeconfig.sh" \
    --recipient "$recipient" \
    --input "$input" \
    --send-to "$target" \
    --send-path "$send_path"
