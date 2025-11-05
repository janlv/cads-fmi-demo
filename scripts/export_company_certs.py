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
from dataclasses import dataclass
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEFAULT_DEST = ROOT / "certs"


@dataclass
class CertificateInfo:
    subject: str
    issuer: str
    fingerprint: str


@dataclass
class ProbeResult:
    exported: int
    fingerprints: set[str]
    names: set[str]


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


def normalize_dn(name: str) -> str:
    return name.strip().lower()


def fingerprint_pem(pem: str) -> CertificateInfo:
    with tempfile.NamedTemporaryFile("w", delete=False) as tmp:
        tmp.write(pem)
        tmp.flush()
        tmp_path = tmp.name
    try:
        subject = run_checked(["openssl", "x509", "-in", tmp_path, "-noout", "-subject", "-nameopt", "RFC2253"]).decode().strip()
        issuer = run_checked(["openssl", "x509", "-in", tmp_path, "-noout", "-issuer", "-nameopt", "RFC2253"]).decode().strip()
        fp = run_checked(["openssl", "x509", "-in", tmp_path, "-noout", "-fingerprint", "-sha1"]).decode().strip()
    finally:
        Path(tmp_path).unlink(missing_ok=True)

    # subject=..., fingerprint=SHA1 Fingerprint=AB:CD...
    subject = subject.partition("=")[2] if "=" in subject else subject
    issuer = issuer.partition("=")[2] if "=" in issuer else issuer
    sha1 = fp.partition("=")[2] if "=" in fp else fp
    return CertificateInfo(
        subject=subject,
        issuer=issuer,
        fingerprint=sha1.replace(":", "").replace(" ", "").lower(),
    )


def should_include(cert: CertificateInfo, allowed_subjects: set[str] | None, allowed_fingerprints: set[str] | None) -> bool:
    if allowed_fingerprints and cert.fingerprint in allowed_fingerprints:
        return True
    if allowed_subjects:
        subject_key = normalize_dn(cert.subject) if cert.subject else ""
        issuer_key = normalize_dn(cert.issuer) if cert.issuer else ""
        if subject_key in allowed_subjects or (issuer_key and issuer_key in allowed_subjects):
            return True
        return False
    return not allowed_subjects and not allowed_fingerprints


def export_mac(dest: Path, keychains: list[str], seen: set[str], allowed_subjects: set[str] | None = None,
               allowed_fingerprints: set[str] | None = None) -> int:
    exported = 0
    for keychain in keychains:
        kc_path = Path(keychain).expanduser()
        if not kc_path.exists():
            continue
        data = run_checked(["security", "find-certificate", "-a", "-p", str(kc_path)])
        for pem in split_pems(data):
            if "BEGIN CERTIFICATE" not in pem:
                continue
            cert = fingerprint_pem(pem)
            if not should_include(cert, allowed_subjects, allowed_fingerprints):
                continue
            if cert.fingerprint in seen:
                continue
            seen.add(cert.fingerprint)
            filename = dest / f"company-{cert.fingerprint}.crt"
            filename.write_text(pem)
            print(f"[mac] exported {cert.subject} -> {filename}")
            exported += 1
    return exported


def export_linux(dest: Path, source_dir: Path, seen: set[str], allowed_subjects: set[str] | None = None,
                 allowed_fingerprints: set[str] | None = None) -> int:
    exported = 0
    if not source_dir.exists():
        raise FileNotFoundError(f"Linux source directory '{source_dir}' does not exist")
    candidates = list(source_dir.rglob("*.crt")) + list(source_dir.rglob("*.pem"))
    for path in candidates:
        pem = path.read_text()
        cert = fingerprint_pem(pem)
        if not should_include(cert, allowed_subjects, allowed_fingerprints):
            continue
        if cert.fingerprint in seen:
            continue
        seen.add(cert.fingerprint)
        filename = dest / f"company-{cert.fingerprint}.crt"
        shutil.copy2(path, filename)
        print(f"[linux] copied {path} ({cert.subject}) -> {filename}")
        exported += 1
    return exported


def export_probe(dest: Path, hosts: list[str], seen: set[str]) -> ProbeResult:
    exported = 0
    fingerprints: set[str] = set()
    names: set[str] = set()
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
            cert = fingerprint_pem(pem)
            fingerprints.add(cert.fingerprint)
            for candidate in (cert.subject, cert.issuer):
                if candidate:
                    names.add(candidate)
            if cert.fingerprint in seen:
                continue
            seen.add(cert.fingerprint)
            filename = dest / f"company-{cert.fingerprint}.crt"
            filename.write_text(pem)
            print(f"[probe] exported {cert.subject} (#{idx} from {host}) -> {filename}")
            exported += 1
    return ProbeResult(exported=exported, fingerprints=fingerprints, names=names)


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

    probe_targets = args.probe_hosts or ["pypi.org"]
    probe_result = export_probe(dest, probe_targets, seen)
    allowed_subjects = {normalize_dn(name) for name in probe_result.names if name}
    allowed_fingerprints = set(probe_result.fingerprints)

    if allowed_subjects or allowed_fingerprints:
        msg_subjects = f"{len(allowed_subjects)} subject(s)" if allowed_subjects else "no subjects"
        msg_fp = f"{len(allowed_fingerprints)} fingerprint(s)" if allowed_fingerprints else "no fingerprints"
        print(f"[filter] restricting platform export to {msg_subjects} and {msg_fp} captured from probe.")
    else:
        print("[filter] probe yielded no certificates; exporting all platform certificates.")

    if platform_name == "mac":
        keychains = [
            "/Library/Keychains/System.keychain",
            str(Path.home() / "Library/Keychains/login.keychain-db"),
        ]
        if args.mac_keychains:
            keychains.extend(args.mac_keychains)
        exported = probe_result.exported + export_mac(
            dest,
            keychains,
            seen,
            allowed_subjects or None,
            allowed_fingerprints or None,
        )
    else:
        exported = probe_result.exported + export_linux(
            dest,
            args.linux_source,
            seen,
            allowed_subjects or None,
            allowed_fingerprints or None,
        )

    if exported == 0:
        print("No certificates exported. Try adjusting --probe-host targets or verifying your source paths.", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
