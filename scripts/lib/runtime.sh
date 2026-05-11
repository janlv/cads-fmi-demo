#!/usr/bin/env bash
# Shared runtime helpers for repo scripts.

_cads_runtime_shell_pid="${BASHPID:-$$}"
if [[ "${CADS_RUNTIME_SH_LOADED:-}" != "$_cads_runtime_shell_pid" ]]; then
    cads_setup_local_path() {
        local root_dir="$1"
        local local_bin_dir="$root_dir/.local/bin"
        local local_go_bin="$root_dir/.local/go/bin"
        export PATH="$local_go_bin:$local_bin_dir:$PATH"
    }

    cads_require_cmd() {
        if ! command -v "$1" >/dev/null 2>&1; then
            log_error "Required command '$1' not found."
            exit 1
        fi
    }

    cads_sanitize_resource_name() {
        local value="$1"
        value="$(printf '%s\n' "$value" | tr '[:upper:]' '[:lower:]')"
        value="$(echo "$value" | tr -c 'a-z0-9.-' '-')"
        while [[ "$value" =~ ^[^a-z0-9]+ ]]; do
            value="${value#?}"
        done
        while [[ "$value" =~ [^a-z0-9]+$ ]]; do
            value="${value%?}"
        done
        if [[ -z "$value" ]]; then
            value="workflow"
        fi
        printf '%s\n' "$value"
    }

    cads_select_container_tool() {
        if command -v podman >/dev/null 2>&1; then
            if podman info >/dev/null 2>&1; then
                printf 'podman\n'
                return 0
            fi
            log_warn "Podman is installed but not reachable. On macOS, run 'podman machine start' and verify 'podman info'."
        fi
        if command -v docker >/dev/null 2>&1; then
            if docker info >/dev/null 2>&1; then
                printf 'docker\n'
                return 0
            fi
            log_warn "Docker is installed but not reachable. Start Docker and verify 'docker info'."
        fi
        log_error "No running container runtime found. Install/start Podman or Docker."
        return 1
    }

    cads_sync_minikube_ca() {
        local root_dir="$1"
        local profile="$2"
        log_step "Syncing custom CA certificates into Minikube"
        if ! bash "$root_dir/scripts/install_minikube_ca.sh" --profile "$profile"; then
            log_warn "Unable to install custom CA certificates inside Minikube; continuing without them."
        fi
    }

    cads_ensure_argo_controller() {
        local root_dir="$1"
        local namespace="$2"
        log_step "Ensuring Argo Workflows is installed in Minikube"
        bash "$root_dir/scripts/ensure_argo_workflows.sh" --namespace "$namespace"
    }

    cads_load_image_into_minikube() {
        local image="$1"
        local profile="$2"
        local container_tool="${3:-}"

        if ! command -v minikube >/dev/null 2>&1; then
            log_warn "minikube command not found; skipping image load"
            return
        fi

        if [[ -z "$container_tool" ]]; then
            container_tool="$(cads_select_container_tool || true)"
        fi

        log_step "Loading $image into Minikube profile ${profile}"
        if minikube image load -p "$profile" "$image" >/dev/null 2>&1; then
            return
        fi

        log_warn "minikube image load failed; falling back to streaming the image."
        if [[ "$container_tool" == "podman" ]] && command -v podman >/dev/null 2>&1; then
            if podman image exists "$image" >/dev/null 2>&1; then
                if podman save "$image" | minikube -p "$profile" image load -; then
                    return
                fi
            fi
        elif [[ "$container_tool" == "docker" ]] && command -v docker >/dev/null 2>&1; then
            if docker image inspect "$image" >/dev/null 2>&1; then
                if docker save "$image" | minikube -p "$profile" image load -; then
                    return
                fi
            fi
        fi

        log_warn "Unable to preload $image into Minikube; workflows may need to pull the tag manually."
    }

    cads_source_host_ca() {
        local root_dir="$1"
        local had_errexit=0
        local status=0
        if [[ -f "$root_dir/scripts/host_ca_env.sh" ]]; then
            case "$-" in
                *e*)
                    had_errexit=1
                    set +e
                    ;;
            esac
            # shellcheck disable=SC1090
            source "$root_dir/scripts/host_ca_env.sh" "$root_dir" >/dev/null 2>&1
            status=$?
            if ((had_errexit)); then
                set -e
            fi
            return "$status"
        fi
        return 0
    }

    cads_resolve_kubeconfig() {
        local explicit="${1:-}"
        if [[ -n "$explicit" ]]; then
            printf '%s\n' "$explicit"
            return 0
        fi
        if [[ -n "${KUBECONFIG:-}" ]]; then
            printf '%s\n' "$KUBECONFIG"
            return 0
        fi
        return 1
    }

    cads_normalize_bearer_token() {
        local token="$1"
        token="${token#Bearer }"
        token="${token#bearer }"
        printf '%s\n' "$token"
    }

    cads_extract_argo_token() {
        local kubeconfig="$1"
        local context=""
        local user=""
        local token=""

        context="$(kubectl config view --raw --kubeconfig "$kubeconfig" -o jsonpath='{.current-context}' 2>/dev/null || true)"
        if [[ -n "$context" ]]; then
            user="$(kubectl config view --raw --kubeconfig "$kubeconfig" -o jsonpath="{.contexts[?(@.name==\"$context\")].context.user}" 2>/dev/null || true)"
        fi
        if [[ -n "$user" ]]; then
            token="$(kubectl config view --raw --kubeconfig "$kubeconfig" -o jsonpath="{.users[?(@.name==\"$user\")].user.token}" 2>/dev/null || true)"
        fi
        if [[ -z "$token" ]]; then
            token="$(kubectl config view --raw --kubeconfig "$kubeconfig" -o jsonpath='{.users[0].user.token}' 2>/dev/null || true)"
        fi
        if [[ -z "$token" ]]; then
            return 1
        fi
        cads_normalize_bearer_token "$token"
    }

    cads_resolve_argo_token() {
        local kubeconfig="${1:-}"
        if [[ -n "${ARGO_TOKEN:-}" ]]; then
            cads_normalize_bearer_token "$ARGO_TOKEN"
            return 0
        fi
        if [[ -z "$kubeconfig" ]]; then
            return 1
        fi
        cads_extract_argo_token "$kubeconfig"
    }

    cads_is_ghcr_image() {
        [[ "$1" == ghcr.io/* ]]
    }

    cads_guess_ghcr_owner_from_image() {
        local image="$1"
        local remainder="${image#ghcr.io/}"
        if [[ "$remainder" == "$image" || "$remainder" != */* ]]; then
            return 1
        fi
        printf '%s\n' "${remainder%%/*}"
    }

    cads_has_valid_gh_auth() {
        if ! command -v gh >/dev/null 2>&1; then
            return 1
        fi
        GH_PROMPT_DISABLED=1 gh auth status --hostname github.com >/dev/null 2>&1
    }

    cads_resolve_ghcr_token() {
        if [[ -n "${GHCR_TOKEN:-}" ]]; then
            printf '%s\n' "$GHCR_TOKEN"
            return 0
        fi
        if [[ -n "${GITHUB_TOKEN:-}" ]]; then
            printf '%s\n' "$GITHUB_TOKEN"
            return 0
        fi
        if cads_has_valid_gh_auth; then
            local token=""
            token="$(GH_PROMPT_DISABLED=1 gh auth token 2>/dev/null || true)"
            if [[ -n "$token" ]]; then
                printf '%s\n' "$token"
                return 0
            fi
        fi
        return 1
    }

    cads_resolve_ghcr_username() {
        local image="${1:-}"
        if [[ -n "${GHCR_USERNAME:-}" ]]; then
            printf '%s\n' "$GHCR_USERNAME"
            return 0
        fi
        if [[ -n "${GITHUB_ACTOR:-}" ]]; then
            printf '%s\n' "$GITHUB_ACTOR"
            return 0
        fi
        if cads_has_valid_gh_auth; then
            local username=""
            username="$(GH_PROMPT_DISABLED=1 gh api user -q .login 2>/dev/null || true)"
            if [[ -n "$username" ]]; then
                printf '%s\n' "$username"
                return 0
            fi
        fi
        cads_guess_ghcr_owner_from_image "$image"
    }

    cads_ensure_ghcr_login() {
        local image="$1"
        local container_tool="$2"
        local token=""
        local username=""

        if ! cads_is_ghcr_image "$image"; then
            return 0
        fi

        token="$(cads_resolve_ghcr_token || true)"
        if [[ -z "$token" ]]; then
            log_warn "No automatic GHCR credentials found. Reusing existing ${container_tool} login if present."
            log_warn "To automate GHCR publishing, run ./prepare_ghcr.sh or set GHCR_TOKEN/GITHUB_TOKEN."
            return 0
        fi

        username="$(cads_resolve_ghcr_username "$image" || true)"
        if [[ -z "$username" ]]; then
            log_error "Resolved a GHCR token but not a username. Set GHCR_USERNAME or GITHUB_ACTOR."
            return 1
        fi

        log_step "Authenticating ${container_tool} to ghcr.io as ${username}"
        if printf '%s\n' "$token" | "$container_tool" login ghcr.io -u "$username" --password-stdin >/dev/null 2>&1; then
            log_ok "Authenticated to ghcr.io"
            return 0
        fi

        log_error "Automatic ghcr.io login failed. Run ./prepare_ghcr.sh, or set GHCR_USERNAME plus GHCR_TOKEN."
        return 1
    }

    CADS_RUNTIME_SH_LOADED="$_cads_runtime_shell_pid"
fi
