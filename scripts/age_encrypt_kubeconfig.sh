#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
input="${KUBECONFIG:-$ROOT_DIR/.local/kaizen/kubeconfig}"
output=""
recipients=()
recipient_files=()

usage() {
    cat <<'EOF'
Usage: scripts/age_encrypt_kubeconfig.sh --recipient age1... [options]

Encrypts a Kaizen kubeconfig for one or more age recipients.

Options:
  -r, --recipient KEY       age public recipient key. May be repeated.
  -R, --recipient-file PATH File containing age recipients. May be repeated.
  -i, --input PATH          Kubeconfig to encrypt.
                            Default: $KUBECONFIG or .local/kaizen/kubeconfig
                            in this checkout
  -o, --out PATH            Encrypted output file.
                            Default: <input>.age

The output file is encrypted and suitable for transfer, but still should not be
committed to git.
EOF
}

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        -r|--recipient)
            shift
            recipients+=("${1:-}")
            ;;
        --recipient=*)
            recipients+=("${1#*=}")
            ;;
        -R|--recipient-file)
            shift
            recipient_files+=("${1:-}")
            ;;
        --recipient-file=*)
            recipient_files+=("${1#*=}")
            ;;
        -i|--input)
            shift
            input="${1:-}"
            ;;
        --input=*)
            input="${1#*=}"
            ;;
        -o|--out)
            shift
            output="${1:-}"
            ;;
        --out=*)
            output="${1#*=}"
            ;;
        *)
            echo "error: unknown argument: $1" >&2
            usage >&2
            exit 1
            ;;
    esac
    shift
done

if ! command -v age >/dev/null 2>&1; then
    cat >&2 <<'EOF'
error: age is not installed.

Install age first:
  Ubuntu/Debian: sudo apt install age
  macOS:         brew install age
EOF
    exit 1
fi

if [[ -z "$input" || ! -f "$input" ]]; then
    echo "error: input kubeconfig not found: $input" >&2
    exit 1
fi

if ((${#recipients[@]} == 0 && ${#recipient_files[@]} == 0)); then
    echo "error: provide at least one --recipient age1... or --recipient-file path" >&2
    exit 1
fi

if [[ -z "$output" ]]; then
    output="${input}.age"
fi

mkdir -p "$(dirname "$output")"

age_args=(--armor)
for recipient in "${recipients[@]}"; do
    if [[ -z "$recipient" ]]; then
        echo "error: empty recipient" >&2
        exit 1
    fi
    age_args+=(-r "$recipient")
done
for recipient_file in "${recipient_files[@]}"; do
    if [[ -z "$recipient_file" || ! -f "$recipient_file" ]]; then
        echo "error: recipient file not found: $recipient_file" >&2
        exit 1
    fi
    age_args+=(-R "$recipient_file")
done

age "${age_args[@]}" -o "$output" "$input"
chmod 600 "$output"

cat <<EOF
Encrypted kubeconfig written to:
  $output

Send that encrypted file to your colleague. Do not commit it to git.
EOF
