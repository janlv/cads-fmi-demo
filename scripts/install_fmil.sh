#!/usr/bin/env bash
set -euo pipefail

DEFAULT_PREFIX="${FMIL_HOME:-$HOME/fmil}"
REPO_URL=${FMIL_REPO_URL:-"https://github.com/modelon-community/fmi-library.git"}
REPO_REF=${FMIL_REPO_REF:-"main"}

usage() {
    cat <<'EOF'
Usage: scripts/install_fmil.sh [--prefix /path/to/install] [--ref <git-ref>] [--force]

Downloads, builds, and installs FMIL (fmilib) if it is not already available
under the requested prefix. Defaults:

  --prefix    $HOME/fmil  (or the current FMIL_HOME if set)
  --ref       main        (override via FMIL_REPO_REF env var)

Examples:
  scripts/install_fmil.sh
  scripts/install_fmil.sh --prefix "$HOME/.local/fmil" --ref v2.4
EOF
}

PREFIX="$DEFAULT_PREFIX"
REF="$REPO_REF"
FORCE=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        -h|--help)
            usage
            exit 0
            ;;
        --prefix)
            shift
            PREFIX="${1:-}"
            if [[ -z "$PREFIX" ]]; then
                echo "[error] --prefix expects a path" >&2
                exit 1
            fi
            ;;
        --ref)
            shift
            REF="${1:-}"
            if [[ -z "$REF" ]]; then
                echo "[error] --ref expects a git reference" >&2
                exit 1
            fi
            ;;
        --force)
            FORCE=true
            ;;
        *)
            echo "[error] Unknown argument: $1" >&2
            usage
            exit 1
            ;;
    esac
    shift || true
done

have_fmil() {
    [[ -d "$PREFIX/include/FMI" ]] && [[ -f "$PREFIX/lib/libfmilib_shared.so" ]]
}

if have_fmil && [[ "$FORCE" != true ]]; then
    echo "[fmil] Reusing existing installation at $PREFIX"
    exit 0
fi

echo "[fmil] Installing FMIL into $PREFIX (ref: $REF)"
mkdir -p "$PREFIX"

WORKDIR="$(mktemp -d)"
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT

SRC_DIR="$WORKDIR/src"
BUILD_DIR="$WORKDIR/build"

echo "[fmil] Cloning $REPO_URL ($REF)"
git clone --depth 1 --branch "$REF" "$REPO_URL" "$SRC_DIR"

cmake_args=(
    -S "$SRC_DIR"
    -B "$BUILD_DIR"
    -DCMAKE_BUILD_TYPE=Release
    "-DCMAKE_INSTALL_PREFIX=$PREFIX"
)

echo "[fmil] Configuring via CMake"
cmake "${cmake_args[@]}"

if command -v nproc >/dev/null 2>&1; then
    JOBS="$(nproc)"
elif command -v sysctl >/dev/null 2>&1; then
    JOBS="$(sysctl -n hw.ncpu)"
else
    JOBS=4
fi

echo "[fmil] Building (jobs: $JOBS)"
cmake --build "$BUILD_DIR" -j"$JOBS"

echo "[fmil] Installing"
cmake --install "$BUILD_DIR"

echo "[fmil] Installed fmilib to $PREFIX"
echo "       Remember to run ./build.sh --fmil-home \"$PREFIX\" or export FMIL_HOME accordingly."
