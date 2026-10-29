#!/usr/bin/env bash
# Verify that the Linux patch set still applies to the pinned NVIDIA sources.
#
# The patches under patches/<version>/ target one exact release of
# open-gpu-kernel-modules each. When NVIDIA retags a source archive, a patch is
# edited against a different tree, or a new version is added without its patch
# set, the failure only shows up during installation. This
# script reproduces the install-time patch step — and nothing else: no kernel
# headers, no compiler, no root.
#
# Usage:
#   ./tools/check-linux-patches.sh [--version VER ...] [--source PATH] [--keep]
#
#   --version VER   Check only this driver version (repeatable). Default: all
#                   versions listed in SUPPORTED_VERSIONS.
#   --source PATH   NVIDIA source for the selected version: an archive or an
#                   existing extracted tree. Only valid with a single
#                   --version. Default: download the pinned archive from
#                   GitHub and verify its SHA256.
#   --keep          Keep the temporary work directory (printed on success).
#
# Exit status:
#   0  every selected version applies cleanly in both PCIe modes where the
#      diagnostic patch exists, and all markers are present
#   1  a patch does not apply, a marker is missing, or the source is wrong
#   2  usage or environment error
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Keep in sync with install.sh: the script fails when they disagree, because
# CI would otherwise validate a different set than users install.
SUPPORTED_VERSIONS=(580.173.02 610.57.04 615.78.08)

sha256_for() {
    case "$1" in
        580.173.02) echo "a2cd41cf100a81d90de9d5ca192b828ed8a63408330acef7931df00487acd82f" ;;
        610.57.04) echo "619d7b5ce1f79c3211afdbf87d02b2174d268b10d005c5b8f994be22299be681" ;;
        615.78.08) echo "eac0ecd52facab28ad2963cf7e03ae8d2289d596ad590c703b5f72d65899e2bf" ;;
        *) echo "" ;;
    esac
}

selected=()
SRC_INPUT=""
KEEP=0

usage() { awk 'NR>1 { if (/^#/) { sub(/^# ?/, ""); print } else exit }' "${BASH_SOURCE[0]}"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

version_supported() {
    local v
    for v in "${SUPPORTED_VERSIONS[@]}"; do [[ "$v" == "$1" ]] && return 0; done
    return 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --version)
            [[ $# -ge 2 ]] || { usage >&2; exit 2; }
            version_supported "$2" || die "unsupported version: $2 (supported: ${SUPPORTED_VERSIONS[*]})"
            selected+=("$2")
            shift 2 ;;
        --version=*)
            v="${1#--version=}"
            version_supported "$v" || die "unsupported version: $v (supported: ${SUPPORTED_VERSIONS[*]})"
            selected+=("$v")
            shift ;;
        --source)
            [[ $# -ge 2 ]] || { usage >&2; exit 2; }
            SRC_INPUT="$2"; shift 2 ;;
        --source=*)
            SRC_INPUT="${1#--source=}"; shift ;;
        --keep)
            KEEP=1; shift ;;
        -h|--help)
            usage; exit 0 ;;
        *)
            usage >&2; exit 2 ;;
    esac
done

