#!/bin/bash
# =============================================================================
# CMP 40HX — Compute + PCIe Gen2 + ReBAR Unlock Installer
# Targets Arch Linux / CachyOS, Ubuntu/Debian, Fedora/RHEL and openSUSE.
# The installer itself does not install packages or require a specific package manager.
#
# Features:
#   - Downloads NVIDIA open-gpu-kernel-modules 615.78.08 by default
#   - Verifies the source archive SHA256
#   - Applies the CMP 40HX unlock patches with GNU patch
#   - Builds the open NVIDIA kernel modules
#   - Installs them into /lib/modules/<kernel>/updates/cmpunlocker
#   - Supports the optional PCIe diagnostic patch
#   - Supports optional CMP SKU profiling on 615.78.08
#
# Usage:
#   sudo ./install.sh
#   sudo ./install.sh --no-download
#   sudo ./install.sh --source-dir=PATH
#   sudo ./install.sh --pcie-diagnostic
#   sudo ./install.sh --source-dir=PATH --pcie-diagnostic
#
# Requirements:
#   Linux, matching kernel headers, a working C compiler, make, patch, tar,
#   sha256sum, pciutils, kmod/depmod, and either curl or wget for downloads.
#   The installed NVIDIA userspace/firmware must match the selected driver version.
#
# IMPORTANT:
#   The NVIDIA source tree is NOT treated as a Git repository.
#   Patches are applied with "patch -p1" so this installer also works when
#   the source directory is located inside another Git repository.
# =============================================================================

set -euo pipefail

# ---------- Configuration ----------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

DRIVER_VERSION="${NVIDIA_VERSION:-615.78.08}"
# DOWNLOAD_URL is derived from the final version after the argument loop.

# Supported open-gpu-kernel-modules releases, each with its own patch
# directory (patches/<version>/) and official source-archive SHA256.
SUPPORTED_VERSIONS=(580.173.02 610.57.04 615.78.08)

sha256_for() {
    case "$1" in
        580.173.02) echo "a2cd41cf100a81d90de9d5ca192b828ed8a63408330acef7931df00487acd82f" ;;
        610.57.04) echo "619d7b5ce1f79c3211afdbf87d02b2174d268b10d005c5b8f994be22299be681" ;;
        615.78.08) echo "eac0ecd52facab28ad2963cf7e03ae8d2289d596ad590c703b5f72d65899e2bf" ;;
        *) echo "" ;;
    esac
}

SRC_SHA256=""

SOURCE_SHA256="${SOURCE_SHA256:-}"
SOURCE_SHA256="${SOURCE_SHA256,,}"

# Patches live in patches/<driver-version>/. They are resolved after the
# argument loop because --driver-version may select a different directory.
PATCH_DIR=""
PATCH_COMPUTE=""
PATCH_PCIE=""
PATCH_PCIE_DIAG=""
PATCH_REBAR=""
PATCH_PROFILING=""

# Set when a local archive was used; stays empty for --source-dir installs.
TARBALL=""

ARTIFACTS_DIR="${SCRIPT_DIR}/artifacts"

KERNEL_UNAME="${KERNEL_UNAME:-$(uname -r)}"
KERNEL_HDRS="${KERNEL_HDRS:-}"
if [[ -z "${KERNEL_HDRS}" ]]; then
    for candidate in \
        "/lib/modules/${KERNEL_UNAME}/build" \
        "/usr/lib/modules/${KERNEL_UNAME}/build"; do
        if [[ -d "${candidate}" ]]; then
            KERNEL_HDRS="${candidate}"
            break
        fi
    done
fi

INITRAMFS_TOOL="${INITRAMFS_TOOL:-auto}"

# SRC_DIR and LOCAL_TARBALLS depend on the driver version and are defined
# after the argument loop resolves --driver-version.

# ---------- Output ----------
# Colours are opt-in: only when stdout is a terminal and NO_COLOR is unset.
# Otherwise every escape is empty, so logs stay clean in CI and when piped
# through tee/journal/cat.
if [[ -t 1 && -z "${NO_COLOR:-}" ]]; then
    RED='\033[0;31m'
    YEL='\033[1;33m'
    GRN='\033[0;32m'
    BLU='\033[0;34m'
    NC='\033[0m'
else
    RED='' YEL='' GRN='' BLU='' NC=''
fi

info()  { echo -e "${GRN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YEL}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERR]${NC}   $*" >&2; }
step()  { echo -e "${BLU}[STEP]${NC}  $*"; }

die() {
    error "$*"
    exit 1
}

# ---------- Arguments ----------
USE_LOCAL_SRC=0
USE_PCIE_DIAGNOSTIC=0
USE_PROFILING=0
SOURCE_DIR_EXPLICIT=0
ASSUME_YES=0
DRY_RUN=0
FORCE_UNINSTALL=0
ACTION="install"

for arg in "$@"; do
    case "${arg}" in
        --source-dir=*)
            SRC_DIR="${arg#--source-dir=}"
            USE_LOCAL_SRC=1
            SOURCE_DIR_EXPLICIT=1
            ;;
        --driver-version=*)
            DRIVER_VERSION="${arg#--driver-version=}"
            ;;
        --driver-version)
            shift
            [[ $# -gt 0 ]] || die "--driver-version requires a value."
            DRIVER_VERSION="$1"
            ;;
        --no-download)
            USE_LOCAL_SRC=1
            ;;
        --pcie-diagnostic)
            USE_PCIE_DIAGNOSTIC=1
            ;;
        --profiling)
            USE_PROFILING=1
            ;;
        -y|--yes)
            ASSUME_YES=1
            ;;
        --dry-run)
            DRY_RUN=1
            ;;
        --uninstall)
            ACTION="uninstall"
            ;;
        --force)
            FORCE_UNINSTALL=1
            ;;
        --verify)
            ACTION="verify"
            ;;
        -h|--help)
            cat <<EOF
