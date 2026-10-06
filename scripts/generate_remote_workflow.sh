#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT_DIR/scripts/lib/logging.sh"
source "$ROOT_DIR/scripts/lib/runtime.sh"

ARGO_DIR="$ROOT_DIR/deploy/argo"

IMAGE="ghcr.io/janlv/cads-fmi-demo:playground"
WORKFLOW=""
SERVICE_ACCOUNT="playground-storhy-playground-pg-admin"
NAMESPACE="playground"
OUTPUT=""
S3_CREDENTIALS_SECRET="storhy-argo-artifacts-s3-credentials"
MAX_RUNTIME_SECONDS="${CADS_MAX_RUNTIME_SECONDS:-900}"
MAX_RUNTIME_CEILING_SECONDS="${CADS_MAX_RUNTIME_CEILING_SECONDS:-3600}"
CPU_REQUEST="${CADS_DEFAULT_CPU_REQUEST:-250m}"
MEMORY_REQUEST="${CADS_DEFAULT_MEMORY_REQUEST:-256Mi}"
CPU_LIMIT="${CADS_DEFAULT_CPU_LIMIT:-1}"
MEMORY_LIMIT="${CADS_DEFAULT_MEMORY_LIMIT:-1Gi}"

usage() {
    cat <<'USAGE'
Usage: scripts/generate_remote_workflow.sh workflows/foo.yaml [--image ghcr.io/...]
                                                             [--service-account name]
                                                             [--namespace name]
                                                             [--s3-credentials-secret name]
                                                             [--max-runtime-seconds N]
                                                             [--cpu quantity] [--memory quantity]
                                                             [--output deploy/argo/foo-remote-workflow.yaml]

Generates a PVC/configmap-free Argo Workflow manifest for hosted Argo instances
which only expose the demo container filesystem.

The script intentionally omits any persistent volume mounts and assumes the
referenced workflow file already exists inside the container image at the same
relative path.

The Workflow uses generateName (cads-<workflow>-), so Argo assigns a unique
name unless `argo submit --name` overrides it. It carries the same limits and
provenance as dashboard submissions:
  - spec.activeDeadlineSeconds: the workflow's metadata.limits.max_runtime_seconds,
    else --max-runtime-seconds (env CADS_MAX_RUNTIME_SECONDS, default 900),
    clamped to CADS_MAX_RUNTIME_CEILING_SECONDS (default 3600);
  - container resources: metadata.limits.cpu/memory as request and limit, else
    --cpu/--memory as request and limit, else CADS_DEFAULT_{CPU,MEMORY}_{REQUEST,LIMIT}
    (250m/256Mi requests, 1/1Gi limits);
  - labels/annotations with the workflow path, site, SHA-256 and git version.
USAGE
}

if (($# == 0)); then
    usage
    exit 1
fi

if [[ "$1" == "-h" || "$1" == "--help" ]]; then
    usage
    exit 0
fi

WORKFLOW="$1"
shift

while (($#)); do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --image)
            shift
            IMAGE="${1:-}"
            ;;
        --service-account)
            shift
            SERVICE_ACCOUNT="${1:-}"
            ;;
        --namespace)
            shift
            NAMESPACE="${1:-}"
            ;;
        --s3-credentials-secret)
            shift
            S3_CREDENTIALS_SECRET="${1:-}"
            ;;
        --output)
            shift
            OUTPUT="${1:-}"
            ;;
        --max-runtime-seconds)
            shift
            MAX_RUNTIME_SECONDS="${1:-}"
            ;;
        --cpu)
            shift
            CPU_REQUEST="${1:-}"
            CPU_LIMIT="${1:-}"
            ;;
        --memory)
            shift
            MEMORY_REQUEST="${1:-}"
            MEMORY_LIMIT="${1:-}"
            ;;
        *)
            echo "[error] Unknown argument: $1" >&2
            usage
            exit 1
            ;;
    esac
    shift || true
done

if [[ ! -f "$ROOT_DIR/$WORKFLOW" ]]; then
    echo "[error] Workflow file not found: $WORKFLOW" >&2
    exit 1
fi

CPU_PATTERN='^([0-9]+m|[0-9]+(\.[0-9]+)?)$'
MEMORY_PATTERN='^[0-9]+(\.[0-9]+)?(Ki|Mi|Gi|Ti|Pi|Ei|k|M|G|T|P|E)?$'
SECONDS_PATTERN='^[1-9][0-9]*$'

