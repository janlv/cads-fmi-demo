#!/usr/bin/env bash
set -euo pipefail

key_file="${AGE_KEY_FILE:-$HOME/.config/age/key.txt}"
copy_to_clipboard=0
mailto=""
ssh_target=""

usage() {
    cat <<'EOF'
Usage: scripts/age_create_identity.sh [--key-file path] [--copy] [--mailto address] [--send-to user@host]

Creates an age identity for receiving encrypted credentials and prints the
public recipient key to share with the sender.

Options:
  --key-file PATH    age private key file.
                     Default: $AGE_KEY_FILE or ~/.config/age/key.txt
  --copy             Copy the public recipient key to the clipboard when a
                     supported clipboard helper is available.
  --mailto ADDRESS   Open a prefilled email draft containing the public
                     recipient key. The email is not sent automatically.
  --send-to USER@HOST
                     Send the public recipient key to a remote SSH account.
                     The remote SSH password is requested by ssh when needed.

Default key file:
  ~/.config/age/key.txt

The private key file must not be shared.
EOF
}

copy_public_key() {
    local public_key="$1"

    if command -v pbcopy >/dev/null 2>&1; then
        printf '%s' "$public_key" | pbcopy
    elif command -v wl-copy >/dev/null 2>&1; then
        printf '%s' "$public_key" | wl-copy
    elif command -v xclip >/dev/null 2>&1; then
        printf '%s' "$public_key" | xclip -selection clipboard
    elif command -v xsel >/dev/null 2>&1; then
        printf '%s' "$public_key" | xsel --clipboard --input
    else
        echo "warning: no supported clipboard helper found; copy the printed key manually" >&2
        return 1
    fi

    echo "Public recipient key copied to clipboard."
}

url_encode() {
    local input="$1"

    if command -v python3 >/dev/null 2>&1; then
        python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$input"
        return
    fi

    local output=""
    local char=""
    local hex=""
    local i=0
    for ((i = 0; i < ${#input}; i++)); do
        char="${input:i:1}"
        case "$char" in
            [a-zA-Z0-9.~_-])
                output+="$char"
                ;;
            " ")
                output+="%20"
                ;;
            $'\n')
                output+="%0A"
                ;;
            *)
                printf -v hex '%%%02X' "'$char"
                output+="$hex"
                ;;
        esac
    done

    printf '%s' "$output"
}

open_mailto_draft() {
    local recipient="$1"
    local public_key="$2"
    local subject=""
    local body=""
    local uri=""

    subject="$(url_encode "CADS dashboard age public key")"
    body="$(url_encode "Hello,

Please encrypt the Kaizen kubeconfig for this age recipient:

$public_key

Do not send the plaintext kubeconfig by email.
")"
    uri="mailto:${recipient}?subject=${subject}&body=${body}"

    if command -v xdg-open >/dev/null 2>&1; then
        xdg-open "$uri" >/dev/null 2>&1 &
    elif command -v open >/dev/null 2>&1; then
        open "$uri" >/dev/null 2>&1
    elif command -v wslview >/dev/null 2>&1; then
        wslview "$uri" >/dev/null 2>&1
    else
        echo "warning: no supported mail opener found; send the printed key manually" >&2
        return 1
    fi

    echo "Opened email draft for $recipient. Review and send it from your mail client."
}

send_public_key_ssh() {
    local public_key="$1"
    local target="$2"

    if ! command -v ssh >/dev/null 2>&1; then
        echo "error: ssh is not installed or not on PATH" >&2
        return 1
    fi

    if [[ "$target" != *@* || "$target" == *[[:space:]]* ]]; then
        echo "error: --send-to expects USER@HOST" >&2
        return 1
    fi

    printf '%s\n' "$public_key" | ssh -o BatchMode=no "$target" 'set -e
dir="$HOME/.config/cads"
file="$dir/age-recipient.txt"
mkdir -p "$dir"
umask 077
cat > "$file"
chmod 600 "$file"
printf "Saved public recipient key to %s\n" "$file"
'

    echo "Sent public recipient key to $target."
}

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --key-file)
            shift
            key_file="${1:-}"
            if [[ -z "$key_file" ]]; then
                echo "error: --key-file expects a path" >&2
                exit 1
            fi
            ;;
        --key-file=*)
            key_file="${1#*=}"
            if [[ -z "$key_file" ]]; then
                echo "error: --key-file expects a path" >&2
                exit 1
            fi
            ;;
        --copy)
            copy_to_clipboard=1
            ;;
        --mailto)
            shift
            mailto="${1:-}"
            if [[ -z "$mailto" ]]; then
                echo "error: --mailto expects an email address" >&2
                exit 1
            fi
            ;;
        --mailto=*)
            mailto="${1#*=}"
            if [[ -z "$mailto" ]]; then
                echo "error: --mailto expects an email address" >&2
                exit 1
            fi
            ;;
        --send-to)
            shift
            ssh_target="${1:-}"
            if [[ -z "$ssh_target" ]]; then
                echo "error: --send-to expects USER@HOST" >&2
                exit 1
            fi
            ;;
        --send-to=*)
            ssh_target="${1#*=}"
            if [[ -z "$ssh_target" ]]; then
                echo "error: --send-to expects USER@HOST" >&2
                exit 1
            fi
            ;;
        *)
            echo "error: unknown argument: $1" >&2
            usage >&2
            exit 1
            ;;
    esac
    shift
done

if ! command -v age-keygen >/dev/null 2>&1; then
    cat >&2 <<'EOF'
error: age-keygen is not installed.

Install age first:
  Ubuntu/Debian: sudo apt install age
  macOS:         brew install age
EOF
    exit 1
fi

mkdir -p "$(dirname "$key_file")"

if [[ ! -f "$key_file" ]]; then
    umask 077
    age-keygen -o "$key_file"
    chmod 600 "$key_file"
else
    chmod 600 "$key_file"
fi

public_key="$(age-keygen -y "$key_file")"

echo
echo "Private key file: $key_file"
echo "Share this public recipient key with the person encrypting the credentials:"
echo "$public_key"

if [[ "$copy_to_clipboard" -eq 1 ]]; then
    copy_public_key "$public_key" || true
fi

if [[ -n "$mailto" ]]; then
    open_mailto_draft "$mailto" "$public_key" || true
fi

if [[ -n "$ssh_target" ]]; then
    send_public_key_ssh "$public_key" "$ssh_target"
fi
