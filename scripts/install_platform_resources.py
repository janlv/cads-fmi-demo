#!/usr/bin/env python3
"""
Install architecture-specific pythonfmu resource bundles.

Resources are cached under ``create_fmu/artifacts/cache/<profile>/pythonfmu_resources``. When the
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
from collections import deque
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CREATE_FMU_ROOT = ROOT / "create_fmu"
ARTIFACTS_ROOT = CREATE_FMU_ROOT / "artifacts"
CACHE_ROOT = ARTIFACTS_ROOT / "cache"
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

CERTS_DIR = ROOT / "scripts" / "certs"
EXPORT_SCRIPT = ROOT / "scripts" / "export_company_certs.py"


class StageWindow:
    """Render streaming command output in a compact Colima-style window."""

    def __init__(
        self,
        title: str,
        *,
        prefix: str = "[bootstrap]",
        max_lines: int | None = 6,
    ) -> None:
        self.title = title
        self.prefix = prefix
        self.max_lines = max_lines if (max_lines is None or max_lines > 0) else None
        self._buffer: deque[str]
        if self.max_lines is None:
            self._buffer = deque()
        else:
            self._buffer = deque(maxlen=self.max_lines)
        self._is_tty = sys.stdout.isatty()
        self._term_width = (
            shutil.get_terminal_size(fallback=(80, 24)).columns if self._is_tty else None
        )
        self._line_width = max((self._term_width or 0) - 6, 16)
        self._lines_rendered = 1
        self._closed = False
        self._grey = "\033[90m" if self._is_tty else ""
        self._reset = "\033[0m" if self._is_tty else ""
        self._start()

    def _start(self) -> None:
        print(f"{self.prefix} {self.title}")
        sys.stdout.flush()

    def _truncate(self, text: str) -> str:
        if self._term_width is None or len(text) <= self._line_width:
            return text
        return text[: self._line_width - 3] + "..."

    def feed(self, message: str) -> None:
        if self._closed:
            return
        cleaned = message.strip()
        if not cleaned:
            return
        if not self._is_tty:
            print(f"{self.prefix}   {cleaned}")
            return
        self._buffer.append(self._truncate(cleaned))
        self._render()

    def _render(self) -> None:
        for _ in range(self._lines_rendered - 1):
            sys.stdout.write("\033[F\033[K")
        for line in self._buffer:
            sys.stdout.write("\033[K")
            sys.stdout.write(f"   {self._grey}{line}{self._reset}\n")
        sys.stdout.flush()
        self._lines_rendered = 1 + len(self._buffer)

    def finalize(self, *, success: bool, message: str | None = None) -> None:
        if self._closed:
            return
        summary = message or self.title
        if not self._is_tty:
            status = "ok" if success else "fail"
            print(f"[{status}] {summary}")
            self._closed = True
            return
        for _ in range(self._lines_rendered):
            sys.stdout.write("\033[F")
        sys.stdout.write("\033[J")
        sys.stdout.write(f"[{ 'ok' if success else 'fail' }] {summary}\n")
        sys.stdout.flush()
        self._buffer.clear()
        self._lines_rendered = 1
        self._closed = True


def _run_with_stage(cmd: list[str], stage: StageWindow) -> tuple[int, str]:
    captured: list[str] = []
    proc = subprocess.Popen(
        cmd,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        bufsize=1,
        encoding="utf-8",
        errors="replace",
    )
    try:
        assert proc.stdout is not None
        for chunk in proc.stdout:
            captured.append(chunk)
            normalized = chunk.replace("\r", "\n")
            for raw_line in normalized.splitlines():
                stage.feed(raw_line)
        return proc.wait(), "".join(captured)
    except BaseException:
        proc.kill()
        proc.wait()
        raise
    finally:
        if proc.stdout is not None:
            proc.stdout.close()

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


def bootstrap_source(
    source: str,
    *,
    verbose: bool = False,
    auto_certs: bool = True,
    stage_window_lines: int | None = None,
) -> None:
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

    retried_with_certs = False
    attempt = 1

    while True:
        certs_mount, certs_snippet, certs_env, have_certs = _certs_handling()
        if not have_certs and auto_certs and retried_with_certs:
            # Certificates were expected but export produced none; avoid endless loop.
            auto_certs = False

        docker_script = (
            "set -euo pipefail\n"
            "echo '[bootstrap] Updating apt cache' >&2\n"
            "apt-get update\n"
            f"{certs_snippet}"
            f"{certs_env}"
            "echo '[bootstrap] Installing build prerequisites' >&2\n"
            "DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends build-essential cmake unzip\n"
            "echo '[bootstrap] Installing pythonfmu' >&2\n"
            f"pip install --no-cache-dir pythonfmu=={PYTHONFMU_VERSION}\n"
            "PYFMI_EXPORT_DIR=/usr/local/lib/python3.11/site-packages/pythonfmu/pythonfmu-export\n"
            "cd \"$PYFMI_EXPORT_DIR\"\n"
            "chmod +x build_unix.sh\n"
            "echo '[bootstrap] Building pythonfmu-export native library' >&2\n"
            "./build_unix.sh\n"
            "rm -rf build\n"
            "rm -rf /out/pythonfmu_resources\n"
            "cp -a /usr/local/lib/python3.11/site-packages/pythonfmu/resources /out/pythonfmu_resources\n"
        )

        docker_cmd = _build_docker_cmd(destination_root, platform_flag, certs_mount, docker_script)
        attempt_label = f" (attempt {attempt})" if attempt > 1 else ""
        if stage_window_lines is None:
            max_lines = 50 if verbose else 6
        elif stage_window_lines <= 0:
            max_lines = None
        else:
            max_lines = stage_window_lines
        stage = StageWindow(
            f"Bootstrapping resources for '{source}' using Docker ({platform_flag}){attempt_label}",
            max_lines=max_lines,
        )
        returncode, output = _run_with_stage(docker_cmd, stage)

        if returncode == 0:
            stage.finalize(success=True, message=f"Resources for '{source}' ready")
            break

        stage.finalize(success=False, message=f"Bootstrap failed (exit {returncode})")

        if auto_certs and not retried_with_certs and _is_cert_error(output):
            print("[bootstrap] TLS certificate error detected during pip install.")
            if ensure_company_certs(verbose=verbose):
                retried_with_certs = True
                print("[bootstrap] Retrying bootstrap with freshly exported certificates.")
                attempt += 1
                continue
            else:
                print("[bootstrap] Automatic certificate export failed; will fall back to original error.")

        raise SystemExit(
            f"Failed to bootstrap resources for '{source}'. "
            f"Docker exited with {returncode}. Output:\n{output}"
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
    stage_window_lines: int | None = None,
) -> None:
    try:
        sources = PROFILE_SOURCES[profile]
    except KeyError as exc:
        raise SystemExit(f"Unknown profile '{profile}'. Available: {', '.join(PROFILE_SOURCES)}") from exc

    print(f"Installing pythonfmu resources for profile: {profile}")

    if not dry_run:
        CREATE_FMU_ROOT.mkdir(parents=True, exist_ok=True)
        ARTIFACTS_ROOT.mkdir(parents=True, exist_ok=True)
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
                bootstrap_source(
                    source_name,
                    verbose=verbose,
                    auto_certs=auto_certs,
                    stage_window_lines=stage_window_lines,
                )
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
    parser.add_argument(
        "--stage-window-lines",
        type=int,
        default=None,
        help=(
            "Number of log lines to retain in the live stage window. "
            "Defaults to 6 (50 when --verbose). Use 0 for unlimited."
        ),
    )
    args = parser.parse_args()

    profile = args.profile or detect_profile()
    install(
        profile,
        dry_run=args.dry_run,
        allow_bootstrap=not args.no_bootstrap,
        verbose=args.verbose,
        auto_certs=not args.no_auto_certs,
        stage_window_lines=args.stage_window_lines,
    )


if __name__ == "__main__":
    main()
