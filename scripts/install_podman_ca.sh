#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE_DIR="$ROOT_DIR/.local/state"
STATE_FILE="$STATE_DIR/podman-ca.env"
FORCE=0

default_certs_dir() {
    if [[ -n "${CADS_HOST_CA_CERT_DIR:-}" ]]; then
        printf '%s\n' "$CADS_HOST_CA_CERT_DIR"
    elif find "$ROOT_DIR/scripts/certs" -maxdepth 1 -type f \( -name '*.crt' -o -name '*.pem' \) 2>/dev/null | grep -q .; then
        printf '%s\n' "$ROOT_DIR/scripts/certs"
    elif find "$ROOT_DIR/certs" -maxdepth 1 -type f \( -name '*.crt' -o -name '*.pem' \) 2>/dev/null | grep -q .; then
        printf '%s\n' "$ROOT_DIR/certs"
    else
        printf '%s\n' "$ROOT_DIR/scripts/certs"
    fi
}

CERTS_DIR="$(default_certs_dir)"

certs_signature() {
    local cert_dir="$1"
    (
        cd "$cert_dir"
        find . -maxdepth 1 -type f \( -name '*.crt' -o -name '*.pem' \) -print |
            sort |
            while IFS= read -r cert; do
                cksum "$cert"
            done
    ) | cksum | awk '{print $1 "-" $2}'
}

load_cache() {
    cached_cert_dir=""
    cached_certs_signature=""
    if [[ -f "$STATE_FILE" ]]; then
        # shellcheck disable=SC1090
        source "$STATE_FILE"
    fi
}

save_cache() {
    local cert_dir="$1"
    local signature="$2"
    mkdir -p "$STATE_DIR"
    {
        printf 'cached_cert_dir=%q\n' "$cert_dir"
        printf 'cached_certs_signature=%q\n' "$signature"
    } >"$STATE_FILE"
}

usage() {
    cat <<'EOF'
Usage: scripts/install_podman_ca.sh [--cert-dir path] [--force]

Copies .crt/.pem files into the active Podman machine trust store.
Skips the copy when the selected cert directory and cert checksum match the
last successful sync cached under .local/state/podman-ca.env.

Defaults:
  --cert-dir  scripts/certs, or certs/ when scripts/certs has no certs
              (override with CADS_HOST_CA_CERT_DIR)
  --force     Refresh Podman CA trust even when the cached checksum matches
EOF
}

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --cert-dir)
            shift
            CERTS_DIR="${1:-}"
            if [[ -z "$CERTS_DIR" ]]; then
                printf '[error] --cert-dir expects a path\n' >&2
                exit 1
            fi
            ;;
        --force)
            FORCE=1
            ;;
        *)
            printf '[error] Unknown argument: %s\n' "$1" >&2
            usage
            exit 1
            ;;
    esac
    shift || true
done

if ! command -v podman >/dev/null 2>&1; then
    printf '[error] podman is required.\n' >&2
    exit 1
fi

if ! podman info >/dev/null 2>&1; then
    printf '[error] Podman is not reachable. Run podman machine start and verify podman info.\n' >&2
    exit 1
fi

if [[ ! -d "$CERTS_DIR" ]]; then
    printf '[info] No certificate directory found at %s; skipping.\n' "$CERTS_DIR"
    exit 0
fi

resolved_cert_dir="$(cd "$CERTS_DIR" && pwd -P)"
cert_files=()
while IFS= read -r -d '' cert; do
    cert_files+=("$cert")
done < <(find "$resolved_cert_dir" -maxdepth 1 -type f \( -name '*.crt' -o -name '*.pem' \) -print0)

if ((${#cert_files[@]} == 0)); then
    printf '[info] No .crt/.pem files found under %s; skipping.\n' "$resolved_cert_dir"
    exit 0
fi

current_signature="$(certs_signature "$resolved_cert_dir")"
load_cache
if ((FORCE == 0)) &&
    [[ "${cached_cert_dir:-}" == "$resolved_cert_dir" ]] &&
    [[ "${cached_certs_signature:-}" == "$current_signature" ]]; then
    printf '[info] Podman machine CA trust already matches %s; skipping.\n' "$resolved_cert_dir"
    exit 0
fi

for cert in "${cert_files[@]}"; do
    base="$(basename "$cert")"
    case "$base" in
        *.pem) remote="/etc/pki/ca-trust/source/anchors/${base%.pem}.crt" ;;
        *) remote="/etc/pki/ca-trust/source/anchors/$base" ;;
    esac
    printf '[info] Installing %s into Podman machine\n' "$base"
    podman machine ssh -- "sudo tee '$remote' >/dev/null" <"$cert"
done

podman machine ssh -- 'sudo update-ca-trust extract && (sudo systemctl restart podman.socket podman.service 2>/dev/null || true)'
save_cache "$resolved_cert_dir" "$current_signature"
printf '[ok] Podman machine CA trust updated.\n'
