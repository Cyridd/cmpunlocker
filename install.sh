#!/bin/bash
# =============================================================================
# CMP 40HX — Compute + PCIe Gen2 + ReBAR Unlock Installer
# Tested on Arch Linux / CachyOS
#
# Features:
#   - Downloads NVIDIA open-gpu-kernel-modules 610.57.04
#   - Verifies the source archive SHA256
#   - Applies the CMP 40HX unlock patches with GNU patch
#   - Builds the open NVIDIA kernel modules
#   - Installs them into /lib/modules/<kernel>/updates/cmpunlocker
#   - Supports the optional PCIe diagnostic patch
#
# Usage:
#   sudo ./install.sh
#   sudo ./install.sh --no-download
#   sudo ./install.sh --source-dir=PATH
#   sudo ./install.sh --pcie-diagnostic
#   sudo ./install.sh --source-dir=PATH --pcie-diagnostic
#
# Requirements:
#   Arch Linux / CachyOS, matching kernel headers, make, patch, tar,
#   sha256sum, pciutils, and either curl or wget for automatic downloads.
#
# IMPORTANT:
#   The NVIDIA source tree is NOT treated as a Git repository.
#   Patches are applied with "patch -p1" so this installer also works when
#   the source directory is located inside another Git repository.
# =============================================================================

set -euo pipefail

# ---------- Configuration ----------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

DRIVER_VERSION="610.57.04"
DOWNLOAD_URL="https://github.com/NVIDIA/open-gpu-kernel-modules/archive/refs/tags/${DRIVER_VERSION}.tar.gz"

# SHA256 of the official NVIDIA open-gpu-kernel-modules 610.57.04 archive.
SRC_SHA256="619d7b5ce1f79c3211afdbf87d02b2174d268b10d005c5b8f994be22299be681"

PATCH_COMPUTE="${SCRIPT_DIR}/0001-cmp40hx-unlock.patch"
PATCH_PCIE="${SCRIPT_DIR}/0002-cmp40hx-pcie2-unlock.patch"
PATCH_PCIE_DIAG="${SCRIPT_DIR}/0002-cmp40hx-pcie2-diagnostic.patch"
PATCH_REBAR="${SCRIPT_DIR}/0003-cmp40hx-rebar-unlock.patch"

ARTIFACTS_DIR="${SCRIPT_DIR}/artifacts"

KERNEL_UNAME="${KERNEL_UNAME:-$(uname -r)}"
KERNEL_HDRS="/usr/lib/modules/${KERNEL_UNAME}/build"

SRC_DIR="${SCRIPT_DIR}/open-gpu-kernel-modules-${DRIVER_VERSION}"

LOCAL_TARBALLS=(
    "${SCRIPT_DIR}/open-gpu-kernel-modules-${DRIVER_VERSION}.tar.gz"
    "${SCRIPT_DIR}/NVIDIA-kernel-module-source-${DRIVER_VERSION}.tar.xz"
    "${SCRIPT_DIR}/NVIDIA-kernel-module-source-${DRIVER_VERSION}.tar.bz2"
    "${SCRIPT_DIR}/NVIDIA-${DRIVER_VERSION}.tar.xz"
)

# ---------- Output ----------
RED='\033[0;31m'
YEL='\033[1;33m'
GRN='\033[0;32m'
BLU='\033[0;34m'
NC='\033[0m'

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

for arg in "$@"; do
    case "${arg}" in
        --source-dir=*)
            SRC_DIR="${arg#--source-dir=}"
            USE_LOCAL_SRC=1
            ;;
        --no-download)
            USE_LOCAL_SRC=1
            ;;
        --pcie-diagnostic)
            USE_PCIE_DIAGNOSTIC=1
            ;;
        -h|--help)
            cat <<EOF
Usage: sudo ./install.sh [OPTIONS]

Options:
  --source-dir=PATH      Use an existing NVIDIA source tree
  --no-download          Do not download; use an existing source tree or
                         local source archive
  --pcie-diagnostic      Use 0002-cmp40hx-pcie2-diagnostic.patch instead of
                         the normal PCIe Gen2 patch
  -h, --help             Show this help

Environment:
  KERNEL_UNAME=...       Override the kernel version (default: uname -r)
  CC=clang               Force Clang/LLVM
  CC=gcc                 Force GCC
  JOBS=N                 Number of parallel build jobs (default: nproc)

Examples:
  sudo ./install.sh
  sudo ./install.sh --pcie-diagnostic
  sudo ./install.sh --source-dir=/path/to/open-gpu-kernel-modules-${DRIVER_VERSION}
  sudo KERNEL_UNAME=6.12.1-cachyos ./install.sh
  sudo env CC=clang ./install.sh
  sudo JOBS=4 ./install.sh