# Prints key=value lines for metadata.site_id and metadata.limits.{max_runtime_seconds,cpu,memory}.
# Handles block style and one-line flow style (`limits: {cpu: 500m, memory: 1Gi}`) only.
read_workflow_metadata() {
    awk '
        function clean(v) {
            sub(/[ \t]+#.*$/, "", v)
            gsub(/^[ \t]+|[ \t]+$/, "", v)
            gsub(/^["\047]|["\047]$/, "", v)
            return v
        }
        function emit(k, v) {
            k = clean(k); v = clean(v)
            if (k == "max_runtime_seconds" || k == "cpu" || k == "memory") print k "=" v
        }
        /^[^ \t#]/ { in_meta = ($0 ~ /^metadata:[ \t]*$/); in_limits = 0; next }
        !in_meta { next }
        /^[ \t]*(#.*)?$/ { next }
        {
            match($0, /^[ \t]*/); indent = RLENGTH
            line = substr($0, indent + 1)
        }
        in_limits && indent > limits_indent {
            split(line, kv, ":"); emit(kv[1], substr(line, index(line, ":") + 1)); next
        }
        { in_limits = 0 }
        line ~ /^site_id:/ { print "site_id=" clean(substr(line, 9)); next }
        line ~ /^limits:/ {
            rest = clean(substr(line, 8))
            if (rest ~ /^\{.*\}$/) {
                rest = substr(rest, 2, length(rest) - 2)
                n = split(rest, parts, ",")
                for (i = 1; i <= n; i++) {
                    if (index(parts[i], ":") > 0) emit(substr(parts[i], 1, index(parts[i], ":") - 1), substr(parts[i], index(parts[i], ":") + 1))
                }
            } else if (rest == "") {
                in_limits = 1; limits_indent = indent
            }
        }
    ' "$1"
}

sanitize_label_value() {
    local value
    value="$(printf '%s' "$1" | tr -c 'A-Za-z0-9._-' '-' | cut -c1-63)"
    while [[ "$value" =~ ^[-_.] ]]; do value="${value#?}"; done
    while [[ "$value" =~ [-_.]$ ]]; do value="${value%?}"; done
    printf '%s' "$value"
}

file_sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

SITE_ID=""
WORKFLOW_MAX_RUNTIME=""
WORKFLOW_CPU=""
WORKFLOW_MEMORY=""
while IFS='=' read -r key value; do
    case "$key" in
        site_id) SITE_ID="$value" ;;
        max_runtime_seconds) WORKFLOW_MAX_RUNTIME="$value" ;;
        cpu) WORKFLOW_CPU="$value" ;;
        memory) WORKFLOW_MEMORY="$value" ;;
    esac
done <<EOF_META
$(read_workflow_metadata "$ROOT_DIR/$WORKFLOW")
EOF_META

for pair in "--max-runtime-seconds/CADS_MAX_RUNTIME_SECONDS:$MAX_RUNTIME_SECONDS" "CADS_MAX_RUNTIME_CEILING_SECONDS:$MAX_RUNTIME_CEILING_SECONDS" "metadata.limits.max_runtime_seconds:${WORKFLOW_MAX_RUNTIME:-1}"; do
    if [[ ! "${pair##*:}" =~ $SECONDS_PATTERN ]]; then
        echo "[error] ${pair%:*} must be a positive integer number of seconds (got '${pair##*:}')" >&2
        exit 1
    fi
done
for pair in "cpu request:$CPU_REQUEST" "cpu limit:$CPU_LIMIT" "metadata.limits.cpu:${WORKFLOW_CPU:-1}"; do
    if [[ ! "${pair##*:}" =~ $CPU_PATTERN ]]; then
        echo "[error] ${pair%:*} '${pair##*:}' is not a CPU quantity such as 500m or 2" >&2
        exit 1
    fi
done
for pair in "memory request:$MEMORY_REQUEST" "memory limit:$MEMORY_LIMIT" "metadata.limits.memory:${WORKFLOW_MEMORY:-1Gi}"; do
    if [[ ! "${pair##*:}" =~ $MEMORY_PATTERN ]]; then
        echo "[error] ${pair%:*} '${pair##*:}' is not a memory quantity such as 512Mi or 2Gi" >&2
        exit 1
    fi
done

DEADLINE_SECONDS="$MAX_RUNTIME_SECONDS"
if [[ -n "$WORKFLOW_MAX_RUNTIME" ]]; then
    DEADLINE_SECONDS="$WORKFLOW_MAX_RUNTIME"
fi
if ((DEADLINE_SECONDS > MAX_RUNTIME_CEILING_SECONDS)); then
    DEADLINE_SECONDS="$MAX_RUNTIME_CEILING_SECONDS"