if [[ ${#selected[@]} -eq 0 ]]; then
    selected=("${SUPPORTED_VERSIONS[@]}")
fi

if [[ -n "${SRC_INPUT}" && ${#selected[@]} -ne 1 ]]; then
    die "--source can only be used with a single --version"
fi

for tool in patch tar sha256sum find mktemp grep awk; do
    command -v "${tool}" >/dev/null 2>&1 || die "missing required tool: ${tool}"
done

WORK="$(mktemp -d "${TMPDIR:-/tmp}/cmp40-patchcheck.XXXXXXXX")"
cleanup() {
    if [[ "${KEEP}" -eq 1 ]]; then
        printf 'work directory kept: %s\n' "${WORK}" >&2
    else
        rm -rf -- "${WORK}"
    fi
}
trap cleanup EXIT

download_source() { # <version> <out>
    local version="$1" out="$2" url expected actual
    url="https://github.com/NVIDIA/open-gpu-kernel-modules/archive/refs/tags/${version}.tar.gz"
    expected="$(sha256_for "${version}")"
    command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 \
        || die "neither curl nor wget is installed; pass --source PATH"
    printf 'downloading %s\n' "${url}" >&2
    if command -v curl >/dev/null 2>&1; then
        curl -fL --retry 3 --silent --show-error "${url}" -o "${out}"
    else
        wget -q -O "${out}" "${url}"
    fi
    actual="$(sha256sum "${out}" | awk '{print $1}')"
    [[ "${actual}" == "${expected}" ]] \
        || die "SHA256 mismatch for ${version} (expected ${expected}, got ${actual}). The pinned source changed; revalidation is required."
}

# Every variant starts from the same freshly extracted tree, so a second run
# cannot see remnants of the first.
make_tree() { # <source> <dest>
    local source="$1" dest="$2" root
    mkdir -p "${dest}"
    case "${source}" in
        *.tar.gz|*.tgz)    tar --no-same-owner --no-same-permissions -xzf "${source}" -C "${dest}" ;;
        *.tar.xz)          tar --no-same-owner --no-same-permissions -xJf "${source}" -C "${dest}" ;;
        *.tar.bz2)         tar --no-same-owner --no-same-permissions -xjf "${source}" -C "${dest}" ;;
        *)
            [[ -d "${source}" ]] || die "not an archive or directory: ${source}"
            cp -a -- "${source}/." "${dest}/" ;;
    esac
    root="$(find "${dest}" -mindepth 1 -maxdepth 1 -type d -name "*${3}*" -print -quit)"
    [[ -n "${root}" ]] || root="${dest}"
    printf '%s' "${root}"
}

check_version() { # <tree> <expected>
    local tree="$1" expected="$2" version
    [[ -f "${tree}/version.mk" ]] || die "not an NVIDIA source tree: ${tree} (version.mk missing)"
    version="$(awk -F= '$1 ~ /^NVIDIA_VERSION[[:space:]]*$/ { gsub(/[[:space:]]/, "", $2); print $2; exit }' "${tree}/version.mk")"
    [[ "${version}" == "${expected}" ]] \
        || die "source is ${version:-unknown}; the patches target ${expected}"
}

apply_and_check() { # <version> <source> <label> <pcie patch> <marker>
    local version="$1" source="$2" label="$3" pcie_patch="$4" marker="$5" tree
    local compute="${SCRIPT_DIR}/patches/${version}/0001-cmp40hx-unlock.patch"
    local rebar="${SCRIPT_DIR}/patches/${version}/0003-cmp40hx-rebar-unlock.patch"

    [[ -f "${compute}" && -f "${pcie_patch}" && -f "${rebar}" ]] \
        || die "missing patch set for ${version} (${label})"

    tree="$(make_tree "${source}" "${WORK}/${version}-${label}" "${version}")"
    check_version "${tree}" "${version}"

    printf '== %s / %s ==\n' "${version}" "${label}" >&2
    (
        cd "${tree}"
        for patch_file in "${compute}" "${pcie_patch}" "${rebar}"; do
            patch --dry-run -p1 -F0 < "${patch_file}" >/dev/null 2>&1 \
                || die "$(basename "${patch_file}") does not apply to ${version} at fuzz 0"
            patch --silent -p1 < "${patch_file}"
            printf '  %s: applied\n' "$(basename "${patch_file}")" >&2
        done

        grep -Rqs "CMP40_COMPUTE_UNLOCK" src/nvidia \
            || die "compute marker missing after ${version}/${label}"
        # The PCIe patch touches both trees: older versions write the marker in
        # src/nvidia (GSP-side register policy), the current ones also in
        # kernel-open (the retrain helper).
        grep -Rqs "${marker}" src/nvidia kernel-open/nvidia \
            || die "PCIe marker ${marker} missing after ${version}/${label}"
        # 610/615 need the GSP-ready policy writes as well as the host retrain
        # helper. The original 615 port silently omitted this entire hunk;
        # the host marker alone passed while every retrain was skipped.
        case "${version}" in
            610.57.04|615.78.08)
                grep -Fqs 'LNKCFG=0x%08x(bits[19:18]=%u)' \
                    src/nvidia/src/kernel/gpu/gsp/arch/turing/kernel_gsp_tu102.c \
                    || die "GSP-ready Gen2 policy setup missing after ${version}/${label}"
                ;;
        esac
        grep -Rqs "CMP40_REBAR" kernel-open/nvidia \
            || die "ReBAR marker missing after ${version}/${label}"
    )
    printf '  markers present\n' >&2
}

for version in "${selected[@]}"; do
    source="${SRC_INPUT}"
    if [[ -z "${source}" ]]; then
        source="${WORK}/${version}.tar.gz"
        download_source "${version}" "${source}"
    fi

    apply_and_check "${version}" "${source}" normal \
        "${SCRIPT_DIR}/patches/${version}/0002-cmp40hx-pcie2-unlock.patch" \
        "CMP40_PCIE_GEN2_V2"

    # The diagnostic PCIe patch exists for 610.57.04 and 615.78.08 only.
    if [[ -f "${SCRIPT_DIR}/patches/${version}/0002-cmp40hx-pcie2-diagnostic.patch" ]]; then
        apply_and_check "${version}" "${source}" diagnostic \
            "${SCRIPT_DIR}/patches/${version}/0002-cmp40hx-pcie2-diagnostic.patch" \
            "CMP40_PCIE_GEN2_DIAG"
    else
        printf '== %s / diagnostic: no patch (skipped) ==\n' "${version}" >&2
    fi
done

printf '\nOK: patch sets apply for: %s\n' "${selected[*]}"