Usage: sudo ./install.sh [OPTIONS]

Actions:
  (default)              Build and install the patched modules
  --verify               Check the installed modules against the manifest
                         written by the last install; changes nothing
  --uninstall            Remove the installed patched modules, and rebuild
                         depmod/initramfs; asks for confirmation unless --yes

Options:
  --driver-version=VER   NVIDIA open-gpu-kernel-modules version to build.
                         Supported: 580.173.02, 610.57.04, 615.78.08 (default)
  --source-dir=PATH      Use an existing NVIDIA source tree
  --no-download          Do not download; use an existing source tree or
                         local source archive
  --pcie-diagnostic      Use the diagnostic PCIe Gen2 patch instead of the
                         normal one (not available for 580.173.02)
  --profiling            Apply the CMP SKU profiling unlock (615.78.08 only;
                         CUPTI support gate confirmed on CMP 40HX)
  -y, --yes              Do not ask questions; use the default answer. Also
                         implied when stdin is not a terminal. Questions
                         default to "no", so --yes alone never removes files
  --force                With --uninstall, allow removing modules that no
                         manifest records (installs made by older versions)
  --dry-run              Run every check (tools, headers, driver version,
                         patch application) but do not build or install
  -h, --help             Show this help

Environment:
  NVIDIA_VERSION=VER    Select driver version (default: 615.78.08);
                         --driver-version takes precedence
  KERNEL_UNAME=...       Override the kernel version (default: uname -r)
  KERNEL_HDRS=PATH       Override the kernel build/header directory
  INITRAMFS_TOOL=auto|mkinitcpio|update-initramfs|dracut|mkinitrd|none
                         Select initramfs tool (default: auto)
  SOURCE_SHA256=HEX      SHA256 for a non-official local source archive
  CC=clang               Force Clang/LLVM
  CC=gcc                 Force GCC
  JOBS=N                 Number of parallel build jobs (default: nproc)
  NO_COLOR=1             Disable coloured output

Examples:
  sudo ./install.sh
  sudo ./install.sh --dry-run
  sudo ./install.sh --pcie-diagnostic
  sudo ./install.sh --profiling
  sudo ./install.sh --source-dir=/path/to/open-gpu-kernel-modules-${DRIVER_VERSION}
  sudo ./install.sh --verify
  sudo ./install.sh --uninstall --yes
  sudo env KERNEL_UNAME=6.12.1-cachyos ./install.sh
  sudo env CC=clang ./install.sh
  sudo env JOBS=4 INITRAMFS_TOOL=dracut ./install.sh
EOF
            exit 0
            ;;
        *)
            die "Unknown argument: ${arg} (use -h for help)"
            ;;
    esac
done

# Validate the driver version and resolve its patch directory.
version_supported() {
    local v
    for v in "${SUPPORTED_VERSIONS[@]}"; do
        [[ "$v" == "$1" ]] && return 0
    done
    return 1
}

if ! version_supported "${DRIVER_VERSION}"; then
    die "Unsupported driver version: ${DRIVER_VERSION}. Supported: ${SUPPORTED_VERSIONS[*]}"
fi

PATCH_DIR="${SCRIPT_DIR}/patches/${DRIVER_VERSION}"
PATCH_COMPUTE="${PATCH_DIR}/0001-cmp40hx-unlock.patch"
PATCH_PCIE="${PATCH_DIR}/0002-cmp40hx-pcie2-unlock.patch"
PATCH_PCIE_DIAG="${PATCH_DIR}/0002-cmp40hx-pcie2-diagnostic.patch"
PATCH_REBAR="${PATCH_DIR}/0003-cmp40hx-rebar-unlock.patch"
PATCH_PROFILING="${PATCH_DIR}/0004-cmp40hx-profiling-unlock.patch"
SRC_SHA256="$(sha256_for "${DRIVER_VERSION}")"
DOWNLOAD_URL="https://github.com/NVIDIA/open-gpu-kernel-modules/archive/refs/tags/${DRIVER_VERSION}.tar.gz"

# Resolved here, not in the configuration block: both depend on the version.
if [[ "${SOURCE_DIR_EXPLICIT}" -eq 0 ]]; then
    SRC_DIR="${SCRIPT_DIR}/open-gpu-kernel-modules-${DRIVER_VERSION}"
fi
LOCAL_TARBALLS=(
    "${SCRIPT_DIR}/open-gpu-kernel-modules-${DRIVER_VERSION}.tar.gz"
    "${SCRIPT_DIR}/NVIDIA-kernel-module-source-${DRIVER_VERSION}.tar.xz"
    "${SCRIPT_DIR}/NVIDIA-kernel-module-source-${DRIVER_VERSION}.tar.bz2"
    "${SCRIPT_DIR}/NVIDIA-${DRIVER_VERSION}.tar.xz"
)

# 580.173.02 ships no diagnostic PCIe patch; fail early instead of half-way.
if [[ "${USE_PCIE_DIAGNOSTIC}" -eq 1 && ! -f "${PATCH_PCIE_DIAG}" ]]; then
    die "--pcie-diagnostic is not available for ${DRIVER_VERSION} (no diagnostic PCIe patch in patches/${DRIVER_VERSION}/)."
fi

# Profiling remains independently selectable; CUPTI support gate is confirmed.
if [[ "${USE_PROFILING}" -eq 1 && ! -f "${PATCH_PROFILING}" ]]; then
    die "--profiling is not available for ${DRIVER_VERSION} (no profiling patch in patches/${DRIVER_VERSION}/)."
