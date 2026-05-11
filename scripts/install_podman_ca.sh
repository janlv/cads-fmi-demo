#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

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

usage() {
    cat <<'EOF'
Usage: scripts/install_podman_ca.sh [--cert-dir path]

Copies .crt/.pem files into the active Podman machine trust store.

Defaults:
  --cert-dir  scripts/certs, or certs/ when scripts/certs has no certs
              (override with CADS_HOST_CA_CERT_DIR)
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

cert_files=()
while IFS= read -r -d '' cert; do
    cert_files+=("$cert")
done < <(find "$CERTS_DIR" -maxdepth 1 -type f \( -name '*.crt' -o -name '*.pem' \) -print0)

if ((${#cert_files[@]} == 0)); then
    printf '[info] No .crt/.pem files found under %s; skipping.\n' "$CERTS_DIR"
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
printf '[ok] Podman machine CA trust updated.\n'