fi
if [[ -n "$WORKFLOW_CPU" ]]; then
    CPU_REQUEST="$WORKFLOW_CPU"
    CPU_LIMIT="$WORKFLOW_CPU"
fi
if [[ -n "$WORKFLOW_MEMORY" ]]; then
    MEMORY_REQUEST="$WORKFLOW_MEMORY"
    MEMORY_LIMIT="$WORKFLOW_MEMORY"
fi

BASENAME="$(basename "$WORKFLOW")"
NAME="${BASENAME%.*}"
GENERATE_BASE="$(cads_sanitize_resource_name "$NAME" | cut -c1-40)"
while [[ "$GENERATE_BASE" =~ [.-]$ ]]; do GENERATE_BASE="${GENERATE_BASE%?}"; done
GENERATE_NAME="cads-${GENERATE_BASE:-workflow}-"

WORKFLOW_SHA256="$(file_sha256 "$ROOT_DIR/$WORKFLOW")"
WORKFLOW_SHA_SHORT="$(printf '%s' "$WORKFLOW_SHA256" | cut -c1-12)"
WORKFLOW_LABEL="$(sanitize_label_value "$NAME")"
SITE_LABEL="$(sanitize_label_value "$SITE_ID")"
CADS_VERSION="$(git -C "$ROOT_DIR" describe --always --dirty 2>/dev/null || echo dev)"
SUBMITTED_FROM="$(hostname 2>/dev/null || echo unknown)"

SITE_LABEL_LINE=""
if [[ -n "$SITE_LABEL" ]]; then
    # Ends with a newline so the heredoc line below stays a single label when the site is unknown.
    SITE_LABEL_LINE="    cads.norceresearch.no/site: \"${SITE_LABEL}\"
"
fi

if [[ -z "$OUTPUT" ]]; then
    OUTPUT="$ARGO_DIR/${NAME}-remote-workflow.yaml"
fi

mkdir -p "$(dirname "$OUTPUT")"

cat >"$OUTPUT" <<YAML
apiVersion: argoproj.io/v1alpha1
kind: Workflow
metadata:
  generateName: ${GENERATE_NAME}
  namespace: ${NAMESPACE}
  labels:
    app.kubernetes.io/managed-by: cads-dashboard
    cads.norceresearch.no/workflow: "${WORKFLOW_LABEL}"
${SITE_LABEL_LINE}    cads.norceresearch.no/workflow-sha: "${WORKFLOW_SHA_SHORT}"
  annotations:
    cads.norceresearch.no/workflow-path: "${WORKFLOW}"
    cads.norceresearch.no/workflow-sha256: "${WORKFLOW_SHA256}"
    cads.norceresearch.no/dashboard-version: "${CADS_VERSION}"
    cads.norceresearch.no/submitted-from: "${SUBMITTED_FROM}"
spec:
  serviceAccountName: ${SERVICE_ACCOUNT}
  entrypoint: run-workflow
  activeDeadlineSeconds: ${DEADLINE_SECONDS}
  templates:
    - name: run-workflow
      container:
        image: ${IMAGE}
        imagePullPolicy: Always
        command: ["/app/bin/cads-workflow-runner"]
        args: ["--json-output", "--workflow", "${WORKFLOW}"]
        resources:
          requests:
            cpu: "${CPU_REQUEST}"
            memory: "${MEMORY_REQUEST}"
          limits:
            cpu: "${CPU_LIMIT}"
            memory: "${MEMORY_LIMIT}"
        env:
          - name: AWS_ACCESS_KEY_ID
            valueFrom:
              secretKeyRef:
                name: ${S3_CREDENTIALS_SECRET}
                key: access_key_id
          - name: AWS_SECRET_ACCESS_KEY
            valueFrom:
              secretKeyRef:
                name: ${S3_CREDENTIALS_SECRET}
                key: secret_access_key
          - name: AWS_REGION
            valueFrom:
              secretKeyRef:
                name: ${S3_CREDENTIALS_SECRET}
                key: region
          - name: AWS_DEFAULT_REGION
            valueFrom:
              secretKeyRef:
                name: ${S3_CREDENTIALS_SECRET}
                key: region
          - name: S3_BUCKET
            valueFrom:
              secretKeyRef:
                name: ${S3_CREDENTIALS_SECRET}
                key: bucket_name
          - name: S3_ENDPOINT
            valueFrom:
              secretKeyRef:
                name: ${S3_CREDENTIALS_SECRET}
                key: endpoint
YAML

echo "[remote-workflow] Generated $OUTPUT"