fi

# Questions are skipped with --yes or when there is no terminal to answer on.
# Without this, `read` returns EOF under `set -e` and the script dies with no
# explanation in CI, cron, or `sudo ./install.sh < /dev/null`.
ask() { # ask <question> <default: y|n>
    local question="$1" default="$2" answer=""
    if [[ "${ASSUME_YES}" -eq 1 || ! -t 0 ]]; then
        info "${question} — assuming '${default}' (--yes or non-interactive)."
        [[ "${default}" == "y" ]]
        return
    fi
    read -r -p "${question} [$( [[ "${default}" == "y" ]] && echo "Y/n" || echo "y/N" )] " answer
    answer="${answer:-${default}}"
    [[ "${answer,,}" == "y" ]]
}

INSTALL_DIR="/lib/modules/${KERNEL_UNAME}/updates/cmpunlocker"
MANIFEST_DIR="/var/lib/cmp40hx"
MANIFEST_FILE="${MANIFEST_DIR}/install-${KERNEL_UNAME}.manifest"

# Remove only the files the previous install recorded, plus the manifest
# itself. Anything else in the module tree is left alone.
#
# Without a manifest there is no record of what this project installed, and the
# directory may also hold modules from other tools — so the no-manifest path
# lists the files and does nothing unless the operator passes --force.
uninstall_modules() {
    if [[ -f "${MANIFEST_FILE}" ]]; then
        info "Using manifest: ${MANIFEST_FILE}"
        local removed=0 name _file
        while IFS=$'\t' read -r kind _file _; do
            [[ "${kind}" == "module" ]] || continue
            name="$(basename -- "${_file}")"
            [[ "${name}" =~ ^[A-Za-z0-9_-]+\.ko$ ]] \
                || { warn "Skipping unexpected module name in manifest: ${name}"; continue; }
            if [[ -f "${INSTALL_DIR}/${name}" ]]; then
                rm -f -- "${INSTALL_DIR}/${name}"
                removed=$((removed + 1))
            fi
        done < "${MANIFEST_FILE}"
        info "Removed ${removed} module(s) recorded in the manifest."
    elif [[ -d "${INSTALL_DIR}" ]]; then
        local candidates=()
        while IFS= read -r -d '' f; do
            candidates+=("$(basename -- "${f}")")
        done < <(find "${INSTALL_DIR}" -maxdepth 1 -type f -name '*.ko' -print0)

        if [[ ${#candidates[@]} -eq 0 ]]; then
            info "No .ko files and no manifest in ${INSTALL_DIR}; nothing to remove."
        elif [[ "${FORCE_UNINSTALL}" -ne 1 ]]; then
            warn "No manifest records what was installed for kernel ${KERNEL_UNAME}."
            warn "This install predates the manifest, so these files cannot be"
            warn "verified as the project's own. Found in ${INSTALL_DIR}:"
            local f
            for f in "${candidates[@]}"; do
                echo "        ${f}"
            done
            echo
            error "Refusing to delete unrecorded files."
            echo "  Remove them explicitly if you are sure:"
            echo "      sudo ./install.sh --uninstall --force"
            echo "  Or remove the directory yourself after inspecting it:"
            echo "      ls -la ${INSTALL_DIR}"
            exit 1
        else
            warn "--force: removing ${#candidates[@]} unrecorded module(s) from ${INSTALL_DIR}."
            local f
            for f in "${candidates[@]}"; do
                rm -f -- "${INSTALL_DIR}/${f}"
            done
        fi
    else
        info "Nothing to remove: ${INSTALL_DIR} does not exist."
    fi

    rmdir -- "${INSTALL_DIR}" 2>/dev/null || true
    rm -f -- "${MANIFEST_FILE}"
    rmdir -- "${MANIFEST_DIR}" 2>/dev/null || true

    depmod -a "${KERNEL_UNAME}"
    info "depmod completed."
    rebuild_initramfs
}

# Compare the installed modules with the hashes recorded at install time.
verify_modules() {
    [[ -f "${MANIFEST_FILE}" ]] \
        || die "No manifest for ${KERNEL_UNAME}: ${MANIFEST_FILE}. Run install.sh first."

    local ok=0 bad=0 kind file expected actual
    info "Manifest: ${MANIFEST_FILE}"
    while IFS=$'\t' read -r kind file expected; do
        [[ "${kind}" == "module" ]] || continue
        actual=""
        if [[ -f "${INSTALL_DIR}/$(basename -- "${file}")" ]]; then
            actual="$(sha256sum "${INSTALL_DIR}/$(basename -- "${file}")" | awk '{print $1}')"
        fi
        if [[ "${actual}" == "${expected}" ]]; then
            info "OK      $(basename -- "${file}")"
            ok=$((ok + 1))
        else
            error "MISMATCH $(basename -- "${file}") (installed: ${actual:-missing}, expected: ${expected})"
            bad=$((bad + 1))
        fi
    done < "${MANIFEST_FILE}"

    if [[ "${bad}" -gt 0 ]]; then
        die "${bad} module(s) do not match; rebuild with install.sh or remove them with --uninstall."
    fi
    info "${ok} module(s) verified against the manifest."
}
# The initramfs tool is detected once here and used by both the install path
# and --uninstall. INITRAMFS_TOOL can be forced from the environment; "auto"
# picks the tool the distribution actually uses.
detect_initramfs_tool() {
    case "${INITRAMFS_TOOL}" in
        auto)
            if [[ -f /etc/mkinitcpio.conf ]] \
                && command -v mkinitcpio >/dev/null 2>&1; then
                INITRAMFS_TOOL="mkinitcpio"
            elif [[ -d /etc/initramfs-tools ]] \
                && command -v update-initramfs >/dev/null 2>&1; then
                INITRAMFS_TOOL="update-initramfs"
            elif command -v dracut >/dev/null 2>&1; then
                INITRAMFS_TOOL="dracut"
            elif command -v mkinitrd >/dev/null 2>&1; then
                INITRAMFS_TOOL="mkinitrd"
            elif command -v update-initramfs >/dev/null 2>&1; then
                INITRAMFS_TOOL="update-initramfs"
            elif command -v mkinitcpio >/dev/null 2>&1; then
                INITRAMFS_TOOL="mkinitcpio"
            else
                INITRAMFS_TOOL="none"
            fi
            ;;
    esac
}

rebuild_initramfs() {
    case "${INITRAMFS_TOOL}" in
        mkinitcpio)
            command -v mkinitcpio >/dev/null 2>&1 \
                || die "INITRAMFS_TOOL=mkinitcpio but mkinitcpio is not installed."
            info "Rebuilding initramfs with mkinitcpio..."
            mkinitcpio -P
            info "mkinitcpio completed."
            ;;
        update-initramfs)
            command -v update-initramfs >/dev/null 2>&1 \
                || die "INITRAMFS_TOOL=update-initramfs but update-initramfs is not installed."
            info "Rebuilding initramfs with update-initramfs..."
            update-initramfs -u -k "${KERNEL_UNAME}"
            info "update-initramfs completed."
            ;;
        dracut)
            command -v dracut >/dev/null 2>&1 \
                || die "INITRAMFS_TOOL=dracut but dracut is not installed."
            info "Rebuilding initramfs with dracut..."
            dracut --force --kver "${KERNEL_UNAME}"
            info "dracut completed."
            ;;
        mkinitrd)
            command -v mkinitrd >/dev/null 2>&1 \
                || die "INITRAMFS_TOOL=mkinitrd but mkinitrd is not installed."
            info "Rebuilding initramfs with mkinitrd..."
            mkinitrd -f "/boot/initramfs-${KERNEL_UNAME}.img" "${KERNEL_UNAME}"
            info "mkinitrd completed."
            ;;
        none)
            warn "Skipping initramfs rebuild. Rebuild it with your distribution's "\
                 "tool before rebooting if NVIDIA modules are included in initramfs."
            ;;
    esac
}