EOF
            exit 0
            ;;
        *)
            die "Unknown argument: ${arg} (use -h for help)"
            ;;
    esac
done

# ---------- Environment ----------
echo
echo "======================================================================"
echo "  CMP 40HX — Unlock Installer"
echo "  NVIDIA source: ${DRIVER_VERSION}"
echo "  Kernel: ${KERNEL_UNAME}"
echo "======================================================================"
echo

step "[1/6] Checking environment"

[[ "$(id -u)" -eq 0 ]] || die "Run this script with sudo."

for tool in make patch sha256sum tar lspci; do
    command -v "${tool}" >/dev/null 2>&1 \
        || die "Required tool not found: ${tool}"
done

if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
    if [[ "${USE_LOCAL_SRC}" -eq 0 ]]; then
        die "Neither curl nor wget is installed. Install one with pacman."
    fi
fi

[[ -d "${KERNEL_HDRS}" ]] || {
    error "Kernel headers not found: ${KERNEL_HDRS}"
    echo
    echo "Install the headers package matching ${KERNEL_UNAME}, for example:"
    echo "  sudo pacman -S linux-headers"
    echo "  sudo pacman -S linux-cachyos-headers"
    exit 1
}

info "Kernel headers: ${KERNEL_HDRS}"

if [[ -f /etc/cachyos-release ]]; then
    DISTRO="CachyOS"
elif grep -q "Arch Linux" /etc/os-release 2>/dev/null; then
    DISTRO="Arch Linux"
else
    DISTRO="Arch-based"
fi
info "Distribution: ${DISTRO}"

GPU_LINE="$(lspci -nn 2>/dev/null | grep -i 'nvidia' | grep -i '1f0b' | head -1 || true)"
if [[ -n "${GPU_LINE}" ]]; then
    info "CMP 40HX detected: ${GPU_LINE}"
else
    warn "CMP 40HX (10de:1f0b) was not found in lspci. Continuing."
fi

[[ -f "${PATCH_COMPUTE}" ]] || die "Missing patch: ${PATCH_COMPUTE}"
[[ -f "${PATCH_REBAR}" ]]   || die "Missing patch: ${PATCH_REBAR}"

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
            | head -1 || true
    )"
fi

if [[ -z "${NVIDIA_USERSPACE_VER}" ]] && command -v pacman >/dev/null 2>&1; then
    NVIDIA_USERSPACE_VER="$(
        pacman -Q nvidia nvidia-open nvidia-dkms 2>/dev/null \
            | awk '{print $2}' \
            | sed 's/-[0-9]*$//' \
            | head -1 || true
    )"
fi

if [[ -n "${NVIDIA_USERSPACE_VER}" ]]; then
    if [[ "${NVIDIA_USERSPACE_VER}" == "${DRIVER_VERSION}"* ]]; then
        info "NVIDIA userspace: ${NVIDIA_USERSPACE_VER} — compatible"
    else
        warn "NVIDIA userspace: ${NVIDIA_USERSPACE_VER}"
        warn "This installer targets NVIDIA ${DRIVER_VERSION}."
        warn "A userspace/kernel-module version mismatch can cause crashes."
        read -r -p "Continue anyway? [y/N] " answer
        [[ "${answer,,}" == "y" ]] || { echo "Cancelled."; exit 0; }
    fi
else
    warn "Could not determine the installed NVIDIA userspace version."
fi

# ---------- Secure Boot ----------
if command -v mokutil >/dev/null 2>&1; then
    SB_STATE="$(mokutil --sb-state 2>/dev/null || true)"
    if echo "${SB_STATE}" | grep -qi "enabled"; then
        warn "Secure Boot is enabled. Unsigned modules will not load."
        read -r -p "Continue anyway? [y/N] " answer
        [[ "${answer,,}" == "y" ]] || { echo "Cancelled."; exit 0; }
    fi
fi

# ---------- Obtain sources ----------
step "[2/6] Obtaining NVIDIA ${DRIVER_VERSION} sources"

# Use an existing source directory when explicitly requested or when
# --no-download is used and the expected directory already exists.
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

    info "Verifying SHA256..."
    actual="$(sha256sum "${TARBALL}" | awk '{print $1}')"

    [[ "${actual}" == "${SRC_SHA256}" ]] || {
        error "SHA256 mismatch!"
        error "Expected: ${SRC_SHA256}"
        error "Actual:   ${actual}"
        error "The archive is corrupted or is not the expected source."
        exit 1
    }

    info "SHA256: OK"

    info "Extracting source archive..."
    rm -rf -- "${SRC_DIR}"

    case "${TARBALL}" in
        *.tar.gz|*.tgz)
            tar xzf "${TARBALL}" -C "${SCRIPT_DIR}"
            ;;
        *.tar.xz)
            tar xJf "${TARBALL}" -C "${SCRIPT_DIR}"
            ;;
        *.tar.bz2)
            tar xjf "${TARBALL}" -C "${SCRIPT_DIR}"
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
                -name "*${DRIVER_VERSION}*" -print -quit
        )"
        [[ -n "${found}" ]] || die "Extracted NVIDIA source directory not found."
        SRC_DIR="${found}"
    fi
