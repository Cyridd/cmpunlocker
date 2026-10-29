#!/usr/bin/env bash
# Build the patcher and generate a local patched libnvidia-glcore.so.
#
# Usage: ./build_patched_lib.sh [--driver-version=VER] [ARGUMENT [SOURCE_LIB]]
#   ARGUMENT    MME loop count to emit instead of 240 (default 0, recommended).
#   SOURCE_LIB  original library; its version determines the output filename.
#   --driver-version=VER  Select an installed userspace release explicitly.
#
# Without an explicit source/version, use the loaded NVIDIA driver's version.
# If no driver is loaded, select the sole installed 64-bit glcore version;
# multiple installed versions require an explicit selection.
#
# Environment:
#   GLCORE_SRC      Same as SOURCE_LIB (positional source takes precedence).
#   NVIDIA_VERSION  Same as --driver-version (CLI takes precedence).
#   CXX             C++ compiler (default: g++).
#
# Root is not needed. The original library is read and a local copy is written.
# Compatibility is checked by ELF architecture and the binary command signature,
# not a list of allowed driver versions. Use the result through LD_LIBRARY_PATH.
set -euo pipefail

CXX="${CXX:-g++}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSION="${NVIDIA_VERSION:-}"

usage() { awk 'NR>1 { if (/^#/) { sub(/^# ?/, ""); print } else exit }' "${BASH_SOURCE[0]}"; }

SRC="${GLCORE_SRC:-}"
positional=()
while (( $# )); do
    arg="$1"
    case "$arg" in
        -h|--help)
            usage; exit 0 ;;
        --driver-version=*)
            VERSION="${arg#--driver-version=}"
            [[ -n "$VERSION" ]] || { echo "ERROR: --driver-version requires a value." >&2; exit 2; } ;;
        --driver-version)
            (( $# >= 2 )) || { echo "ERROR: --driver-version requires a value." >&2; exit 2; }
            VERSION="$2"; shift ;;
        --*)
            echo "ERROR: unknown option: $arg" >&2; exit 2 ;;
        [A-Za-z_]*=*)
            echo "ERROR: '$arg' looks like an environment variable, not an argument." >&2
            echo "       Put it in front of the command:  $arg ./build_patched_lib.sh" >&2
            echo "       or pass the path directly:       ./build_patched_lib.sh 0 /path/to/libnvidia-glcore.so.VER" >&2
            exit 2 ;;
        *)
            positional+=("$arg") ;;
    esac
    shift
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

if [[ -n "$VERSION" && ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "ERROR: invalid driver version: $VERSION" >&2
    exit 2
fi

SEARCH_DIRS=(/usr/lib /usr/lib64 /usr/lib/x86_64-linux-gnu)
# Resolve the installed release rather than choosing the newest stale library.
if [[ -z "$SRC" && -z "$VERSION" ]]; then
    if [[ -r /proc/driver/nvidia/version ]]; then
        VERSION="$(sed -nE '/^NVRM version:/s/.* ([0-9]+\.[0-9]+\.[0-9]+) .*/\1/p' /proc/driver/nvidia/version)"
    fi
    if [[ -z "$VERSION" ]]; then
        versions=()
        shopt -s nullglob
        for search_dir in "${SEARCH_DIRS[@]}"; do
            for candidate in "$search_dir"/libnvidia-glcore.so.*; do
                [[ -f "$candidate" ]] || continue
                name="$(basename "$(readlink -f "$candidate")")"
                if [[ "$name" =~ ^libnvidia-glcore\.so\.([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
                    candidate_version="${BASH_REMATCH[1]}"
                    # Ignore any 32-bit libraries reached through symlinks.
                    elf_class="$(od -An -tu1 -j4 -N1 "$candidate" | tr -d '[:space:]')"
                    [[ "$elf_class" == 2 ]] || continue
                    if [[ " ${versions[*]} " != *" $candidate_version "* ]]; then
                        versions+=("$candidate_version")
                    fi
                fi
            done
        done
        shopt -u nullglob
        if (( ${#versions[@]} != 1 )); then
            echo "ERROR: cannot select the NVIDIA userspace version (found: ${versions[*]:-none})." >&2
            echo "       Pass --driver-version=VER or an original SOURCE_LIB path." >&2
            exit 1
        fi
        VERSION="${versions[0]}"
    fi
fi

if [[ -z "$SRC" ]]; then
    LIB="libnvidia-glcore.so.${VERSION}"
    for search_dir in "${SEARCH_DIRS[@]}"; do
        candidate="$search_dir/$LIB"
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
SRC="$(readlink -f "$SRC")"
name="$(basename "$SRC")"
if [[ ! "$name" =~ ^libnvidia-glcore\.so\.([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
    # An explicitly supplied library may have been renamed. Preserve its SONAME.
    command -v readelf >/dev/null 2>&1 || { echo "ERROR: readelf is needed to identify a renamed library." >&2; exit 1; }
    name="$(LC_ALL=C readelf -d "$SRC" | sed -nE 's/.*\(SONAME\).*\[([^]]+)\].*/\1/p')"
fi
if [[ ! "$name" =~ ^libnvidia-glcore\.so\.([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
    echo "ERROR: cannot determine the glcore release from the filename or ELF SONAME." >&2
    exit 1
fi
source_version="${BASH_REMATCH[1]}"
if [[ -n "$VERSION" && "$VERSION" != "$source_version" ]]; then
    echo "ERROR: requested $VERSION but the source library is $source_version." >&2
    exit 1
fi
VERSION="$source_version"
LIB="libnvidia-glcore.so.${VERSION}"
OUT="$DIR/$LIB"
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

echo "NVIDIA userspace version: $VERSION"
echo "[1/2] Building patcher with $CXX"
"$CXX" -std=c++17 -O2 -Wall -Wextra "$DIR/patch_glcore.cpp" -o "$DIR/patch_glcore"

echo "[2/2] Patching a copy of $SRC (argument $ARGUMENT)"
if ! "$DIR/patch_glcore" "$SRC" "$OUT" "$ARGUMENT"; then
    echo >&2
    echo "The patcher needs the original, unmodified ${LIB}." >&2
    echo "An already patched library or a release with a different emitter is rejected." >&2
    exit 1
fi

echo
echo "Done: $OUT"
echo "Use it per application, for example in Steam launch options:"
echo "  LD_LIBRARY_PATH=$DIR:\$LD_LIBRARY_PATH %command%"