echo
echo "======================================================================"
echo "  CMP 40HX — Unlock Installer"
echo "  NVIDIA source: ${DRIVER_VERSION}"
echo "  Kernel: ${KERNEL_UNAME}"
echo "  Action: ${ACTION}"
echo "======================================================================"
echo

step "[1/6] Checking environment"

[[ "$(id -u)" -eq 0 ]] || die "Run this script with sudo."

if [[ -n "${SOURCE_SHA256}" && ! "${SOURCE_SHA256}" =~ ^[[:xdigit:]]{64}$ ]]; then
    die "Invalid SOURCE_SHA256: expected 64 hexadecimal characters."
fi

[[ -n "${KERNEL_UNAME}" ]] || die "KERNEL_UNAME is empty."
[[ "${KERNEL_UNAME}" =~ ^[A-Za-z0-9._+-]+$ ]] \
    || die "Invalid KERNEL_UNAME: ${KERNEL_UNAME}"

case "${INITRAMFS_TOOL}" in
    auto|mkinitcpio|update-initramfs|dracut|mkinitrd|none) ;;
    *) die "Invalid INITRAMFS_TOOL: ${INITRAMFS_TOOL}" ;;
esac

detect_initramfs_tool

# --verify and --uninstall never download, patch, or build anything, so they
# do not require headers, a compiler, or a network tool.
if [[ "${ACTION}" == "verify" ]]; then
    step "Verifying installed modules"
    verify_modules
    exit 0
fi

if [[ "${ACTION}" == "uninstall" ]]; then
    step "Removing installed modules"
    if [[ -d "${INSTALL_DIR}" || -f "${MANIFEST_FILE}" ]]; then
        # A manifest proves exactly what is being removed, so non-interactive
        # runs may proceed. Without one, the answer is "no" unless the
        # operator explicitly passed --force.
        if [[ -f "${MANIFEST_FILE}" || "${FORCE_UNINSTALL}" -eq 1 ]]; then
            ask "Remove the patched NVIDIA modules from ${INSTALL_DIR}?" y \
                || { echo "Cancelled."; exit 0; }
        else
            ask "Remove the patched NVIDIA modules from ${INSTALL_DIR}?" n \
                || { echo "Cancelled."; exit 0; }
        fi
        uninstall_modules
    else
        info "Nothing to remove for kernel ${KERNEL_UNAME}."
    fi
    exit 0
fi

for tool in make patch sha256sum tar lspci depmod install grep awk sed find date dirname basename head; do
    command -v "${tool}" >/dev/null 2>&1 \
        || die "Required tool not found: ${tool}"
done

if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
    if [[ "${USE_LOCAL_SRC}" -eq 0 ]]; then
        die "Neither curl nor wget is installed. Install one with your distribution's package manager."
    fi
fi

[[ -d "${KERNEL_HDRS}" ]] || {
    error "Kernel headers not found: ${KERNEL_HDRS}"
    echo
    echo "Install the headers/devel package matching ${KERNEL_UNAME}."
    echo "If the headers are in a non-standard location, set:"
    echo "  KERNEL_HDRS=/path/to/kernel/build sudo ./install.sh"
    exit 1
}

info "Kernel headers: ${KERNEL_HDRS}"

DISTRO="$(awk -F= '$1 == "PRETTY_NAME" {
    value = $2
    gsub(/^"|"$/, "", value)
    print value
    exit
}' /etc/os-release 2>/dev/null || true)"
DISTRO="${DISTRO:-Linux}"
info "Distribution: ${DISTRO}"

