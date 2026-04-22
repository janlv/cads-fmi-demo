#!/usr/bin/env python3
"""
Lists objects and prefixes from an S3 or S3-compatible bucket.
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

try:
    import boto3
except ImportError as exc:  # pragma: no cover - depends on local environment
    print(
        "[s3] boto3 is required to list bucket contents. Install it with "
        "'python3 -m pip install boto3'.",
        file=sys.stderr,
    )
    raise SystemExit(1) from exc

DEFAULT_S3_SECRET_NAME = "storhy-argo-artifacts-s3-credentials"
DEFAULT_S3_SECRET_NAMESPACE = "playground"


def _env_default(name: str, fallback: str = "") -> str:
    value = os.environ.get(name)
    return value if value else fallback


def _env_bool(name: str, fallback: bool) -> bool:
    value = os.environ.get(name)
    if value is None:
        return fallback
    return value.strip().lower() in {"1", "true", "yes", "on"}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="List keys from an S3 bucket using standard AWS-style credentials."
    )
    parser.add_argument(
        "--access-key-id",
        default=_env_default("AWS_ACCESS_KEY_ID"),
        help="Access key ID (default: $AWS_ACCESS_KEY_ID).",
    )
    parser.add_argument(
        "--secret-access-key",
        default=_env_default("AWS_SECRET_ACCESS_KEY"),
        help="Secret access key (default: $AWS_SECRET_ACCESS_KEY).",
    )
    parser.add_argument(
        "--bucket",
        default=_env_default("S3_BUCKET"),
        help="Bucket name (default: $S3_BUCKET).",
    )
    parser.add_argument(
        "--prefix",
        default=_env_default("S3_PREFIX"),
        help="Only list keys under this prefix (default: $S3_PREFIX).",
    )
    parser.add_argument(
        "--delimiter",
        default=_env_default("S3_DELIMITER", "/"),
        help="Delimiter used for grouped prefix listings (default: %(default)s or $S3_DELIMITER). Use empty string for a flat listing.",
    )
    parser.add_argument(
        "--endpoint",
        default=_env_default("S3_ENDPOINT", _env_default("AWS_ENDPOINT_URL_S3", _env_default("AWS_ENDPOINT_URL"))),
        help="Custom S3 endpoint URL (default: $S3_ENDPOINT, $AWS_ENDPOINT_URL_S3, or $AWS_ENDPOINT_URL).",
    )
    parser.add_argument(
        "--region",
        default=_env_default("AWS_REGION", _env_default("AWS_DEFAULT_REGION", "us-east-1")),
        help="AWS region (default: %(default)s or $AWS_REGION/$AWS_DEFAULT_REGION).",
    )
    parser.add_argument(
        "--limit",
        type=int,
        default=int(_env_default("S3_LIST_LIMIT", "200")),
        help="Maximum number of results to print (default: %(default)s or $S3_LIST_LIMIT).",
    )
    parser.add_argument(
        "--flat",
        action="store_true",
        help="Ignore delimiter grouping and print a flat key listing.",
    )
    parser.add_argument(
        "--long",
        action="store_true",
        help="Include object size and last-modified timestamps when printing objects.",
    )
    parser.add_argument(
        "--path-style",
        action="store_true",
        default=_env_bool("S3_FORCE_PATH_STYLE", False),
        help="Force path-style addressing (default: off unless $S3_FORCE_PATH_STYLE is truthy).",
    )
    parser.add_argument(
        "--secret-name",
        default=_env_default("S3_K8S_SECRET_NAME", DEFAULT_S3_SECRET_NAME),
        help="Kubernetes secret name used for auto-discovery (default: %(default)s or $S3_K8S_SECRET_NAME).",
    )
    parser.add_argument(
        "--secret-namespace",
        default=_env_default("S3_K8S_SECRET_NAMESPACE", DEFAULT_S3_SECRET_NAMESPACE),
        help="Kubernetes secret namespace used for auto-discovery (default: %(default)s or $S3_K8S_SECRET_NAMESPACE).",
    )
    parser.add_argument(
        "--kubeconfig",
        default=_env_default("KUBECONFIG", default_kubeconfig_path()),
        help="Kubeconfig path for secret lookup (default: $KUBECONFIG or ~/Kaizen_CADS/kubeconfig when present).",
    )
    parser.add_argument(
        "--no-k8s-secret",
        action="store_true",
        help="Do not auto-load missing S3 settings from the Kaizen Kubernetes secret.",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    configure_from_k8s_secret(args)

    if not args.bucket:
        print(
            "[s3] Missing bucket configuration. Set --bucket/$S3_BUCKET or allow auto-discovery from the Kaizen secret.",
            file=sys.stderr,
        )
        return 2
    if not args.access_key_id or not args.secret_access_key:
        print(
            "[s3] Missing S3 credentials. Set AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY or allow auto-discovery from the Kaizen secret.",
            file=sys.stderr,
        )
        return 2
    if args.limit <= 0:
        print("[s3] --limit must be positive.", file=sys.stderr)
        return 2

    delimiter = "" if args.flat else args.delimiter
    client = build_client(
        args.endpoint,
        args.region,
        args.path_style,
        args.access_key_id,
        args.secret_access_key,
    )

    paginator = client.get_paginator("list_objects_v2")
    request = {"Bucket": args.bucket, "Prefix": args.prefix, "MaxKeys": min(args.limit, 1000)}
    if delimiter:
        request["Delimiter"] = delimiter

    printed = 0
    saw_any = False
    for page in paginator.paginate(**request):
        prefixes = page.get("CommonPrefixes", [])
        contents = page.get("Contents", [])

        if prefixes:
            saw_any = True
            print("Prefixes:")
            for item in prefixes:
                print(f"  {item['Prefix']}")
                printed += 1
                if printed >= args.limit:
                    return 0

        if contents:
            saw_any = True
            if prefixes:
                print("")
            print("Objects:")
            for item in contents:
                print(format_object(item, args.long))
                printed += 1
                if printed >= args.limit:
                    return 0

        if saw_any:
            print("")

    if not saw_any:
        prefix_label = args.prefix or "/"
        print(f"[s3] No objects found in s3://{args.bucket}/{prefix_label}")
    return 0


def build_client(
    endpoint: str,
    region: str,
    path_style: bool,
    access_key_id: str,
    secret_access_key: str,
):
    session = boto3.session.Session()
    config = None
    if path_style:
        from botocore.config import Config

        config = Config(s3={"addressing_style": "path"})

    kwargs = {
        "region_name": region,
        "aws_access_key_id": access_key_id,
        "aws_secret_access_key": secret_access_key,
    }
    if endpoint:
        kwargs["endpoint_url"] = endpoint
    if config is not None:
        kwargs["config"] = config
    return session.client("s3", **kwargs)


def import_requests():
    try:
        import requests
    except ImportError as exc:  # pragma: no cover - depends on local environment
        print(
            "[s3] requests is required for Kubernetes secret auto-discovery. Install it with "
            "'python3 -m pip install requests'.",
            file=sys.stderr,
        )
        raise SystemExit(1) from exc
    return requests


def import_yaml():
    try:
        import yaml
    except ImportError as exc:  # pragma: no cover - depends on local environment
        print(
            "[s3] PyYAML is required for kubeconfig parsing. Install it with "
            "'python3 -m pip install pyyaml'.",
            file=sys.stderr,
        )
        raise SystemExit(1) from exc
    return yaml


def configure_from_k8s_secret(args: argparse.Namespace) -> None:
    if args.no_k8s_secret:
        return

    missing = []
    if not args.access_key_id:
        missing.append("access_key_id")
    if not args.secret_access_key:
        missing.append("secret_access_key")
    if not args.bucket:
        missing.append("bucket_name")
    if not args.endpoint:
        missing.append("endpoint")
    if not args.region:
        missing.append("region")

    if not missing:
        return

    try:
        secret_data = read_k8s_secret(args.secret_name, args.secret_namespace, args.kubeconfig)
    except Exception as exc:
        print(f"[s3] Skipping Kubernetes secret auto-discovery: {exc}", file=sys.stderr)
        return

    args.access_key_id = pick_non_empty(
        args.access_key_id,
        secret_data.get("access_key_id"),
        secret_data.get("access_jey_id"),
    )
    args.secret_access_key = pick_non_empty(args.secret_access_key, secret_data.get("secret_access_key"))
    args.bucket = pick_non_empty(args.bucket, secret_data.get("bucket_name"))
    args.endpoint = pick_non_empty(args.endpoint, secret_data.get("endpoint"))
    args.region = pick_non_empty(args.region, secret_data.get("region"), "us-east-1")
    if not args.path_style and args.endpoint:
        args.path_style = True


def read_k8s_secret(name: str, namespace: str, kubeconfig: str) -> dict[str, str]:
    primary_error = None
    if kubeconfig:
        try:
            return read_k8s_secret_via_kubeconfig(name, namespace, kubeconfig)
        except Exception as exc:
            primary_error = exc

    command = ["kubectl", "get", "secret", name, "-n", namespace, "-o", "json"]
    if kubeconfig:
        command.extend(["--kubeconfig", kubeconfig])
    env = os.environ.copy()
    env.setdefault("GODEBUG", "http2client=0")
    for proxy_key in ("HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy"):
        env.pop(proxy_key, None)
    try:
        completed = subprocess.run(
            command,
            check=True,
            capture_output=True,
            text=True,
            env=env,
        )
    except FileNotFoundError as exc:
        if primary_error is not None:
            raise RuntimeError(str(primary_error)) from primary_error
        raise RuntimeError("kubectl is not installed") from exc
    except subprocess.CalledProcessError as exc:
        fallback_error = summarize_kubectl_error(exc.stderr, exc.stdout)
        if primary_error is not None:
            raise RuntimeError(f"{primary_error}; kubectl fallback also failed: {fallback_error}") from primary_error
        raise RuntimeError(fallback_error) from exc

    payload = json.loads(completed.stdout)
    encoded = payload.get("data", {})
    decoded = {}
    for key, value in encoded.items():
        decoded[key] = base64.b64decode(value).decode("utf-8")
    return decoded


def read_k8s_secret_via_kubeconfig(name: str, namespace: str, kubeconfig: str) -> dict[str, str]:
    if not kubeconfig:
        raise RuntimeError("kubeconfig is required for direct Kubernetes secret lookup")
    server, token, ca_data = parse_kubeconfig(kubeconfig)
    requests = import_requests()
    verify = True
    ca_file = build_verify_bundle(ca_data)
    if ca_file is not None:
        verify = ca_file.name

    url = f"{server.rstrip('/')}/api/v1/namespaces/{namespace}/secrets/{name}"
    session = requests.Session()
    session.trust_env = False
    try:
        response = session.get(
            url,
            headers={
                "Authorization": f"Bearer {token}",
                "Accept": "application/json",
            },
            timeout=20,
            verify=verify,
        )
        response.raise_for_status()
        try:
            payload = response.json()
        except ValueError as exc:
            body_preview = response.text.strip().replace("\n", " ")
            if len(body_preview) > 240:
                body_preview = body_preview[:240] + "..."
            raise RuntimeError(
                f"direct secret lookup returned HTTP {response.status_code} with a non-JSON body: {body_preview or '<empty>'}"
            ) from exc
    except Exception as exc:
        raise RuntimeError(f"direct secret lookup failed: {exc}") from exc
    finally:
        if ca_file is not None:
            try:
                os.unlink(ca_file.name)
            except OSError:
                pass

    encoded = payload.get("data", {})
    decoded = {}
    for key, value in encoded.items():
        decoded[key] = base64.b64decode(value).decode("utf-8")
    return decoded


def parse_kubeconfig(path: str) -> tuple[str, str, str]:
    yaml = import_yaml()
    with open(path, "r", encoding="utf-8") as handle:
        document = yaml.safe_load(handle) or {}

    current_context = document.get("current-context")
    contexts = {item["name"]: item.get("context", {}) for item in document.get("contexts", []) if "name" in item}
    users = {item["name"]: item.get("user", {}) for item in document.get("users", []) if "name" in item}
    clusters = {item["name"]: item.get("cluster", {}) for item in document.get("clusters", []) if "name" in item}

    context = contexts.get(current_context)
    if not context:
        raise RuntimeError(f"current context {current_context!r} not found in kubeconfig")
    cluster = clusters.get(context.get("cluster"))
    user = users.get(context.get("user"))
    if not cluster or not user:
        raise RuntimeError("kubeconfig context is missing cluster or user details")

    server = cluster.get("server")
    token = user.get("token")
    if not server or not token:
        raise RuntimeError("kubeconfig does not contain server/token for the active context")
    return server, token, cluster.get("certificate-authority-data", "")


def build_verify_bundle(ca_data: str):
    bundle_sources: list[bytes] = []

    for path in candidate_ca_bundle_paths():
        try:
            bundle_sources.append(path.read_bytes())
        except OSError:
            continue

    if ca_data:
        try:
            bundle_sources.append(base64.b64decode(ca_data))
        except Exception:
            pass

    if not bundle_sources:
        return None

    ca_file = tempfile.NamedTemporaryFile(prefix="cads-k8s-ca-", suffix=".pem", delete=False)
    for content in bundle_sources:
        if not content:
            continue
        ca_file.write(content.rstrip() + b"\n")
    ca_file.flush()
    ca_file.close()
    return ca_file


def candidate_ca_bundle_paths() -> list[Path]:
    candidates = [
        Path(_env_default("REQUESTS_CA_BUNDLE")),
        Path(_env_default("SSL_CERT_FILE")),
        Path("/etc/ssl/certs/ca-certificates.crt"),
        Path(".local/custom-ca-bundle.pem"),
        Path.home() / "Kaizen_CADS" / ".local" / "custom-ca-bundle.pem",
    ]
    seen: set[Path] = set()
    resolved: list[Path] = []
    for candidate in candidates:
        if not str(candidate):
            continue
        if candidate in seen:
            continue
        seen.add(candidate)
        if candidate.exists() and candidate.is_file():
            resolved.append(candidate)
    return resolved


def default_kubeconfig_path() -> str:
    path = Path.home() / "Kaizen_CADS" / "kubeconfig"
    return str(path) if path.exists() else ""


def pick_non_empty(*values: str) -> str:
    for value in values:
        if value and value.strip():
            return value.strip()
    return ""


def summarize_kubectl_error(stderr: str, stdout: str) -> str:
    combined = (stderr or stdout or "").strip()
    if not combined:
        return "kubectl secret lookup failed"
    lines = [line.strip() for line in combined.splitlines() if line.strip()]
    if not lines:
        return "kubectl secret lookup failed"
    for line in reversed(lines):
        if not line.startswith(("E", "W", "I")):
            return line
    return lines[-1]


def format_object(item: dict, long_format: bool) -> str:
    key = item["Key"]
    if not long_format:
        return f"  {key}"

    last_modified = item.get("LastModified")
    if hasattr(last_modified, "isoformat"):
        timestamp = last_modified.isoformat()
    else:
        timestamp = "n/a"
    size = item.get("Size", 0)
    return f"  {key}  size={size}  last_modified={timestamp}"


if __name__ == "__main__":
    sys.exit(main())
