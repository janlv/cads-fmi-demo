#!/usr/bin/env python3
"""
Install architecture-specific pythonfmu resource bundles.

Copies staged resources from platform_resources/<profile>/pythonfmu_resources
into the root-level pythonfmu_resources/ directory (which is ignored from git).
Profiles can layer multiple bundles (e.g. apple = linux + apple overlay).
"""

from __future__ import annotations

import argparse
import platform
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PLATFORM_RESOURCES = ROOT / "platform_resources"
TARGET_DIR = ROOT / "pythonfmu_resources"

PROFILE_SOURCES = {
    "linux": ["linux"],
    "apple": ["linux", "apple"],
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


def install(profile: str, *, dry_run: bool = False) -> None:
    try:
        sources = PROFILE_SOURCES[profile]
    except KeyError as exc:
        raise SystemExit(f"Unknown profile '{profile}'. Available: {', '.join(PROFILE_SOURCES)}") from exc

    print(f"Installing pythonfmu resources for profile: {profile}")

    if TARGET_DIR.exists():
        print(f"- Removing existing {TARGET_DIR}")
        if not dry_run:
            shutil.rmtree(TARGET_DIR)
    if not dry_run:
        TARGET_DIR.mkdir(parents=True, exist_ok=True)

    for source_name in sources:
        src = PLATFORM_RESOURCES / source_name / "pythonfmu_resources"
        if not src.exists():
            raise SystemExit(f"Source directory missing: {src}")
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
    args = parser.parse_args()

    profile = args.profile or detect_profile()
    install(profile, dry_run=args.dry_run)


if __name__ == "__main__":
    main()