GPU_LINE="$(lspci -nn 2>/dev/null | grep -i 'nvidia' | grep -i '1f0b' | head -1 || true)"
if [[ -n "${GPU_LINE}" ]]; then
    info "CMP 40HX detected: ${GPU_LINE}"
else
    warn "CMP 40HX (10de:1f0b) was not found in lspci. Continuing."
fi

[[ -f "${PATCH_COMPUTE}" ]] || die "Missing patch: ${PATCH_COMPUTE}"
[[ -f "${PATCH_REBAR}" ]]   || die "Missing patch: ${PATCH_REBAR}"
if [[ "${USE_PROFILING}" -eq 1 ]]; then
    info "Profiling patch: CMP SKU query mask enabled"
fi

if [[ "${USE_PCIE_DIAGNOSTIC}" -eq 1 ]]; then
    [[ -f "${PATCH_PCIE_DIAG}" ]] || die "Missing diagnostic patch: ${PATCH_PCIE_DIAG}"
    info "PCIe patch mode: DIAGNOSTIC"
else
    [[ -f "${PATCH_PCIE}" ]] || die "Missing patch: ${PATCH_PCIE}"
    info "PCIe patch mode: NORMAL"
fi

# ---------- NVIDIA userspace version ----------
NVIDIA_USERSPACE_VER=""
if command -v nvidia-smi >/dev/null 2>&1; then
    NVIDIA_USERSPACE_VER="$(
        nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null \
            | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true
    )"
fi

if [[ -z "${NVIDIA_USERSPACE_VER}" && -r /proc/driver/nvidia/version ]]; then
    NVIDIA_USERSPACE_VER="$(
        grep -oE '[0-9]+\.[0-9]+\.[0-9]+' /proc/driver/nvidia/version \
            | head -1 || true
    )"
fi

if [[ -z "${NVIDIA_USERSPACE_VER}" ]] && command -v modinfo >/dev/null 2>&1; then
    NVIDIA_USERSPACE_VER="$(
        modinfo -F version nvidia 2>/dev/null \
            | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true
    )"
fi

if [[ -z "${NVIDIA_USERSPACE_VER}" ]] && command -v pacman >/dev/null 2>&1; then
    NVIDIA_USERSPACE_VER="$(
        pacman -Q nvidia nvidia-open nvidia-dkms 2>/dev/null \
            | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true
    )"
fi

if [[ -z "${NVIDIA_USERSPACE_VER}" ]] && command -v dpkg-query >/dev/null 2>&1; then
    NVIDIA_USERSPACE_VER="$(
        dpkg-query -W -f='${Package} ${Version}\n' 2>/dev/null \
            | awk '$1 ~ /nvidia/ {print $2}' \
            | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true
    )"
fi

if [[ -z "${NVIDIA_USERSPACE_VER}" ]] && command -v rpm >/dev/null 2>&1; then
    NVIDIA_USERSPACE_VER="$(
        rpm -qa --qf '%{NAME} %{VERSION}-%{RELEASE}\n' 2>/dev/null \
            | awk '$1 ~ /nvidia/ {print $2}' \
            | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true
    )"
fi

if [[ -n "${NVIDIA_USERSPACE_VER}" ]]; then
    if [[ "${NVIDIA_USERSPACE_VER}" == "${DRIVER_VERSION}"* ]]; then
        info "NVIDIA userspace: ${NVIDIA_USERSPACE_VER} — compatible"
    else
        warn "NVIDIA userspace: ${NVIDIA_USERSPACE_VER}"
        warn "This installer targets NVIDIA ${DRIVER_VERSION}."
        warn "A userspace/kernel-module version mismatch can cause crashes."
        # A dry run changes nothing, so a mismatch is only worth reporting.
        if [[ "${DRY_RUN}" -eq 0 ]]; then
            ask "Continue anyway?" n || { echo "Cancelled."; exit 0; }
        fi
    fi
else
    warn "Could not determine the installed NVIDIA userspace version."
fi

# ---------- Secure Boot ----------
if command -v mokutil >/dev/null 2>&1; then
    SB_STATE="$(mokutil --sb-state 2>/dev/null || true)"
    if echo "${SB_STATE}" | grep -qi "enabled"; then
        warn "Secure Boot is enabled. Unsigned modules will not load."
        if [[ "${DRY_RUN}" -eq 0 ]]; then
            ask "Continue anyway?" n || { echo "Cancelled."; exit 0; }
        fi
    fi
fi

# ---------- Obtain sources ----------
step "[2/6] Obtaining NVIDIA ${DRIVER_VERSION} sources"

# Use an existing source directory when explicitly requested or when
# --no-download is used and the expected directory already exists.
if [[ "${SOURCE_DIR_EXPLICIT}" -eq 1 && ! -d "${SRC_DIR}" ]]; then
    die "Source directory does not exist: ${SRC_DIR}"
fi
if [[ "${USE_LOCAL_SRC}" -eq 1 && -d "${SRC_DIR}" ]]; then
    info "Using local source directory: ${SRC_DIR}"
