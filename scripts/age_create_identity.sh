#!/usr/bin/env bash
set -euo pipefail

key_file="${AGE_KEY_FILE:-$HOME/.config/age/key.txt}"

usage() {
    cat <<'EOF'
Usage: scripts/age_create_identity.sh [--key-file path]

Creates an age identity for receiving encrypted credentials and prints the
public recipient key to share with the sender.

Default key file:
  ~/.config/age/key.txt

The private key file must not be shared.
EOF
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

echo
echo "Private key file: $key_file"
echo "Share this public recipient key with the person encrypting the credentials:"
age-keygen -y "$key_file"
