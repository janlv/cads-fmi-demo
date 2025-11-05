#!/usr/bin/env python3
"""
Install architecture-specific pythonfmu resource bundles.

Resources are cached under ``cache/<profile>/pythonfmu_resources``. When the
cache is missing, the script bootstraps it automatically by spinning up a
temporary Python Docker image for the relevant architecture, installing
pythonfmu, rebuilding the exporter, and copying its resource directory back to
the host.
"""

from __future__ import annotations

import argparse
import platform
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CACHE_ROOT = ROOT / "cache"
PYTHONFMU_VERSION = "0.6.9"

PROFILE_SOURCES = {
    "linux": ["linux"],
    "apple": ["linux", "apple"],
}

DOCKER_PLATFORMS = {
    "linux": "linux/amd64",
    "apple": "linux/arm64",
}

CERT_ERROR_PATTERNS = (
    "certificate verify failed",
    "certificateverifyfailed",
    "unable to get local issuer certificate",
    "problem confirming the ssl certificate",
    "ssl: wrong version number",
    "tls: bad certificate",
    "httpsconnectionpool",
)

CERTS_DIR = ROOT / "certs"
EXPORT_SCRIPT = ROOT / "scripts" / "export_company_certs.py"


def detect_profile() -> str:
    system = platform.system().lower()
    machine = platform.machine().lower()
    if system == "darwin" and machine in {"arm64", "aarch64"}:
        return "apple"
    return "linux"


def ensure_company_certs(verbose: bool = False) -> bool:
    if not EXPORT_SCRIPT.exists():
        print("[certs] export_company_certs.py not found; skipping automatic certificate export.")
        return False
    cmd = [sys.executable, str(EXPORT_SCRIPT)]
    print("[certs] Attempting to export corporate certificates required for pip SSL.")
    try:
        subprocess.run(cmd, check=True, cwd=str(ROOT))
        return True
    except subprocess.CalledProcessError as exc:
        if verbose:
            print(f"[certs] Certificate export failed with return code {exc.returncode}.")
        return False


def _certs_handling() -> tuple[list[str], str, str, bool]:
    certs_mount: list[str] = []
    certs_snippet = ""
    certs_env = ""
    ca_files = [
        f
        for f in CERTS_DIR.iterdir()
        if f.is_file() and f.suffix.lower() in {".crt", ".pem"}
    ] if CERTS_DIR.exists() else []
    if ca_files:
        certs_mount = ["-v", f"{CERTS_DIR.resolve()}:/tmp/company-certs:ro"]
        certs_env = (
            "export REQUESTS_CA_BUNDLE=/tmp/company-ca.pem\n"
            "export PIP_CERT=/tmp/company-ca.pem\n"
        )
        certs_snippet = (
            "if [ -d /tmp/company-certs ]; then\n"
            "  mkdir -p /usr/local/share/ca-certificates/company\n"
            "  : > /tmp/company-ca.pem\n"
            "  for file in /tmp/company-certs/*; do\n"
            "    [ -f \"$file\" ] || continue\n"
            "    base=$(basename \"$file\")\n"
            "    case \"$base\" in\n"
            "      *.pem)\n"
            "        cp \"$file\" \"/usr/local/share/ca-certificates/company/${base%.pem}.crt\"\n"
            "        cat \"$file\" >> /tmp/company-ca.pem\n"
            "        ;;\n"
            "      *.crt)\n"
            "        cp \"$file\" \"/usr/local/share/ca-certificates/company/$base\"\n"
            "        cat \"$file\" >> /tmp/company-ca.pem\n"
            "        ;;\n"
            "      *)\n"
            "        continue\n"
            "        ;;\n"
            "    esac\n"
            "  done\n"
            "  update-ca-certificates >/dev/null 2>&1 || true\n"
            "fi\n"
        )
    return certs_mount, certs_snippet, certs_env, bool(ca_files)


def _build_docker_cmd(destination_root: Path, platform_flag: str, certs_mount: list[str], script: str) -> list[str]:
    return [
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
        script,
    ]


def _is_cert_error(output: str) -> bool:
    lowered = output.lower()
    return any(pattern in lowered for pattern in CERT_ERROR_PATTERNS)


def _profile_resources_dir(profile: str) -> Path:
    return CACHE_ROOT / profile / "pythonfmu_resources"