else
    TARBALL=""

    for candidate in "${LOCAL_TARBALLS[@]}"; do
        if [[ -f "${candidate}" ]]; then
            TARBALL="${candidate}"
            info "Using local archive: ${TARBALL}"
            break
        fi
    done

    if [[ -z "${TARBALL}" ]]; then
        [[ "${USE_LOCAL_SRC}" -eq 0 ]] \
            || die "--no-download was specified, but no local source archive was found."

        TARBALL="${SCRIPT_DIR}/open-gpu-kernel-modules-${DRIVER_VERSION}.tar.gz"

        info "Downloading ${DOWNLOAD_URL}"

        if command -v curl >/dev/null 2>&1; then
            curl -L --fail --retry 3 --progress-bar \
                "${DOWNLOAD_URL}" -o "${TARBALL}"
        else
            wget --show-progress -O "${TARBALL}" "${DOWNLOAD_URL}"
        fi
    fi

    expected_sha256="${SOURCE_SHA256}"
    if [[ "$(basename "${TARBALL}")" == "open-gpu-kernel-modules-${DRIVER_VERSION}.tar.gz" ]]; then
        expected_sha256="${SRC_SHA256}"
    fi
    [[ -n "${expected_sha256}" ]] \
        || die "No SHA256 is configured for ${TARBALL}. Set SOURCE_SHA256 or use the official .tar.gz archive."

    info "Verifying SHA256..."
    actual="$(sha256sum "${TARBALL}" | awk '{print $1}')"

    [[ "${actual}" == "${expected_sha256}" ]] || {
        error "SHA256 mismatch!"
        error "Expected: ${expected_sha256}"
        error "Actual:   ${actual}"
        error "The archive is corrupted or is not the expected source."
        exit 1
    }

    info "SHA256: OK"

    info "Extracting source archive..."
    # Never recursively delete a user-selected path. If the expected source
    # directory already exists in download mode, move it aside so a failed or
    # repeated install remains recoverable.
    if [[ -e "${SRC_DIR}" ]]; then
        [[ -d "${SRC_DIR}" ]] || die "Refusing to overwrite existing non-directory: ${SRC_DIR}"
        backup_dir="${SRC_DIR}.previous.$(date +%s)"
        suffix=0
        while [[ -e "${backup_dir}" ]]; do
            suffix=$((suffix + 1))
            backup_dir="${SRC_DIR}.previous.$(date +%s).${suffix}"
        done
        mv -- "${SRC_DIR}" "${backup_dir}" \
            || die "Could not move existing source tree to ${backup_dir}"
        warn "Existing source tree moved to: ${backup_dir}"
    fi

    case "${TARBALL}" in
        *.tar.gz|*.tgz)
            tar --no-same-owner --no-same-permissions -xzf "${TARBALL}" -C "${SCRIPT_DIR}"
            ;;
        *.tar.xz)
            tar --no-same-owner --no-same-permissions -xJf "${TARBALL}" -C "${SCRIPT_DIR}"
            ;;
        *.tar.bz2)
            tar --no-same-owner --no-same-permissions -xjf "${TARBALL}" -C "${SCRIPT_DIR}"
            ;;
        *)
            die "Unsupported archive format: ${TARBALL}"
            ;;
    esac

    # GitHub normally uses the expected directory name. Keep a fallback for
    # compatible locally supplied archives.
    if [[ ! -d "${SRC_DIR}" ]]; then
        found="$(
            find "${SCRIPT_DIR}" -maxdepth 1 -mindepth 1 -type d \
                -name "*${DRIVER_VERSION}*" ! -name "*.previous.*" \
                -print -quit
        )"
        [[ -n "${found}" ]] || die "Extracted NVIDIA source directory not found."
        SRC_DIR="${found}"
    fi
fi

[[ -d "${SRC_DIR}" ]] || die "Source directory does not exist: ${SRC_DIR}"
[[ -d "${SRC_DIR}/kernel-open" ]] || {
    die "Invalid NVIDIA source tree: ${SRC_DIR} (kernel-open/ not found)"
}
[[ -f "${SRC_DIR}/version.mk" ]] || die "Invalid NVIDIA source tree: ${SRC_DIR} (version.mk not found)"
SOURCE_VERSION="$(awk -F= '$1 ~ /^NVIDIA_VERSION[[:space:]]*$/ {
    value = $2
    gsub(/[[:space:]]/, "", value)
    print value
    exit
}' "${SRC_DIR}/version.mk")"
[[ "${SOURCE_VERSION}" == "${DRIVER_VERSION}" ]] \
    || die "Source version is ${SOURCE_VERSION:-unknown}; expected ${DRIVER_VERSION}."

info "Sources ready: ${SRC_DIR}"

# ---------- Apply patches ----------
step "[3/6] Applying CMP 40HX patches"

cd "${SRC_DIR}"

# IMPORTANT:
# Do NOT use git apply here. The NVIDIA source archive is not a Git repository,
# and this directory may itself be located inside the user's cmpunlocker Git
# repository. GNU patch applies the diff paths relative to this source tree.
apply_patch() {
    local patch_file="$1"
    local patch_name
    patch_name="$(basename "${patch_file}")"

    info "Checking ${patch_name}..."

    # Fresh application.
    if patch --dry-run -p1 < "${patch_file}" >/dev/null 2>&1; then
        info "Applying ${patch_name}"
        patch -p1 < "${patch_file}"
        info "${patch_name}: applied successfully"
        return 0
    fi

    # Already applied.
    if patch --dry-run -R -p1 < "${patch_file}" >/dev/null 2>&1; then
        info "${patch_name}: already applied, keeping existing changes"
        return 0
    fi

    error "${patch_name}: cannot be applied."
    error "The source tree does not match NVIDIA ${DRIVER_VERSION},"
    error "or the patch has already been partially modified."
    exit 1
}

