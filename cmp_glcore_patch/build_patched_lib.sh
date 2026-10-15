#!/usr/bin/env bash
# Build the patcher and generate a local patched libnvidia-glcore.so.
#
# Usage: ./build_patched_lib.sh [ARGUMENT]
#   ARGUMENT  MME loop count to emit instead of 240 (default 0, recommended).
#
# Environment:
#   GLCORE_SRC  path to the installed libnvidia-glcore.so.<version> to patch.
#   CXX         C++ compiler (default: g++).
#
# The system library is only read, never modified. The result is written next
# to this script and is meant to be used through LD_LIBRARY_PATH.
set -euo pipefail

VERSION="610.57.04"
ARGUMENT="${1:-0}"
CXX="${CXX:-g++}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB="libnvidia-glcore.so.${VERSION}"

if ! [[ "$ARGUMENT" =~ ^(0[xX][0-9a-fA-F]+|[0-9]+)$ ]]; then
    echo "ERROR: ARGUMENT must be a non-negative integer (decimal or 0x hex), got '$ARGUMENT'" >&2
    exit 2
fi

SRC="${GLCORE_SRC:-}"
if [[ -z "$SRC" ]]; then
    for candidate in \
        "/usr/lib/${LIB}" \
        "/usr/lib64/${LIB}" \
        "/usr/lib/x86_64-linux-gnu/${LIB}"; do
        if [[ -f "$candidate" ]]; then SRC="$candidate"; break; fi
    done
fi
if [[ -z "$SRC" || ! -f "$SRC" ]]; then
    echo "ERROR: ${LIB} not found. Install NVIDIA ${VERSION} userspace, or set GLCORE_SRC=/path/to/${LIB}" >&2
    exit 1
fi

if ! command -v "$CXX" >/dev/null 2>&1; then
    echo "ERROR: C++ compiler '$CXX' not found (install g++ or set CXX)" >&2
    exit 1
fi

echo "[1/2] Building patcher with $CXX"
"$CXX" -std=c++17 -O2 -Wall -Wextra "$DIR/patch_glcore.cpp" -o "$DIR/patch_glcore"

echo "[2/2] Patching a copy of $SRC (argument $ARGUMENT)"
"$DIR/patch_glcore" "$SRC" "$DIR/$LIB" "$ARGUMENT"

echo
echo "Done: $DIR/$LIB"
echo "Use it per application, for example in Steam launch options:"
echo "  LD_LIBRARY_PATH=$DIR:\$LD_LIBRARY_PATH %command%"
