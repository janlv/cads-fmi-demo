#!/usr/bin/env bash
# Shared runtime helpers for repo scripts.

if [[ "${CADS_RUNTIME_SH_LOADED:-}" != "$BASHPID" ]]; then
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
        value="${value,,}"
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
            printf 'podman\n'
            return 0
        fi
        if command -v docker >/dev/null 2>&1; then
            printf 'docker\n'
            return 0
        fi
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
        if [[ -f "$root_dir/scripts/host_ca_env.sh" ]]; then
            # shellcheck disable=SC1090
            source "$root_dir/scripts/host_ca_env.sh" "$root_dir" >/dev/null 2>&1 || true
        fi
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

    CADS_RUNTIME_SH_LOADED="$BASHPID"
fi