if [[ "${DRY_RUN}" -eq 1 ]]; then
    # Apply the whole series to a throwaway copy: this proves the patches
    # apply cleanly without touching the user's source tree.
    step "[3/6] Checking patches on a temporary copy (--dry-run)"
    DRY_COPY="$(mktemp -d "${TMPDIR:-/tmp}/cmp-unlock-dry.XXXXXXXX")"
    trap 'rm -rf -- "${DRY_COPY}"' EXIT
    cp -a -- "${SRC_DIR}/." "${DRY_COPY}/"
    cd "${DRY_COPY}"

    dry_patches=("${PATCH_COMPUTE}" \
        "$( [[ "${USE_PCIE_DIAGNOSTIC}" -eq 1 ]] && echo "${PATCH_PCIE_DIAG}" || echo "${PATCH_PCIE}" )" \
        "${PATCH_REBAR}")
    if [[ "${USE_PROFILING}" -eq 1 ]]; then
        dry_patches+=("${PATCH_PROFILING}")
    fi
    for patch_file in "${dry_patches[@]}"; do
        patch --dry-run -p1 < "${patch_file}" >/dev/null 2>&1 \
            || die "$(basename "${patch_file}"): does not apply to this source tree."
        info "$(basename "${patch_file}"): applies cleanly"
        patch -p1 --silent < "${patch_file}"
    done

    info "All patches apply cleanly."
    info "Dry run complete: nothing was built or installed."
    exit 0
fi

apply_patch "${PATCH_COMPUTE}"

if [[ "${USE_PCIE_DIAGNOSTIC}" -eq 1 ]]; then
    apply_patch "${PATCH_PCIE_DIAG}"
else
    apply_patch "${PATCH_PCIE}"
fi

apply_patch "${PATCH_REBAR}"
if [[ "${USE_PROFILING}" -eq 1 ]]; then
    apply_patch "${PATCH_PROFILING}"
    grep -Fqs 'CMP170HX profiling unlock, scoped to CMP 40HX only.' \
        src/nvidia/src/kernel/gpu/subdevice/subdevice_ctrl_gpu_kernel.c \
        || die "Profiling query mask missing after applying the profiling patch."
fi

# Hard verification: make sure the source tree contains the expected patched
# symbols. This is intentionally independent of Git.
if [[ "${USE_PCIE_DIAGNOSTIC}" -eq 1 ]]; then
    grep -Rqs "CMP40_PCIE_GEN2_DIAG" kernel-open/nvidia \
        || die "Diagnostic marker not found after applying the PCIe patch."
else
    grep -Rqs "CMP40_PCIE_GEN2_V2" kernel-open/nvidia \
        || die "PCIe marker not found after applying the PCIe patch."
fi
grep -Rqs "CMP40_COMPUTE_UNLOCK" src/nvidia \
    || die "Compute marker not found after applying the compute patch."
grep -Rqs "CMP40_REBAR" kernel-open/nvidia \
    || die "ReBAR marker not found after applying the ReBAR patch."

info "Patch set applied to: ${SRC_DIR}"
info "Source tree is intentionally handled without Git."

# ---------- Build ----------
step "[4/6] Building kernel modules"

DEFAULT_JOBS=1
if command -v nproc >/dev/null 2>&1; then
    DEFAULT_JOBS="$(nproc)"
fi
JOBS="${JOBS:-${DEFAULT_JOBS}}"
[[ "${JOBS}" =~ ^[1-9][0-9]*$ ]] || die "JOBS must be a positive integer (got: ${JOBS})"
info "Build jobs: ${JOBS}"

# Build flags are kept in an array so paths/arguments cannot be split
# accidentally by the shell.
MAKE_FLAGS=()

FORCE_CC="${CC:-}"

