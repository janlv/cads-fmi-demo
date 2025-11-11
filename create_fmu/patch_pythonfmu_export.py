#!/usr/bin/env python3
"""
Ensure pythonfmu-export links against libpython when building FMU binaries.

pythonfmu ships a generic exporter library that gets embedded into every FMU.
The upstream CMake defaults build the library like a normal CPython extension,
which leaves symbols such as `_Py_NoneStruct` unresolved unless the library is
loaded by an already-running Python interpreter. Our Go/FMIL runner loads FMUs
directly, so the embedded exporter must link against libpython itself.

This script patches the installed pythonfmu package in-place by:
  * requesting the `Development.Embed` component from CMake’s FindPython3 module
  * linking the exporter target with `Python3::Python`

It is safe to run multiple times; later invocations detect that the changes
are already applied and exit quietly.
"""

from __future__ import annotations

import argparse
import importlib
import sys
from pathlib import Path


def apply_once(path: Path, needle: str, replacement: str) -> bool:
    """
    Replace a snippet exactly once. Returns True if the file changed.

    Raises a ValueError if the snippet is missing and the replacement has not
    already been applied (to guard against upstream file layout changes).
    """

    contents = path.read_text()
    if replacement in contents:
        return False
    if needle not in contents:
        raise ValueError(f"Unable to find expected snippet in {path}")
    path.write_text(contents.replace(needle, replacement, 1))
    return True


def patch_exporter() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--package-dir",
        type=Path,
        help="Override pythonfmu install root (defaults to the active interpreter)",
    )
    args = parser.parse_args()

    if args.package_dir:
        root = args.package_dir
    else:
        try:
            mod = importlib.import_module("pythonfmu")
        except ImportError as exc:
            parser.error(f"pythonfmu is not installed in this interpreter: {exc}")
        root = Path(mod.__file__).resolve().parent

    top_level = root / "pythonfmu-export" / "CMakeLists.txt"
    src_level = root / "pythonfmu-export" / "src" / "CMakeLists.txt"

    if not top_level.exists() or not src_level.exists():
        parser.error(f"pythonfmu-export sources not found under {root}")

    changed = False

    changed |= apply_once(
        top_level,
        "  find_package(Python3 REQUIRED COMPONENTS Development.SABIModule)\n"
        "  add_library (Python3::Module ALIAS Python3::SABIModule)\n",
        "  find_package(Python3 REQUIRED COMPONENTS Development.SABIModule Development.Embed)\n"
        "  add_library (Python3::Module ALIAS Python3::SABIModule)\n",
    )

    changed |= apply_once(
        top_level,
        'option (USE_PYTHON_SABI "Use Python stable ABI" ON)\n',
        'option (USE_PYTHON_SABI "Use Python stable ABI" OFF)\n',
    )

    changed |= apply_once(
        top_level,
        "  find_package(Python3 REQUIRED COMPONENTS Development.Module)\n",
        "  find_package(Python3 REQUIRED COMPONENTS Development.Module Development.Embed)\n",
    )

    changed |= apply_once(
        src_level,
        "target_link_libraries (pythonfmu-export PRIVATE Python3::Module)\n",
        "target_link_libraries (pythonfmu-export PRIVATE Python3::Module Python3::Python)\n",
    )

    if changed:
        print(f"[patch] Applied pythonfmu-export embedding fix under {root}")
    else:
        print(f"[patch] pythonfmu-export already patched under {root}")
    return 0


if __name__ == "__main__":
    sys.exit(patch_exporter())
