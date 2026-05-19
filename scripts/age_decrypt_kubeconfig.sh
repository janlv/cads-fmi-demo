#!/usr/bin/env bash
set -euo pipefail

identity="${AGE_KEY_FILE:-$HOME/.config/age/key.txt}"
recipient_file="${AGE_RECIPIENT_FILE:-$HOME/.config/cads/age-recipient.txt}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output="$ROOT_DIR/.local/kaizen/kubeconfig"
input=""
force=0
remote_source=""
remote_path="${CADS_REMOTE_KUBECONFIG_PATH:-auto}"
downloaded_input=""
repo_name="$(basename "$ROOT_DIR")"

usage() {
    cat <<'EOF'
Usage: scripts/age_decrypt_kubeconfig.sh [encrypted-kubeconfig.age] [options]

Decrypts an age-encrypted Kaizen kubeconfig into the dashboard default path.

Options:
  -i, --identity PATH  age private key file.
                       Default: $AGE_KEY_FILE or ~/.config/age/key.txt
  --recipient-file PATH
                       Public age recipient file used by --get-from.
                       Default: $AGE_RECIPIENT_FILE or ~/.config/cads/age-recipient.txt
  -o, --out PATH       Decrypted kubeconfig path.
                       Default: .local/kaizen/kubeconfig in this checkout
  --get-from USER@HOST Encrypt the remote kubeconfig with the stored public
                       recipient key, fetch it, and decrypt it locally. The
                       remote SSH password is requested by ssh when needed.
  --remote-path PATH   Remote plaintext kubeconfig path for --get-from.
                       Default: auto-detect github/<repo>/.local/kaizen/kubeconfig,
                       <repo>/.local/kaizen/kubeconfig, or .local/kaizen/kubeconfig
                       on the remote host
                       (override default with CADS_REMOTE_KUBECONFIG_PATH)
  --force              Overwrite an existing output file.

After decrypting, run:
  ./run_playground.sh
EOF
}

cleanup() {
    if [[ -n "$downloaded_input" && -f "$downloaded_input" ]]; then
        rm -f "$downloaded_input"
    fi
}

ensure_recipient_file() {
    if [[ -f "$recipient_file" ]]; then
        return
    fi

    if ! command -v age-keygen >/dev/null 2>&1; then
        echo "error: age-keygen is not installed or not on PATH" >&2
        echo "hint: run scripts/age_create_identity.sh on this machine first" >&2
        return 1
    fi

    mkdir -p "$(dirname "$recipient_file")"
    age-keygen -y "$identity" > "$recipient_file"
    chmod 600 "$recipient_file"
    echo "Derived public age recipient file from local identity: $recipient_file"
}

fetch_remote_input() {
    local source="$1"
    local path="$2"
    local repo="$3"
    local recipient=""

    if ! command -v ssh >/dev/null 2>&1; then
        echo "error: ssh is not installed or not on PATH" >&2
        return 1
    fi

    if [[ "$source" != *@* || "$source" == *[[:space:]]* ]]; then
        echo "error: --get-from expects USER@HOST" >&2
        return 1
    fi

    if [[ -n "$path" && "$path" == *[[:space:]]* ]]; then
        echo "error: --remote-path must be a non-empty path without whitespace" >&2
        return 1
    fi

    ensure_recipient_file

    recipient="$(<"$recipient_file")"
    if [[ -z "$recipient" || "$recipient" != age1* || "$recipient" == *[[:space:]]* ]]; then
        echo "error: invalid public age recipient in $recipient_file" >&2
        return 1
    fi

    downloaded_input="$(mktemp "${TMPDIR:-/tmp}/cads-kubeconfig.XXXXXX.age")"
    ssh -o BatchMode=no "$source" sh -s -- "$path" "$recipient" "$repo" > "$downloaded_input" <<'REMOTE_SCRIPT'
set -eu

input="$1"
recipient="$2"
repo="$3"
selected=""

if ! command -v age >/dev/null 2>&1; then
    echo "error: age is not installed on the remote host" >&2
    exit 1
fi

if [ -n "$input" ] && [ "$input" != "auto" ]; then
    candidates="$input"
else
    candidates="github/$repo/.local/kaizen/kubeconfig $repo/.local/kaizen/kubeconfig .local/kaizen/kubeconfig"
fi

for candidate in $candidates; do
    case "$candidate" in
        "~/"*) candidate="$HOME/${candidate#~/}" ;;
    esac
    if [ -f "$candidate" ]; then
        selected="$candidate"
        break
    fi
done

if [ -z "$selected" ]; then
    echo "error: remote kubeconfig not found. Tried: $candidates" >&2
    exit 1
fi

age --armor -r "$recipient" "$selected"
REMOTE_SCRIPT
    chmod 600 "$downloaded_input"
    input="$downloaded_input"

    echo "Encrypted and fetched remote kubeconfig from ${source}:${path}"
}

trap cleanup EXIT

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
        --recipient-file)
            shift
            recipient_file="${1:-}"
            if [[ -z "$recipient_file" ]]; then
                echo "error: --recipient-file expects a path" >&2
                exit 1
            fi
            ;;
        --recipient-file=*)
            recipient_file="${1#*=}"
            if [[ -z "$recipient_file" ]]; then
                echo "error: --recipient-file expects a path" >&2
                exit 1
            fi
            ;;
        -o|--out)
            shift
            output="${1:-}"
            ;;
        --out=*)
            output="${1#*=}"
            ;;
        --get-from)
            shift
            remote_source="${1:-}"
            if [[ -z "$remote_source" ]]; then
                echo "error: --get-from expects USER@HOST" >&2
                exit 1
            fi
            ;;
        --get-from=*)
            remote_source="${1#*=}"
            if [[ -z "$remote_source" ]]; then
                echo "error: --get-from expects USER@HOST" >&2
                exit 1
            fi
            ;;
        --remote-path)
            shift
            remote_path="${1:-}"
            if [[ -z "$remote_path" ]]; then
                echo "error: --remote-path expects a path" >&2
                exit 1
            fi
            ;;
        --remote-path=*)
            remote_path="${1#*=}"
            if [[ -z "$remote_path" ]]; then
                echo "error: --remote-path expects a path" >&2
                exit 1
            fi
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

if [[ -n "$remote_source" && -n "$input" ]]; then
    echo "error: provide either an encrypted input file or --get-from, not both" >&2
    exit 1
fi

if ! command -v age >/dev/null 2>&1; then
    cat >&2 <<'EOF'
error: age is not installed.

Install age first:
  Ubuntu/Debian: sudo apt install age
  macOS:         brew install age
EOF
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

if [[ -n "$remote_source" ]]; then
    fetch_remote_input "$remote_source" "$remote_path" "$repo_name"
fi

if [[ -z "$input" || ! -f "$input" ]]; then
    echo "error: encrypted input file not found: ${input:-<missing>}" >&2
    exit 1
fi

mkdir -p "$(dirname "$output")"
age -d -i "$identity" -o "$output" "$input"
chmod 600 "$output"

cat <<EOF
Decrypted kubeconfig written to:
  $output

You can now run:
  ./run_playground.sh
EOF
