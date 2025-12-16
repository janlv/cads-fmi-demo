#!/usr/bin/env bash
# shellcheck disable=SC2034

# This script must be sourced so it can export the CA-related environment variables
# into the current shell.
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    cat >&2 <<'EOF'
[host-ca] This script exports CA-related environment variables. Run it as:

  source scripts/host_ca_env.sh            # use repo-relative certs
  source scripts/host_ca_env.sh "$PWD"     # target another folder (e.g. Kaizen_CADS)
EOF
    exit 1
fi

__cads_host_ca_env() {
    local script_dir repo_root target_root certs_dir bundle_path export_script default_root
    script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    repo_root="$(cd "$script_dir/.." && pwd)"
    default_root="$repo_root"

    resolve_under_root() {
        local base="$1"
        local path="$2"
        if [[ -z "$path" ]]; then
            printf '%s\n' "$base"
            return
        fi
        if [[ "$path" = /* ]]; then
            printf '%s\n' "$path"
        else
            printf '%s\n' "$base/$path"
        fi
    }

    if [[ -n "${1:-}" ]]; then
        if [[ -d "${1}" ]]; then
            target_root="$(cd "${1}" && pwd)"
        else
            printf '[host-ca] Provided root path "%s" does not exist or is not a directory.\n' "${1}" >&2
            return 1
        fi
    elif [[ -n "${CADS_HOST_CA_ROOT:-}" ]]; then
        if [[ -d "${CADS_HOST_CA_ROOT}" ]]; then
            target_root="$(cd "${CADS_HOST_CA_ROOT}" && pwd)"
        else
            printf '[host-ca] CADS_HOST_CA_ROOT "%s" does not exist or is not a directory.\n' "${CADS_HOST_CA_ROOT}" >&2
            return 1
        fi
    else
        target_root="$default_root"
    fi

    local certs_override="${CADS_HOST_CA_CERT_DIR:-}"
    if [[ -n "$certs_override" ]]; then
        certs_dir="$(resolve_under_root "$target_root" "$certs_override")"
    else
        certs_dir="$target_root/scripts/certs"
    fi

    local bundle_override="${CADS_HOST_CA_BUNDLE:-}"
    if [[ -n "$bundle_override" ]]; then
        bundle_path="$(resolve_under_root "$target_root" "$bundle_override")"
    else
        bundle_path="$target_root/.local/custom-ca-bundle.pem"
    fi

    local export_override="${CADS_HOST_CA_EXPORT_SCRIPT:-}"
    if [[ -n "$export_override" ]]; then
        export_script="$(resolve_under_root "$target_root" "$export_override")"
    else
        export_script="$repo_root/scripts/export_company_certs.py"
    fi

    ensure_cert_files() {
        shopt -s nullglob
        local files=("$certs_dir"/*.crt "$certs_dir"/*.pem)
        shopt -u nullglob
        if ((${#files[@]} > 0)); then
            return 0
        fi
        if [[ ! -f "$export_script" ]]; then
            printf '[host-ca] No certificates found and %s is missing. Add your CA files manually under %s.\n' "$export_script" "$certs_dir" >&2
            return 1
        fi
        if ! command -v python3 >/dev/null 2>&1; then
            printf '[host-ca] No certificates found under %s and python3 is unavailable to run export_company_certs.py.\n' "$certs_dir" >&2
            return 1
        fi
        printf '[host-ca] No certificates detected. Running %s to export them...\n' "$export_script"
        if ! python3 "$export_script" --dest "$certs_dir"; then
            printf '[host-ca] Automatic certificate export failed. Add CA files manually under %s.\n' "$certs_dir" >&2
            return 1
        fi
        return 0
    }

    if [[ ! -d "$certs_dir" ]]; then
        mkdir -p "$certs_dir"
    fi

    if ! ensure_cert_files; then
        return 1
    fi

    shopt -s nullglob
    local cert_files=("$certs_dir"/*.crt "$certs_dir"/*.pem)
    shopt -u nullglob

    if ((${#cert_files[@]} == 0)); then
        printf '[host-ca] No .crt/.pem files found under %s; aborting.\n' "$certs_dir" >&2
        return 1
    fi

    local bundle_dir
    bundle_dir="$(dirname "$bundle_path")"
    mkdir -p "$bundle_dir"
    : >"$bundle_path"
    local cert
    for cert in "${cert_files[@]}"; do
        cat "$cert" >>"$bundle_path"
        printf '\n' >>"$bundle_path"
    done

    export SSL_CERT_FILE="$bundle_path"
    export REQUESTS_CA_BUNDLE="$bundle_path"
    export CURL_CA_BUNDLE="$bundle_path"
    export GIT_SSL_CAINFO="$bundle_path"

    printf '[host-ca] Exported SSL_CERT_FILE/REQUESTS_CA_BUNDLE/CURL_CA_BUNDLE/GIT_SSL_CAINFO -> %s\n' "$bundle_path"

    unset -f ensure_cert_files
    unset -f resolve_under_root
}

__cads_host_ca_env "$@"
unset -f __cads_host_ca_env