fi

[[ -d "${SRC_DIR}" ]] || die "Source directory does not exist: ${SRC_DIR}"
[[ -d "${SRC_DIR}/kernel-open" ]] || {
    die "Invalid NVIDIA source tree: ${SRC_DIR} (kernel-open/ not found)"
}

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

apply_patch "${PATCH_COMPUTE}"

if [[ "${USE_PCIE_DIAGNOSTIC}" -eq 1 ]]; then
    apply_patch "${PATCH_PCIE_DIAG}"
else
    apply_patch "${PATCH_PCIE}"
fi

apply_patch "${PATCH_REBAR}"

# Hard verification: make sure the source tree contains the expected patched
# symbols. This is intentionally independent of Git.
if [[ "${USE_PCIE_DIAGNOSTIC}" -eq 1 ]]; then
    grep -Rqs "CMP40_PCIE_GEN2_DIAG" kernel-open/nvidia \
        || warn "Diagnostic marker not found; inspect the patch output before continuing."
fi

info "Patch set applied to: ${SRC_DIR}"
info "Source tree is intentionally handled without Git."

# ---------- Build ----------
step "[4/6] Building kernel modules"

JOBS="${JOBS:-$(nproc)}"
info "Build jobs: ${JOBS}"

# Build flags are kept in an array so paths/arguments cannot be split
# accidentally by the shell.
MAKE_FLAGS=()

FORCE_CC="${CC:-}"

if [[ -n "${FORCE_CC}" ]]; then
    info "Compiler forced by environment: CC=${FORCE_CC}"

    case "${FORCE_CC}" in
        clang|*/clang|*clang-*)
            command -v clang >/dev/null 2>&1 \
                || die "clang is not installed. Install it with: sudo pacman -S clang llvm"
            command -v ld.lld >/dev/null 2>&1 \
                || die "ld.lld is not installed. Install it with: sudo pacman -S lld"
            MAKE_FLAGS+=(CC=clang HOSTCC=clang LD=ld.lld LLVM=1)
            ;;
        gcc|*/gcc|*gcc-*)
            command -v gcc >/dev/null 2>&1 || die "gcc is not installed."
            MAKE_FLAGS+=(CC="${FORCE_CC}" HOSTCC="${FORCE_CC}")
            ;;
        *)
            command -v "${FORCE_CC}" >/dev/null 2>&1 \
                || die "Compiler not found: ${FORCE_CC}"
            MAKE_FLAGS+=(CC="${FORCE_CC}")
            ;;
    esac
else
    KERNEL_COMPILER="gcc"

    if grep -q "CONFIG_CC_IS_CLANG=y" \
        "${KERNEL_HDRS}/.config" 2>/dev/null \
        || grep -q "CONFIG_CC_IS_CLANG=y" \
        "${KERNEL_HDRS}/include/config/auto.conf" 2>/dev/null \
        || grep -qi "clang" /proc/version 2>/dev/null; then
        KERNEL_COMPILER="clang"
    fi

    if [[ "${KERNEL_COMPILER}" == "clang" ]]; then
        command -v clang >/dev/null 2>&1 \
            || die "Kernel uses Clang, but clang is not installed."
        command -v ld.lld >/dev/null 2>&1 \
            || die "Kernel uses LLVM, but ld.lld is not installed."
        MAKE_FLAGS+=(CC=clang HOSTCC=clang LD=ld.lld LLVM=1)
        info "Detected kernel compiler: Clang/LLVM"
    else
        command -v gcc >/dev/null 2>&1 || die "gcc is not installed."
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

# Rebuild initramfs if mkinitcpio is available.
if command -v mkinitcpio >/dev/null 2>&1; then
    info "Rebuilding initramfs with mkinitcpio..."
    mkinitcpio -P
    info "mkinitcpio completed."
else
    warn "mkinitcpio not found. Rebuild your initramfs with your distro's tool if required."
fi

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
echo "      sudo rm -rf /lib/modules/\$(uname -r)/updates/cmpunlocker"
echo "      sudo depmod -a"
echo
echo "  For PCIe diagnostic mode:"
echo "      sudo ./install.sh --pcie-diagnostic"
echo
echo "======================================================================"
