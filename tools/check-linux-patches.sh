#!/usr/bin/env bash
# Verify that the Linux patch set still applies to the pinned NVIDIA source.
#
# The patches in the repository root target one exact version of
# open-gpu-kernel-modules. When NVIDIA retags the source, replaces the archive,
# or a patch is edited against a different tree, the failure only shows up on a
# user's machine at install time. This script reproduces the install-time patch
# step — and nothing else: no kernel headers, no compiler, no root.
#
# Usage:
#   ./tools/check-linux-patches.sh [--source ARCHIVE_OR_DIR] [--keep]
#
#   --source PATH   NVIDIA source: a .tar.gz/.tar.xz/.tar.bz2 archive or an
#                   existing extracted tree. Default: download the pinned
#                   archive from GitHub and verify its SHA256.
#   --keep          Keep the temporary work directory (printed on success).
#
# Exit status:
#   0  both PCIe variants apply cleanly and all markers are present
#   1  a patch does not apply, a marker is missing, or the source is wrong
#   2  usage or environment error
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Keep these in sync with install.sh — the script deliberately fails when they
# disagree with the installer, because CI would then test the wrong thing.
DRIVER_VERSION="610.57.04"
SRC_SHA256="619d7b5ce1f79c3211afdbf87d02b2174d268b10d005c5b8f994be22299be681"
DOWNLOAD_URL="https://github.com/NVIDIA/open-gpu-kernel-modules/archive/refs/tags/${DRIVER_VERSION}.tar.gz"

PATCH_COMPUTE="${SCRIPT_DIR}/0001-cmp40hx-unlock.patch"
PATCH_PCIE="${SCRIPT_DIR}/0002-cmp40hx-pcie2-unlock.patch"
PATCH_PCIE_DIAG="${SCRIPT_DIR}/0002-cmp40hx-pcie2-diagnostic.patch"
PATCH_REBAR="${SCRIPT_DIR}/0003-cmp40hx-rebar-unlock.patch"

SRC_INPUT=""
KEEP=0

usage() { awk 'NR>1 { if (/^#/) { sub(/^# ?/, ""); print } else exit }' "${BASH_SOURCE[0]}"; }

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
    case "$1" in
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

for tool in patch tar sha256sum find mktemp grep; do
    command -v "${tool}" >/dev/null 2>&1 || die "missing required tool: ${tool}"
done

for patch_file in "${PATCH_COMPUTE}" "${PATCH_PCIE}" "${PATCH_PCIE_DIAG}" "${PATCH_REBAR}"; do
    [[ -f "${patch_file}" ]] || die "missing patch: ${patch_file}"
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

# ---------- Obtain the source ----------
download_source() {
    local out="$WORK/source.tar.gz"
    command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 \
        || die "neither curl nor wget is installed; pass --source PATH"
    printf 'downloading %s\n' "${DOWNLOAD_URL}" >&2
    if command -v curl >/dev/null 2>&1; then
        curl -fL --retry 3 --silent --show-error "${DOWNLOAD_URL}" -o "${out}"
    else
        wget -q -O "${out}" "${DOWNLOAD_URL}"
    fi
    local actual
    actual="$(sha256sum "${out}" | awk '{print $1}')"
    [[ "${actual}" == "${SRC_SHA256}" ]] \
        || die "SHA256 mismatch for the downloaded archive (expected ${SRC_SHA256}, got ${actual}). The pinned source changed; revalidation is required."
    SRC_INPUT="${out}"
}

if [[ -z "${SRC_INPUT}" ]]; then
    download_source
fi

# ---------- Prepare a pristine tree ----------
# Every variant starts from the same extracted tree, so the second run cannot
# accidentally see remnants of the first.
make_tree() {
    local dest="$1"
    mkdir -p "${dest}"
    case "${SRC_INPUT}" in
        *.tar.gz|*.tgz)
            tar --no-same-owner --no-same-permissions -xzf "${SRC_INPUT}" -C "${dest}" ;;
        *.tar.xz)
            tar --no-same-owner --no-same-permissions -xJf "${SRC_INPUT}" -C "${dest}" ;;
        *.tar.bz2)
            tar --no-same-owner --no-same-permissions -xjf "${SRC_INPUT}" -C "${dest}" ;;
        *)
            [[ -d "${SRC_INPUT}" ]] || die "not an archive or directory: ${SRC_INPUT}"
            cp -a -- "${SRC_INPUT}/." "${dest}/" ;;
    esac
    local root
    root="$(find "${dest}" -mindepth 1 -maxdepth 1 -type d -name "*${DRIVER_VERSION}*" -print -quit)"
    [[ -n "${root}" ]] || root="${dest}"
    printf '%s' "${root}"
}

check_version() {
    local tree="$1"
    [[ -f "${tree}/version.mk" ]] || die "not an NVIDIA source tree: ${tree} (version.mk missing)"
    local version
    version="$(awk -F= '$1 ~ /^NVIDIA_VERSION[[:space:]]*$/ { gsub(/[[:space:]]/, "", $2); print $2; exit }' "${tree}/version.mk")"
    [[ "${version}" == "${DRIVER_VERSION}" ]] \
        || die "source is ${version:-unknown}; the patches target ${DRIVER_VERSION}"
}

apply_and_check() {
    local label="$1" pcie_patch="$2" marker="$3"
    local tree
    tree="$(make_tree "$WORK/$label")"
    check_version "${tree}"

    printf '== %s ==\n' "${label}" >&2
    (
        cd "${tree}"
        for patch_file in "${PATCH_COMPUTE}" "${pcie_patch}" "${PATCH_REBAR}"; do
            patch --dry-run -p1 < "${patch_file}" >/dev/null 2>&1 \
                || die "$(basename "${patch_file}") does not apply to ${DRIVER_VERSION}"
            patch --silent -p1 < "${patch_file}"
            printf '  %s: applied\n' "$(basename "${patch_file}")" >&2
        done

        grep -Rqs "CMP40_COMPUTE_UNLOCK" src/nvidia \
            || die "compute marker missing after ${label}"
        grep -Rqs "${marker}" kernel-open/nvidia \
            || die "PCIe marker ${marker} missing after ${label}"
        grep -Rqs "CMP40_REBAR" kernel-open/nvidia \
            || die "ReBAR marker missing after ${label}"
    )
    printf '  markers present\n' >&2
}

apply_and_check normal "${PATCH_PCIE}" "CMP40_PCIE_GEN2_V2"
apply_and_check diagnostic "${PATCH_PCIE_DIAG}" "CMP40_PCIE_GEN2_DIAG"

printf '\nOK: patch set applies to NVIDIA %s in both PCIe modes\n' "${DRIVER_VERSION}"
