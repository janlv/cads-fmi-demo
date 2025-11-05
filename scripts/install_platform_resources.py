#!/usr/bin/env python3
"""
Install architecture-specific pythonfmu resource bundles.

Copies staged resources from platform_resources/<profile>/pythonfmu_resources
into the root-level pythonfmu_resources/ directory (ignored by git). When the
cached resources are missing, the script bootstraps them automatically by
spinning up a temporary Python Docker image for the relevant architecture,
installing pythonfmu, rebuilding the exporter, and copying its resource
directory back to the host.
"""

from __future__ import annotations

import argparse
import platform
import shutil
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PLATFORM_RESOURCES = ROOT / "platform_resources"
TARGET_DIR = ROOT / "pythonfmu_resources"
PYTHONFMU_VERSION = "0.6.9"

PROFILE_SOURCES = {
    "linux": ["linux"],
    "apple": ["linux", "apple"],
}

DOCKER_PLATFORMS = {
    "linux": "linux/amd64",
    "apple": "linux/arm64",
}


def copy_tree(src: Path, dest: Path) -> None:
    for path in src.rglob("*"):
        relative = path.relative_to(src)
        dest_path = dest / relative
        if path.is_dir():
            dest_path.mkdir(parents=True, exist_ok=True)
        else:
            dest_path.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(path, dest_path)


def detect_profile() -> str:
    system = platform.system().lower()
    machine = platform.machine().lower()
    if system == "darwin" and machine in {"arm64", "aarch64"}:
        return "apple"
    return "linux"


def bootstrap_source(source: str) -> None:
    src_dir = PLATFORM_RESOURCES / source / "pythonfmu_resources"
    if src_dir.exists() and any(src_dir.iterdir()):
        return

    if shutil.which("docker") is None:
        raise SystemExit("Docker is required to bootstrap platform resources automatically.")

    platform_flag = DOCKER_PLATFORMS[source]
    destination_root = src_dir.parent
    destination_root.mkdir(parents=True, exist_ok=True)
    if src_dir.exists():
        shutil.rmtree(src_dir)

    print(f"- Bootstrapping resources for '{source}' using Docker ({platform_flag})")
    certs_mount: list[str] = []
    certs_snippet = ""
    cert_env = ""
    certs_dir = ROOT / "certs"
    ca_files = [f for f in certs_dir.iterdir() if f.is_file()] if certs_dir.exists() else []
    if ca_files:
        certs_mount = ["-v", f"{certs_dir.resolve()}:/tmp/company-certs:ro"]
        primary = ca_files[0].name
        cert_env = (
            f'export REQUESTS_CA_BUNDLE="/tmp/company-certs/{primary}"\n'
            f'export PIP_CERT="/tmp/company-certs/{primary}"\n'
        )
        certs_snippet = (
            "if [ -d /tmp/company-certs ]; then\n"
            "  mkdir -p /usr/local/share/ca-certificates/company\n"
            "  for file in /tmp/company-certs/*; do\n"
            "    [ -f \"$file\" ] || continue\n"
            "    base=$(basename \"$file\")\n"
            "    case \"$base\" in\n"
            "      *.pem)\n"
            "        cp \"$file\" \"/usr/local/share/ca-certificates/company/${base%.pem}.crt\"\n"
            "        ;;\n"
            "      *.crt)\n"
            "        cp \"$file\" \"/usr/local/share/ca-certificates/company/$base\"\n"
            "        ;;\n"
            "      *)\n"
            "        continue\n"
            "        ;;\n"
            "    esac\n"
            "  done\n"
            "  update-ca-certificates >/dev/null 2>&1 || true\n"
            "fi\n"
        )

    docker_cmd = [
        "docker",
        "run",
        "--rm",
        f"--platform={platform_flag}",
        "-v",
        f"{destination_root.resolve()}:/out",
        *certs_mount,
        "python:3.11-slim",
        "bash",
        "-lc",
        "set -euo pipefail\n"
        f"{cert_env}"
        "apt-get update >/dev/null\n"
        f"{certs_snippet}"
        "DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "
        "build-essential cmake unzip >/dev/null\n"
        f"pip install --no-cache-dir pythonfmu=={PYTHONFMU_VERSION} >/dev/null\n"
        "PYFMI_EXPORT_DIR=/usr/local/lib/python3.11/site-packages/pythonfmu/pythonfmu-export\n"
        "cd \"$PYFMI_EXPORT_DIR\"\n"
        "chmod +x build_unix.sh\n"
        "./build_unix.sh >/dev/null\n"
        "rm -rf build\n"
        "rm -rf /out/pythonfmu_resources\n"
        "cp -a /usr/local/lib/python3.11/site-packages/pythonfmu/resources /out/pythonfmu_resources\n",
    ]
    try:
        subprocess.run(docker_cmd, check=True)
    except subprocess.CalledProcessError as exc:
        raise SystemExit(f"Failed to bootstrap resources for '{source}'.") from exc

    if not src_dir.exists() or not any(src_dir.iterdir()):
        raise SystemExit(f"Bootstrapping did not produce expected directory: {src_dir}")


def install(profile: str, *, dry_run: bool = False, allow_bootstrap: bool = True) -> None:
    try:
        sources = PROFILE_SOURCES[profile]
    except KeyError as exc:
        raise SystemExit(f"Unknown profile '{profile}'. Available: {', '.join(PROFILE_SOURCES)}") from exc

    print(f"Installing pythonfmu resources for profile: {profile}")

    for source_name in sources:
        src = PLATFORM_RESOURCES / source_name / "pythonfmu_resources"
        if not src.exists() or not any(src.iterdir()):
            if allow_bootstrap:
                bootstrap_source(source_name)
            else:
                raise SystemExit(
                    f"Source directory missing or empty: {src}\n"
                    "Run again without --no-bootstrap to generate it."
                )

    if TARGET_DIR.exists():
        print(f"- Removing existing {TARGET_DIR}")
        if not dry_run:
            shutil.rmtree(TARGET_DIR)
    if not dry_run:
        TARGET_DIR.mkdir(parents=True, exist_ok=True)

    for source_name in sources:
        src = PLATFORM_RESOURCES / source_name / "pythonfmu_resources"
        print(f"- Applying resources from {src}")
        if not dry_run:
            copy_tree(src, TARGET_DIR)

    print(f"Done. Installed resources at {TARGET_DIR}")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--profile",
        choices=sorted(PROFILE_SOURCES),
        help="Resource profile to install (default: auto-detect)",
    )
    parser.add_argument("--dry-run", action="store_true", help="Print actions without copying files.")
    parser.add_argument(
        "--no-bootstrap",
        action="store_true",
        help="Fail if cached resources are missing instead of generating them via Docker.",
    )
    args = parser.parse_args()

    profile = args.profile or detect_profile()
    install(profile, dry_run=args.dry_run, allow_bootstrap=not args.no_bootstrap)


if __name__ == "__main__":
    main()
