#!/usr/bin/env bash
set -euo pipefail

identity="${AGE_KEY_FILE:-$HOME/.config/age/key.txt}"
output="$HOME/Kaizen_CADS/kubeconfig"
input=""
force=0

usage() {
    cat <<'EOF'
Usage: scripts/age_decrypt_kubeconfig.sh encrypted-kubeconfig.age [options]

Decrypts an age-encrypted Kaizen kubeconfig into the dashboard default path.

Options:
  -i, --identity PATH  age private key file.
                       Default: $AGE_KEY_FILE or ~/.config/age/key.txt
  -o, --out PATH       Decrypted kubeconfig path.
                       Default: ~/Kaizen_CADS/kubeconfig
  --force              Overwrite an existing output file.

After decrypting, run:
  ./run_dashboard.sh
EOF
}

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        -i|--identity)
            shift
            identity="${1:-}"
            ;;
        --identity=*)
            identity="${1#*=}"
            ;;
        -o|--out)
            shift
            output="${1:-}"
            ;;
        --out=*)
            output="${1#*=}"
            ;;
        --force)
            force=1
            ;;
        -*)
            echo "error: unknown argument: $1" >&2
            usage >&2
            exit 1
            ;;
        *)
            if [[ -n "$input" ]]; then
                echo "error: only one encrypted input file is supported" >&2
                usage >&2
                exit 1
            fi
            input="$1"
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
    echo "error: encrypted input file not found: ${input:-<missing>}" >&2
    exit 1
fi

if [[ -z "$identity" || ! -f "$identity" ]]; then
    echo "error: age identity file not found: $identity" >&2
    echo "hint: run scripts/age_create_identity.sh first" >&2
    exit 1
fi

if [[ -e "$output" && "$force" -ne 1 ]]; then
    echo "error: output already exists: $output" >&2
    echo "hint: pass --force to overwrite it" >&2
    exit 1
fi

mkdir -p "$(dirname "$output")"
age -d -i "$identity" -o "$output" "$input"
chmod 600 "$output"

cat <<EOF
Decrypted kubeconfig written to:
  $output

You can now run:
  ./run_dashboard.sh
EOF