if [[ -n "${FORCE_CC}" ]]; then
    info "Compiler forced by environment: CC=${FORCE_CC}"

    if [[ "${FORCE_CC}" == */* ]]; then
        if [[ "${FORCE_CC}" == /* ]]; then
            FORCE_CC_PATH="${FORCE_CC}"
        else
            FORCE_CC_PATH="$(cd "$(dirname -- "${FORCE_CC}")" 2>/dev/null && pwd)/$(basename -- "${FORCE_CC}")" \
                || die "Compiler path cannot be resolved: ${FORCE_CC}"
        fi
        [[ -x "${FORCE_CC_PATH}" ]] || die "Compiler is not executable: ${FORCE_CC_PATH}"
    else
        FORCE_CC_PATH="$(command -v "${FORCE_CC}" 2>/dev/null || true)"
        [[ -n "${FORCE_CC_PATH}" ]] || die "Compiler not found: ${FORCE_CC}"
    fi

    case "$(basename "${FORCE_CC_PATH}")" in
        clang|clang-*)
            command -v ld.lld >/dev/null 2>&1 \
                || die "ld.lld is not installed. Install the LLVM linker package "\
                       "with your distribution's package manager."
            MAKE_FLAGS+=(CC="${FORCE_CC_PATH}" HOSTCC="${FORCE_CC_PATH}" LD=ld.lld LLVM=1)
            ;;
        gcc|gcc-*)
            MAKE_FLAGS+=(CC="${FORCE_CC_PATH}" HOSTCC="${FORCE_CC_PATH}")
            ;;
        *)
            MAKE_FLAGS+=(CC="${FORCE_CC_PATH}")
            ;;
    esac
else
    KERNEL_COMPILER="gcc"

    if grep -q "CONFIG_CC_IS_CLANG=y" \
        "${KERNEL_HDRS}/.config" 2>/dev/null \
        || grep -q "CONFIG_CC_IS_CLANG=y" \
        "${KERNEL_HDRS}/include/config/auto.conf" 2>/dev/null; then
        KERNEL_COMPILER="clang"
    fi

    if [[ "${KERNEL_COMPILER}" == "clang" ]]; then
        command -v clang >/dev/null 2>&1 \
            || die "Kernel uses Clang, but clang is not installed. Install the "\
                   "Clang/LLVM packages with your distribution's package manager."
        command -v ld.lld >/dev/null 2>&1 \
            || die "Kernel uses Clang, but ld.lld is not installed. Install the "\
                   "LLVM linker package with your distribution's package manager."
        MAKE_FLAGS+=(CC=clang HOSTCC=clang LD=ld.lld LLVM=1)
        info "Detected kernel compiler: Clang/LLVM"
    else
        command -v gcc >/dev/null 2>&1 \
            || die "gcc is not installed. Install a compiler toolchain with your "\
                   "distribution's package manager."
        info "Detected kernel compiler: GCC"
    fi
fi

info "Running kernel module build..."

make -j"${JOBS}" modules \
    "KERNEL_UNAME=${KERNEL_UNAME}" \
    "SYSSRC=${KERNEL_HDRS}" \
    "${MAKE_FLAGS[@]}"

info "Build completed successfully."

# ---------- Install ----------
step "[5/6] Installing kernel modules"

INSTALL_DIR="/lib/modules/${KERNEL_UNAME}/updates/cmpunlocker"
mkdir -p "${INSTALL_DIR}"

INSTALLED=0
for module in nvidia nvidia-modeset nvidia-drm nvidia-uvm nvidia-peermem; do
    if [[ -f "kernel-open/${module}.ko" ]]; then
        install -m 0644 "kernel-open/${module}.ko" "${INSTALL_DIR}/${module}.ko"
        INSTALLED=$((INSTALLED + 1))
    fi
done

[[ "${INSTALLED}" -gt 0 ]] || die "No kernel modules were produced in kernel-open/."

info "Installed ${INSTALLED} module(s) into ${INSTALL_DIR}"

depmod -a "${KERNEL_UNAME}"
info "depmod completed."

rebuild_initramfs

# ---------- Installation manifest ----------
# Records exactly what was installed, from which source and with which patches,
# so --verify and --uninstall operate on facts instead of globs, and so a bug
# report can state the installed state unambiguously.
mkdir -p "${MANIFEST_DIR}"
{
    printf 'format\t1\n'
    printf 'kernel\t%s\n' "${KERNEL_UNAME}"
    printf 'driver\t%s\n' "${DRIVER_VERSION}"
    printf 'source_sha256\t%s\n' "$(sha256sum "${TARBALL}" 2>/dev/null | awk '{print $1}' || true)"
    printf 'installed_at\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    printf 'pcie_mode\t%s\n' "$( [[ "${USE_PCIE_DIAGNOSTIC}" -eq 1 ]] && echo diagnostic || echo normal )"
    printf 'profiling\t%s\n' "$( [[ "${USE_PROFILING}" -eq 1 ]] && echo enabled || echo disabled )"
    printf 'patch\t%s\t%s\n' "$(basename "${PATCH_COMPUTE}")" "$(sha256sum "${PATCH_COMPUTE}" | awk '{print $1}')"
    if [[ "${USE_PCIE_DIAGNOSTIC}" -eq 1 ]]; then
        printf 'patch\t%s\t%s\n' "$(basename "${PATCH_PCIE_DIAG}")" "$(sha256sum "${PATCH_PCIE_DIAG}" | awk '{print $1}')"
    else
        printf 'patch\t%s\t%s\n' "$(basename "${PATCH_PCIE}")" "$(sha256sum "${PATCH_PCIE}" | awk '{print $1}')"
    fi
    printf 'patch\t%s\t%s\n' "$(basename "${PATCH_REBAR}")" "$(sha256sum "${PATCH_REBAR}" | awk '{print $1}')"
    if [[ "${USE_PROFILING}" -eq 1 ]]; then
        printf 'patch\t%s\t%s\n' "$(basename "${PATCH_PROFILING}")" "$(sha256sum "${PATCH_PROFILING}" | awk '{print $1}')"
    fi
    for module in "${INSTALL_DIR}"/*.ko; do
        [[ -e "${module}" ]] || continue
        printf 'module\t%s\t%s\n' "$(basename "${module}")" "$(sha256sum "${module}" | awk '{print $1}')"
    done
} > "${MANIFEST_FILE}"

info "Manifest written to: ${MANIFEST_FILE}"

# ---------- Save artifacts ----------
step "[6/6] Saving build artifacts"

mkdir -p "${ARTIFACTS_DIR}"

for module in "${INSTALL_DIR}"/*.ko; do
    [[ -e "${module}" ]] || continue
    cp -f "${module}" "${ARTIFACTS_DIR}/"
done

info "Artifacts saved to: ${ARTIFACTS_DIR}"

# ---------- Summary ----------
echo
echo "======================================================================"
echo "  SUCCESS: CMP 40HX patched NVIDIA kernel modules installed"
echo "======================================================================"
echo
echo "  Source tree:      ${SRC_DIR}"
echo "  Kernel:            ${KERNEL_UNAME}"
echo "  Install directory: ${INSTALL_DIR}"
echo "  Artifacts:         ${ARTIFACTS_DIR}"
echo "  Initramfs tool:    ${INITRAMFS_TOOL}"
echo

if [[ "${USE_PCIE_DIAGNOSTIC}" -eq 1 ]]; then
    echo "  PCIe mode:         DIAGNOSTIC"
else
    echo "  PCIe mode:         NORMAL"
fi

echo
echo "  IMPORTANT:"
echo "    Perform a full cold reboot before validating the unlock:"
echo "      sudo shutdown -h now"
echo
echo "  After boot:"
echo "      sudo dmesg | grep CMP40"
echo "      nvidia-smi"
echo
echo "  Uninstall:"
echo "      sudo ./install.sh --uninstall"
echo "      sudo ./install.sh --verify   # compare installed modules with the manifest"
echo
echo "  For PCIe diagnostic mode:"
echo "      sudo ./install.sh --pcie-diagnostic"
echo
echo "======================================================================"
