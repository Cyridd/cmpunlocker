#!/usr/bin/env bash
# Build the patcher and generate a local patched libnvidia-glcore.so.
#
# Usage: ./build_patched_lib.sh [ARGUMENT [SOURCE_LIB]]
#   ARGUMENT    MME loop count to emit instead of 240 (default 0, recommended).
#   SOURCE_LIB  path to the ORIGINAL libnvidia-glcore.so.<version> to patch.
#               Default: search /usr/lib, /usr/lib64, /usr/lib/x86_64-linux-gnu.
#
# Environment (set it in front of the command, not as an argument):
#   GLCORE_SRC  same as SOURCE_LIB, for example
#                 GLCORE_SRC=/path/to/libnvidia-glcore.so.610.57.04 ./build_patched_lib.sh
#   CXX         C++ compiler (default: g++).
#
# Root is not needed: the source library is only read, never modified, and the
# result is written next to this script. Use it through LD_LIBRARY_PATH.
set -euo pipefail

VERSION="610.57.04"
CXX="${CXX:-g++}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB="libnvidia-glcore.so.${VERSION}"
OUT="$DIR/$LIB"

usage() { awk 'NR>1 { if (/^#/) { sub(/^# ?/, ""); print } else exit }' "${BASH_SOURCE[0]}"; }

SRC="${GLCORE_SRC:-}"
positional=()
for arg in "$@"; do
    case "$arg" in
        -h|--help)
            usage; exit 0 ;;
        [A-Za-z_]*=*)
            echo "ERROR: '$arg' looks like an environment variable, not an argument." >&2
            echo "       Put it in front of the command:  $arg ./build_patched_lib.sh" >&2
            echo "       or pass the path directly:       ./build_patched_lib.sh 0 /path/to/${LIB}" >&2
            exit 2 ;;
        *)
            positional+=("$arg") ;;
    esac
done
if (( ${#positional[@]} > 2 )); then
    echo "ERROR: too many arguments." >&2; usage >&2; exit 2
fi
ARGUMENT="${positional[0]:-0}"
if (( ${#positional[@]} == 2 )); then SRC="${positional[1]}"; fi

if ! [[ "$ARGUMENT" =~ ^(0[xX][0-9a-fA-F]+|[0-9]+)$ ]]; then
    echo "ERROR: ARGUMENT must be a non-negative integer (decimal or 0x hex), got '$ARGUMENT'" >&2
    exit 2
fi

if [[ -z "$SRC" ]]; then
    for candidate in \
        "/usr/lib/${LIB}" \
        "/usr/lib64/${LIB}" \
        "/usr/lib/x86_64-linux-gnu/${LIB}"; do
        if [[ -f "$candidate" ]]; then SRC="$candidate"; break; fi
    done
fi
if [[ -z "$SRC" ]]; then
    echo "ERROR: ${LIB} not found in the default locations." >&2
    echo "       Install the NVIDIA ${VERSION} userspace package, or pass the path:" >&2
    echo "         ./build_patched_lib.sh ${ARGUMENT} /path/to/${LIB}" >&2
    exit 1
fi
if [[ ! -f "$SRC" ]]; then
    echo "ERROR: source library not found: $SRC" >&2
    exit 1
fi
if [[ -e "$OUT" && "$(readlink -f "$SRC")" == "$(readlink -f "$OUT")" ]]; then
    echo "ERROR: the source is the output file itself ($OUT)." >&2
    echo "       Use the original system library as the source, not a patched copy." >&2
    exit 1
fi

if ! command -v "$CXX" >/dev/null 2>&1; then
    echo "ERROR: C++ compiler '$CXX' not found (install g++ or set CXX)" >&2
    exit 1
fi
if [[ ${EUID} -eq 0 ]]; then
    echo "note: root is not required; the generated files will be owned by root." >&2
fi

echo "[1/2] Building patcher with $CXX"
"$CXX" -std=c++17 -O2 -Wall -Wextra "$DIR/patch_glcore.cpp" -o "$DIR/patch_glcore"

echo "[2/2] Patching a copy of $SRC (argument $ARGUMENT)"
if ! "$DIR/patch_glcore" "$SRC" "$OUT" "$ARGUMENT"; then
    echo >&2
    echo "The patcher needs the original, unmodified ${LIB}." >&2
    echo "If this copy was patched before, restore the original by reinstalling the" >&2
    echo "NVIDIA userspace package (for example nvidia-utils) and run this script again." >&2
    exit 1
fi

echo
echo "Done: $OUT"
echo "Use it per application, for example in Steam launch options:"
echo "  LD_LIBRARY_PATH=$DIR:\$LD_LIBRARY_PATH %command%"
