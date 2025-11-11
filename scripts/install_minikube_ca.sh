#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROFILE="${MINIKUBE_PROFILE:-minikube}"
DEFAULT_CERTS_DIR="$ROOT_DIR/scripts/certs"
CERTS_DIR="${MINIKUBE_EXTRA_CA_CERTS_DIR:-$DEFAULT_CERTS_DIR}"
SINGLE_CERT="${MINIKUBE_EXTRA_CA_CERT:-}"
SINGLE_NAME="${MINIKUBE_EXTRA_CA_NAME:-}"

log() {
    printf '[minikube-ca] %s\n' "$1"
}

err() {
    printf '[error] %s\n' "$1" >&2
    exit 1
}

sanitize_name() {
    local name="$1"
    name="${name//[^a-zA-Z0-9._-]/-}"
    if [[ -z "$name" ]]; then
        name="custom-ca"
    fi
    printf '%s\n' "$name"
}

collect_cert_files() {
    local files=()
    if [[ -n "$SINGLE_CERT" ]]; then
        if [[ ! -f "$SINGLE_CERT" ]]; then
            err "MINIKUBE_EXTRA_CA_CERT points to '$SINGLE_CERT', but the file does not exist."
        fi
        files+=("$SINGLE_CERT:::${SINGLE_NAME:-}")
    fi
    if [[ -d "$CERTS_DIR" ]]; then
        while IFS= read -r -d '' path; do
            files+=("$path:::")
        done < <(find "$CERTS_DIR" -maxdepth 1 -type f \( -name '*.crt' -o -name '*.pem' \) -print0 | sort -z)
    fi
    printf '%s\n' "${files[@]+"${files[@]}"}"
}

require_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        err "Required command not found: $1"
    fi
}

install_cert() {
    local spec="$1"
    local path="${spec%%:::*}"
    local name_part="${spec#*:::}"
    if [[ "$path" == "$name_part" ]]; then
        name_part=""
    fi
    local base_name
    if [[ -n "$name_part" ]]; then
        base_name="$name_part"
    else
        base_name="$(basename "$path")"
        base_name="${base_name%.*}"
    fi
    local safe_name
    safe_name="$(sanitize_name "$base_name")"
    local remote="/usr/local/share/ca-certificates/${safe_name}.crt"
    log "Installing CA '$path' as ${remote}"
    minikube ssh -p "$PROFILE" "sudo install -m 0644 /dev/stdin '$remote'" < "$path" >/dev/null 2>&1
}

main() {
    local specs
    specs=($(collect_cert_files))
    if ((${#specs[@]} == 0)); then
        return 0
    fi

    require_cmd minikube
    if ! minikube status -p "$PROFILE" >/dev/null 2>&1; then
        err "Minikube profile '$PROFILE' is not running. Start it before installing the CA certificate."
    fi

    for spec in "${specs[@]}"; do
        install_cert "$spec"
    done

    log "Updating CA trust store inside Minikube"
    minikube ssh -p "$PROFILE" "if command -v update-ca-certificates >/dev/null 2>&1; then sudo update-ca-certificates >/dev/null 2>&1; elif command -v update-ca-trust >/dev/null 2>&1; then sudo update-ca-trust extract >/dev/null 2>&1; else sudo /usr/sbin/update-ca-certificates >/dev/null 2>&1 || true; fi" >/dev/null 2>&1

    local restart_runtime_cmd='if command -v systemctl >/dev/null 2>&1; then
        sudo systemctl restart docker >/dev/null 2>&1 ||
        sudo systemctl restart containerd >/dev/null 2>&1 ||
        sudo systemctl restart crio >/dev/null 2>&1 || true
    elif command -v service >/dev/null 2>&1; then
        sudo service docker restart >/dev/null 2>&1 ||
        sudo service containerd restart >/dev/null 2>&1 ||
        sudo service crio restart >/dev/null 2>&1 || true
    fi'

    log "Restarting container runtime inside Minikube to pick up the new CA"
    minikube ssh -p "$PROFILE" "$restart_runtime_cmd >/dev/null 2>&1" >/dev/null 2>&1

    log "Custom CA installation complete."
}

main "$@"
