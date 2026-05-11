#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROFILE="minikube"
CERTS_DIR="$ROOT_DIR/scripts/certs"

usage() {
    cat <<'EOF'
Usage: scripts/install_minikube_ca.sh [--profile name]

Copies every .crt/.pem file under scripts/certs/ into the specified Minikube profile.
EOF
}

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --profile)
            shift
            PROFILE="${1:-}"
            if [[ -z "$PROFILE" ]]; then
                echo "[error] --profile expects a value" >&2
                exit 1
            fi
            ;;
        *)
            echo "[error] Unknown argument: $1" >&2
            usage
            exit 1
            ;;
    esac
    shift || true
done

log() {
    printf '[minikube-ca] %s\n' "$1"
}

require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        printf '[error] Required command not found: %s\n' "$1" >&2
        exit 1
    fi
}

collect_cert_files() {
    if [[ ! -d "$CERTS_DIR" ]]; then
        return
    fi
    find "$CERTS_DIR" -maxdepth 1 -type f \( -name '*.crt' -o -name '*.pem' \) -print0
}

install_cert() {
    local path="$1"
    local base
    base="$(basename "$path")"
    base="${base%.*}"
    local encoded
    encoded="$(base64 < "$path" | tr -d '\n')"
    local remote="/usr/local/share/ca-certificates/${base}.crt"
    log "Installing $(basename "$path") into profile '${PROFILE}'"
    minikube ssh -p "$PROFILE" \
        "PAYLOAD='$encoded' REMOTE='$remote' bash -c 'set -euo pipefail; printf \"%s\" \"\$PAYLOAD\" | base64 -d >/tmp/custom-ca.crt; sudo install -m 0644 /tmp/custom-ca.crt \"\$REMOTE\"; rm -f /tmp/custom-ca.crt'"
}

require_cmd minikube
require_cmd base64

if ! minikube status -p "$PROFILE" >/dev/null 2>&1; then
    echo "[error] Minikube profile '$PROFILE' is not running." >&2
    exit 1
fi

cert_files=()
if [[ -d "$CERTS_DIR" ]]; then
    while IFS= read -r -d '' path; do
        cert_files+=("$path")
    done < <(collect_cert_files || true)
fi

if ((${#cert_files[@]} == 0)); then
    log "No custom certificates found under $CERTS_DIR; skipping CA sync."
    exit 0
fi

for cert in "${cert_files[@]}"; do
    install_cert "$cert"
done

log "Refreshing CA trust store inside Minikube"
minikube ssh -p "$PROFILE" "sudo update-ca-certificates >/dev/null 2>&1 || sudo update-ca-trust extract >/dev/null 2>&1 || true" >/dev/null 2>&1

log "Restarting container runtime to pick up the new certificates"
minikube ssh -p "$PROFILE" "if command -v systemctl >/dev/null 2>&1; then sudo systemctl restart docker >/dev/null 2>&1 || sudo systemctl restart containerd >/dev/null 2>&1 || true; else sudo service docker restart >/dev/null 2>&1 || sudo service containerd restart >/dev/null 2>&1 || true; fi" >/dev/null 2>&1 || true

log "Custom CA installation complete."
