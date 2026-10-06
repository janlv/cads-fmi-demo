#!/usr/bin/env python3
"""
Ensure the pythonfmu/pythonfmu3 exporters link against libpython when building
FMU binaries.

pythonfmu (FMI 2.0) and pythonfmu3 (FMI 3.0) ship a generic exporter library
that gets embedded into every FMU. The upstream CMake defaults build the
library like a normal CPython extension, which leaves symbols such as
`_Py_NoneStruct` unresolved unless the library is loaded by an already-running
Python interpreter. Our Go/FMIL runner loads FMUs directly, so the embedded
exporter must link against libpython itself.

This script patches the installed packages in-place. For each package it:
  * requests the `Development.Embed` component from CMake's FindPython3 module
  * links the exporter target with `Python3::Python`
  * (pythonfmu3 only) names the output folder after the real CPU architecture
    (`aarch64-linux` on arm64 instead of the hardcoded `x86_64-linux`)

Use `--package {pythonfmu,pythonfmu3,all}` to select packages (default `all`;
packages that are not installed are skipped). It is safe to run multiple
times; later invocations detect that the changes are already applied and exit
quietly.
"""

from __future__ import annotations

import argparse
import importlib
import sys
from pathlib import Path
from typing import Optional


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


# Each entry is (file relative to <package>/pythonfmu-export, needle, replacement).
PATCHES = {
    "pythonfmu": [
        (
            "CMakeLists.txt",
            "  find_package(Python3 REQUIRED COMPONENTS Development.SABIModule)\n"
            "  add_library (Python3::Module ALIAS Python3::SABIModule)\n",
            "  find_package(Python3 REQUIRED COMPONENTS Development.SABIModule Development.Embed)\n"
            "  add_library (Python3::Module ALIAS Python3::SABIModule)\n",
        ),
        (
            "CMakeLists.txt",
            'option (USE_PYTHON_SABI "Use Python stable ABI" ON)\n',
            'option (USE_PYTHON_SABI "Use Python stable ABI" OFF)\n',
        ),
        (
            "CMakeLists.txt",
            "  find_package(Python3 REQUIRED COMPONENTS Development.Module)\n",
            "  find_package(Python3 REQUIRED COMPONENTS Development.Module Development.Embed)\n",
        ),
        (
            "src/CMakeLists.txt",
            "target_link_libraries (pythonfmu-export PRIVATE Python3::Module)\n",
            "target_link_libraries (pythonfmu-export PRIVATE Python3::Module Python3::Python)\n",
        ),
    ],
    "pythonfmu3": [
        (
            "CMakeLists.txt",
            "find_package(Python3 REQUIRED COMPONENTS Interpreter Development.Module)\n",
            "find_package(Python3 REQUIRED COMPONENTS Interpreter Development.Module Development.Embed)\n",
        ),
        (
            "CMakeLists.txt",
            'if ("${CMAKE_SIZEOF_VOID_P}" STREQUAL "8")\n'
            "  set(TARGET_PLATFORM x86_64-${TARGET_PLATFORM})\n",
            'if ("${CMAKE_SIZEOF_VOID_P}" STREQUAL "8")\n'
            '  if (CMAKE_SYSTEM_PROCESSOR MATCHES "^(aarch64|arm64|ARM64)$")\n'
            "    set(TARGET_PLATFORM aarch64-${TARGET_PLATFORM})\n"
            "  else ()\n"
            "    set(TARGET_PLATFORM x86_64-${TARGET_PLATFORM})\n"
            "  endif ()\n",
        ),
        (
            "src/CMakeLists.txt",
            "elseif (APPLE)\n"
            '  set_target_properties(pythonfmu-export PROPERTIES LINK_FLAGS "-undefined dynamic_lookup")\n'
            "endif ()\n",
            "elseif (APPLE)\n"
            '  set_target_properties(pythonfmu-export PROPERTIES LINK_FLAGS "-undefined dynamic_lookup")\n'
            "endif ()\n"
            "if (UNIX AND NOT APPLE)\n"
            "  target_link_libraries(pythonfmu-export PRIVATE Python3::Python)\n"
            "endif ()\n",
        ),
    ],
}


def locate_package(name: str) -> Optional[Path]:
    try:
        mod = importlib.import_module(name)
    except ImportError:
        return None
    return Path(mod.__file__).resolve().parent


def patch_package(name: str, root: Path) -> bool:
    export_dir = root / "pythonfmu-export"
    for rel, _, _ in PATCHES[name]:
        if not (export_dir / rel).exists():
            raise FileNotFoundError(f"{name}: pythonfmu-export sources not found under {root}")

    changed = False
    for rel, needle, replacement in PATCHES[name]:
        changed |= apply_once(export_dir / rel, needle, replacement)

    if changed:
        print(f"[patch] Applied {name}-export embedding fix under {root}")
    else:
        print(f"[patch] {name}-export already patched under {root}")
    return changed


def patch_exporter() -> int:
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    parser.add_argument(
        "--package",
        choices=["pythonfmu", "pythonfmu3", "all"],
        default=None,
        help="Package to patch (default: all installed; pythonfmu when only --package-dir is given)",
    )
    parser.add_argument(
        "--package-dir",
        type=Path,
        help="Override the install root of a single package (defaults to the active interpreter)",
    )
    args = parser.parse_args()

    package = args.package
    if package is None:
        package = "pythonfmu" if args.package_dir else "all"
    if args.package_dir and package == "all":
        parser.error("--package-dir requires --package pythonfmu or --package pythonfmu3")

    names = list(PATCHES) if package == "all" else [package]
    patched_any = False
    for name in names:
        root = args.package_dir if args.package_dir else locate_package(name)
        if root is None:
            if package == "all":
                print(f"[patch] {name} is not installed in this interpreter; skipping")
                continue
            parser.error(f"{name} is not installed in this interpreter")
        try:
            patch_package(name, root)
        except (FileNotFoundError, ValueError) as exc:
            parser.error(str(exc))
        patched_any = True

    if not patched_any:
        parser.error("neither pythonfmu nor pythonfmu3 is installed in this interpreter")
    return 0


if __name__ == "__main__":
    sys.exit(patch_exporter())