def bootstrap_source(source: str, *, verbose: bool = False, auto_certs: bool = True) -> None:
    src_dir = _profile_resources_dir(source)
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

    retried_with_certs = False

    while True:
        certs_mount, certs_snippet, certs_env, have_certs = _certs_handling()
        if not have_certs and auto_certs and retried_with_certs:
            # Certificates were expected but export produced none; avoid endless loop.
            auto_certs = False

        docker_script = (
            "set -euo pipefail\n"
            "echo '[bootstrap] Updating apt cache' >&2\n"
            f"{'apt-get update' if verbose else 'apt-get update >/dev/null'}\n"
            f"{certs_snippet}"
            f"{certs_env}"
            "echo '[bootstrap] Installing build prerequisites' >&2\n"
            f"{'DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends build-essential cmake unzip' if verbose else 'DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends build-essential cmake unzip >/dev/null'}\n"
            "echo '[bootstrap] Installing pythonfmu' >&2\n"
            f"{'pip install --no-cache-dir pythonfmu=='+PYTHONFMU_VERSION if verbose else 'pip install --no-cache-dir pythonfmu=='+PYTHONFMU_VERSION+' >/dev/null'}\n"
            "PYFMI_EXPORT_DIR=/usr/local/lib/python3.11/site-packages/pythonfmu/pythonfmu-export\n"
            "cd \"$PYFMI_EXPORT_DIR\"\n"
            "chmod +x build_unix.sh\n"
            "echo '[bootstrap] Building pythonfmu-export native library' >&2\n"
            f"{'./build_unix.sh' if verbose else './build_unix.sh >/dev/null'}\n"
            "rm -rf build\n"
            "rm -rf /out/pythonfmu_resources\n"
            "cp -a /usr/local/lib/python3.11/site-packages/pythonfmu/resources /out/pythonfmu_resources\n"
        )

        docker_cmd = _build_docker_cmd(destination_root, platform_flag, certs_mount, docker_script)
        result = subprocess.run(
            docker_cmd,
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        if result.returncode == 0:
            if verbose and result.stdout:
                print(result.stdout)
            break

        output = result.stdout or ""
        if verbose and output:
            print(output)

        if auto_certs and not retried_with_certs and _is_cert_error(output):
            print("[bootstrap] TLS certificate error detected during pip install.")
            if ensure_company_certs(verbose=verbose):
                retried_with_certs = True
                print("[bootstrap] Retrying bootstrap with freshly exported certificates.")
                continue
            else:
                print("[bootstrap] Automatic certificate export failed; will fall back to original error.")

        raise SystemExit(
            f"Failed to bootstrap resources for '{source}'. "
            f"Docker exited with {result.returncode}. Output:\n{output}"
        )

    if not src_dir.exists() or not any(src_dir.iterdir()):
        raise SystemExit(f"Bootstrapping did not produce expected directory: {src_dir}")


def install(
    profile: str,
    *,
    dry_run: bool = False,
    allow_bootstrap: bool = True,
    verbose: bool = False,
    auto_certs: bool = True,
) -> None:
    try:
        sources = PROFILE_SOURCES[profile]
    except KeyError as exc:
        raise SystemExit(f"Unknown profile '{profile}'. Available: {', '.join(PROFILE_SOURCES)}") from exc

    print(f"Installing pythonfmu resources for profile: {profile}")

    if not dry_run:
        CACHE_ROOT.mkdir(parents=True, exist_ok=True)
        data_dir = ROOT / "data"
        data_dir.mkdir(parents=True, exist_ok=True)

    cache_dirs: list[Path] = []
    missing_caches: list[Path] = []

    for source_name in sources:
        cache_dir = _profile_resources_dir(source_name)
        if dry_run:
            status = "present" if cache_dir.exists() and any(cache_dir.iterdir()) else "missing"
            print(f"- Would ensure cache {cache_dir} ({status})")
            continue
        if not cache_dir.exists() or not any(cache_dir.iterdir()):
            missing_caches.append(cache_dir)
            print(f"- Cache missing at {cache_dir}; run scripts/install_platform_resources.py to bootstrap it.")
            if allow_bootstrap:
                bootstrap_source(source_name, verbose=verbose, auto_certs=auto_certs)
            else:
                raise SystemExit(
                    f"Cache directory missing or empty: {cache_dir}\n"
                    "Run again without --no-bootstrap to generate it."
                )
        print(f"- Cache ready at {cache_dir}")
        cache_dirs.append(cache_dir)

    if dry_run:
        print("Dry run complete.")
        if missing_caches:
            print("Missing caches detected. Run scripts/install_platform_resources.py to generate them before building or running FMUs.")
    else:
        print("Done. Resources cached at:")
        for path in sorted({str(p) for p in cache_dirs}):
            print(f"  - {path}")


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
    parser.add_argument(
        "--verbose",
        action="store_true",
        help="Enable verbose bootstrap logs (apt/pip output).",
    )
    parser.add_argument(
        "--no-auto-certs",
        action="store_true",
        help="Disable automatic company certificate export when pip SSL errors are detected.",
    )
    args = parser.parse_args()

    profile = args.profile or detect_profile()
    install(
        profile,
        dry_run=args.dry_run,
        allow_bootstrap=not args.no_bootstrap,
        verbose=args.verbose,
        auto_certs=not args.no_auto_certs,
    )


if __name__ == "__main__":
    main()
