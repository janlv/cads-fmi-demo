#!/usr/bin/env python3
"""
Export organisation certificate authorities into the repo's ``certs/`` folder.

Usage examples:
    scripts/export_company_certs.py
    scripts/export_company_certs.py --platform mac
    scripts/export_company_certs.py --probe-host private-registry.local:8443
    scripts/export_company_certs.py --dest certs

Mac mode uses the ``security`` CLI to pull certificates from the provided
keychains (System + login by default). Linux mode copies matching ``*.crt`` or
``*.pem`` files from ``/usr/local/share/ca-certificates`` (override with
``--linux-source``). Certificates are written as individual PEM files named
after their SHA1 fingerprint so they can be dropped straight into the Docker
build context.
"""

from __future__ import annotations

import argparse
import platform
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_DEST = ROOT / "certs"


def run_checked(cmd: list[str]) -> bytes:
    proc = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
    if proc.returncode != 0:
        raise RuntimeError(f"Command {' '.join(cmd)} failed with exit code {proc.returncode}:\n{proc.stderr.decode()}")
    return proc.stdout


def split_pems(data: bytes) -> list[str]:
    current: list[str] = []
    blocks: list[str] = []
    for line in data.decode().splitlines():
        current.append(line)
        if line.strip() == "-----END CERTIFICATE-----":
            blocks.append("\n".join(current) + "\n")
            current = []
    if current:
        # Incomplete PEM chunk
        blocks.append("\n".join(current) + "\n")
    return blocks


def fingerprint_pem(pem: str) -> tuple[str, str]:
    with tempfile.NamedTemporaryFile("w", delete=False) as tmp:
        tmp.write(pem)
        tmp.flush()
        tmp_path = tmp.name
    try:
        subject = run_checked(["openssl", "x509", "-in", tmp_path, "-noout", "-subject", "-nameopt", "RFC2253"]).decode().strip()
        fp = run_checked(["openssl", "x509", "-in", tmp_path, "-noout", "-fingerprint", "-sha1"]).decode().strip()
    finally:
        Path(tmp_path).unlink(missing_ok=True)

    # subject=..., fingerprint=SHA1 Fingerprint=AB:CD...
    subject = subject.partition("=")[2] if "=" in subject else subject
    sha1 = fp.partition("=")[2] if "=" in fp else fp
    return subject, sha1.replace(":", "").replace(" ", "").lower()


def export_mac(dest: Path, keychains: list[str], seen: set[str]) -> int:
    exported = 0
    for keychain in keychains:
        kc_path = Path(keychain).expanduser()
        if not kc_path.exists():
            continue
        data = run_checked(["security", "find-certificate", "-a", "-p", str(kc_path)])
        for pem in split_pems(data):
            if "BEGIN CERTIFICATE" not in pem:
                continue
            subject, fingerprint = fingerprint_pem(pem)
            if fingerprint in seen:
                continue
            seen.add(fingerprint)
            filename = dest / f"company-{fingerprint}.crt"
            filename.write_text(pem)
            print(f"[mac] exported {subject} -> {filename}")
            exported += 1
    return exported


def export_linux(dest: Path, source_dir: Path, seen: set[str]) -> int:
    exported = 0
    if not source_dir.exists():
        raise FileNotFoundError(f"Linux source directory '{source_dir}' does not exist")
    candidates = list(source_dir.rglob("*.crt")) + list(source_dir.rglob("*.pem"))
    for path in candidates:
        pem = path.read_text()
        subject, fingerprint = fingerprint_pem(pem)
        if fingerprint in seen:
            continue
        seen.add(fingerprint)
        filename = dest / f"company-{fingerprint}.crt"
        shutil.copy2(path, filename)
        print(f"[linux] copied {path} ({subject}) -> {filename}")
        exported += 1
    return exported


def export_probe(dest: Path, hosts: list[str], seen: set[str]) -> int:
    exported = 0
    for host in hosts:
        host_part, _, port_part = host.partition(":")
        host_part = host_part.strip()
        port = port_part.strip() or "443"
        if not host_part:
            continue
        cmd = [
            "openssl",
            "s_client",
            "-showcerts",
            "-servername",
            host_part,
            "-connect",
            f"{host_part}:{port}",
            "-brief",
        ]
        proc = subprocess.run(cmd, input=b"\n", stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=False)
        output = proc.stdout + proc.stderr
        if proc.returncode != 0:
            print(f"[probe] openssl exited with {proc.returncode} for {host}. Parsing captured output.")
        blocks = split_pems(output)
        if not blocks:
            print(f"[probe] no certificates captured from {host}", file=sys.stderr)
            continue
        for idx, pem in enumerate(blocks, start=1):
            if "BEGIN CERTIFICATE" not in pem:
                continue
            subject, fingerprint = fingerprint_pem(pem)
            if fingerprint in seen:
                continue
            seen.add(fingerprint)
            filename = dest / f"company-{fingerprint}.crt"
            filename.write_text(pem)
            print(f"[probe] exported {subject} (#{idx} from {host}) -> {filename}")
            exported += 1
    return exported


def detect_platform() -> str:
    system = platform.system().lower()
    if system == "darwin":
        return "mac"
    if system == "linux":
        return "linux"
    raise SystemExit(f"Unsupported platform '{system}'. Please specify --platform.")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dest", type=Path, default=DEFAULT_DEST, help="Output directory (default: ./certs)")
    parser.add_argument("--platform", choices=["auto", "mac", "linux"], default="auto",
                        help="Force a specific platform export (default: auto-detect)")
    parser.add_argument("--mac-keychain", dest="mac_keychains", action="append",
                        help="Additional macOS keychain path to scan (default: system + login)")
    parser.add_argument("--linux-source", type=Path, default=Path("/usr/local/share/ca-certificates"),
                        help="Directory to scan for corporate certs on Linux (default: /usr/local/share/ca-certificates)")
    parser.add_argument("--probe-host", dest="probe_hosts", action="append", default=[],
                        help="Connect to host:port with openssl s_client and capture presented certificates")
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    dest = args.dest
    dest.mkdir(parents=True, exist_ok=True)
    platform_name = detect_platform() if args.platform == "auto" else args.platform

    seen: set[str] = set()

    if platform_name == "mac":
        keychains = [
            "/Library/Keychains/System.keychain",
            str(Path.home() / "Library/Keychains/login.keychain-db"),
        ]
        if args.mac_keychains:
            keychains.extend(args.mac_keychains)
        exported = export_mac(dest, keychains, seen)
    else:
        exported = export_linux(dest, args.linux_source, seen)

    probe_targets = args.probe_hosts or ["pypi.org"]
    exported += export_probe(dest, probe_targets, seen)

    if exported == 0:
        print("No certificates exported. Try adding --subject filters or verifying your source paths.", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
