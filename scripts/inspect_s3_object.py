#!/usr/bin/env python3
"""
Fetch metadata and a small preview for an S3 object.
"""

from __future__ import annotations

import argparse
import base64
import gzip
import io
import re
import sys
import tarfile

from list_s3_objects import (
    _env_bool,
    _env_default,
    build_client,
    configure_from_k8s_secret,
    default_kubeconfig_path,
)

AUTO_ARCHIVE_FETCH_LIMIT = 8 * 1024 * 1024
ARCHIVE_TEXT_PREVIEW_LIMIT = 512


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Inspect one S3 object using standard AWS-style credentials."
    )
    parser.add_argument(
        "key",
        help="Full object key to inspect, for example artifacts/my-file.",
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
        "--bytes",
        type=int,
        default=int(_env_default("S3_INSPECT_BYTES", "4096")),
        help="How many leading bytes to fetch for preview (default: %(default)s or $S3_INSPECT_BYTES).",
    )
    parser.add_argument(
        "--path-style",
        action="store_true",
        default=_env_bool("S3_FORCE_PATH_STYLE", False),
        help="Force path-style addressing (default: off unless $S3_FORCE_PATH_STYLE is truthy).",
    )
    parser.add_argument(
        "--secret-name",
        default=_env_default("S3_K8S_SECRET_NAME", "storhy-argo-artifacts-s3-credentials"),
        help="Kubernetes secret name used for auto-discovery.",
    )
    parser.add_argument(
        "--secret-namespace",
        default=_env_default("S3_K8S_SECRET_NAMESPACE", "playground"),
        help="Kubernetes secret namespace used for auto-discovery.",
    )
    parser.add_argument(
        "--kubeconfig",
        default=_env_default("KUBECONFIG", default_kubeconfig_path()),
        help="Kubeconfig path for secret lookup.",
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
    if not args.path_style and args.endpoint:
        args.path_style = True

    if not args.bucket:
        print("[s3] Missing bucket configuration. Set --bucket/$S3_BUCKET or allow auto-discovery from the Kaizen secret.", file=sys.stderr)
        return 2
    if not args.access_key_id or not args.secret_access_key:
        print("[s3] Missing S3 credentials. Set AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY or allow auto-discovery from the Kaizen secret.", file=sys.stderr)
        return 2
    if args.bytes <= 0:
        print("[s3] --bytes must be positive.", file=sys.stderr)
        return 2

    client = build_client(
        args.endpoint,
        args.region,
        args.path_style,
        args.access_key_id,
        args.secret_access_key,
    )

    preview_bytes = b""
    response = None
    try:
        response = client.get_object(
            Bucket=args.bucket,
            Key=args.key,
            Range=f"bytes=0-{args.bytes - 1}",
        )
        preview_bytes = response["Body"].read()
    except Exception as exc:
        print(f"[s3] Unable to fetch preview bytes for s3://{args.bucket}/{args.key}: {exc}", file=sys.stderr)
        return 1
    finally:
        body = response.get("Body") if response is not None else None
        if body is not None:
            try:
                body.close()
            except Exception:
                pass

    total_size = object_size_from_response(response)
    total_size_int = parse_size(total_size)
    inspection_bytes = preview_bytes

    if should_fetch_full_archive(preview_bytes, response.get("ContentType"), total_size_int, args.bytes):
        full_object_bytes = fetch_full_object(
            client=client,
            bucket=args.bucket,
            key=args.key,
            total_size=total_size_int,
        )
        if full_object_bytes is not None:
            inspection_bytes = full_object_bytes

    print(f"Bucket: {args.bucket}")
    print(f"Key: {args.key}")
    print(f"Size: {total_size}")
    print(f"ContentType: {response.get('ContentType', 'n/a')}")
    print(f"LastModified: {response.get('LastModified', 'n/a')}")
    print(f"ETag: {response.get('ETag', 'n/a')}")
    print(f"PreviewBytes: {len(preview_bytes)}")
    print("")

    if not preview_bytes:
        print("[s3] Object is empty.")
        return 0

    archive_output = describe_archive(inspection_bytes)
    if archive_output is not None:
        print(archive_output)
    elif looks_like_text(preview_bytes):
        print("Preview (utf-8 text):")
        text = preview_bytes.decode("utf-8", errors="replace")
        print(text.rstrip("\n"))
    else:
        print("Preview (base64):")
        print(base64.b64encode(preview_bytes).decode("ascii"))

    return 0


def object_size_from_response(response: dict) -> str:
    content_range = response.get("ContentRange")
    if isinstance(content_range, str):
        match = re.match(r"bytes\s+\d+-\d+/(\d+)", content_range)
        if match:
            return match.group(1)

    content_length = response.get("ContentLength")
    if content_length is not None:
        return str(content_length)
    return "n/a"


def parse_size(value: str) -> int | None:
    if not value or value == "n/a":
        return None
    try:
        return int(value)
    except ValueError:
        return None


def should_fetch_full_archive(
    preview_bytes: bytes,
    content_type: str | None,
    total_size: int | None,
    requested_preview_bytes: int,
) -> bool:
    if total_size is None or total_size <= requested_preview_bytes:
        return False
    if total_size > AUTO_ARCHIVE_FETCH_LIMIT:
        return False
    return looks_like_archive(preview_bytes, content_type)


def fetch_full_object(client, bucket: str, key: str, total_size: int | None) -> bytes | None:
    if total_size is not None and total_size > AUTO_ARCHIVE_FETCH_LIMIT:
        return None

    response = None
    try:
        response = client.get_object(Bucket=bucket, Key=key)
        return response["Body"].read()
    except Exception as exc:
        print(f"[s3] Unable to fetch the full object for archive inspection: {exc}", file=sys.stderr)
        return None
    finally:
        body = response.get("Body") if response is not None else None
        if body is not None:
            try:
                body.close()
            except Exception:
                pass


def looks_like_archive(data: bytes, content_type: str | None) -> bool:
    lowered = (content_type or "").lower()
    if lowered in {"application/gzip", "application/x-gzip", "application/x-tar", "application/tar"}:
        return True
    return is_gzip_bytes(data) or is_tar_bytes(data)


def is_gzip_bytes(data: bytes) -> bool:
    return len(data) >= 2 and data[:2] == b"\x1f\x8b"


def is_tar_bytes(data: bytes) -> bool:
    return len(data) >= 262 and data[257:262] == b"ustar"


def describe_archive(data: bytes) -> str | None:
    if is_gzip_bytes(data):
        try:
            decompressed = gzip.decompress(data)
        except OSError:
            return None

        nested = describe_tar_archive(decompressed)
        if nested is not None:
            return "Archive: gzip -> tar\n" + nested

        if looks_like_text(decompressed):
            text = decompressed.decode("utf-8", errors="replace").rstrip("\n")
            return "Archive: gzip\nDecompressed preview (utf-8 text):\n" + text

        return (
            "Archive: gzip\n"
            f"DecompressedBytes: {len(decompressed)}\n"
            "Decompressed preview (base64):\n"
            + base64.b64encode(decompressed[:ARCHIVE_TEXT_PREVIEW_LIMIT]).decode("ascii")
        )

    return describe_tar_archive(data)


def describe_tar_archive(data: bytes) -> str | None:
    if not is_tar_bytes(data):
        return None

    try:
        with tarfile.open(fileobj=io.BytesIO(data), mode="r:") as tf:
            members = tf.getmembers()
            lines = ["Archive: tar", f"Members: {len(members)}"]
            previewed_member = False
            for member in members:
                kind = "dir" if member.isdir() else "file"
                lines.append(f"- {member.name}  size={member.size}  type={kind}")
                if previewed_member or not member.isfile() or member.size == 0:
                    continue
                extracted = tf.extractfile(member)
                if extracted is None:
                    continue
                payload = extracted.read(min(member.size, ARCHIVE_TEXT_PREVIEW_LIMIT))
                if looks_like_text(payload):
                    text = payload.decode("utf-8", errors="replace").rstrip("\n")
                    lines.append(f"  Preview ({member.name}):")
                    if text:
                        for line in text.splitlines():
                            lines.append(f"    {line}")
                    else:
                        lines.append("    [empty text file]")
                    previewed_member = True
            return "\n".join(lines)
    except tarfile.TarError:
        return None


def looks_like_text(data: bytes) -> bool:
    if b"\x00" in data:
        return False
    try:
        decoded = data.decode("utf-8")
    except UnicodeDecodeError:
        return False

    printable = 0
    for char in decoded:
        if char.isprintable() or char in "\n\r\t":
            printable += 1
    if not decoded:
        return False
    return printable / len(decoded) >= 0.9


if __name__ == "__main__":
    sys.exit(main())
