#!/bin/bash
# =============================================================================
# CMP 40HX — Compute + PCIe Gen2 Unlock Installer
# Tested on Arch Linux / CachyOS
#
# Features: download NVIDIA open-gpu-kernel-modules 580.173.02 -> apply unlock
#           patches -> build -> install into updates/cmpunlocker
#           -> cold-reboot instructions
#
# Usage:
#   sudo ./install.sh                          # full cycle (download sources)
#   sudo ./install.sh --source-dir=PATH        # use a local source directory
#   sudo ./install.sh --no-download            # do not download; use a local source tree/archive
#
# Requirements: Arch Linux / CachyOS, installed kernel headers,
#              build tools (make, GCC or Clang), pciutils, and a CMP 40HX (0x1F0B)
# =============================================================================
set -euo pipefail

# ---------- Configuration ----------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PATCH_FILES=(
    "${SCRIPT_DIR}/0001-cmp40hx-unlock.patch"
    "${SCRIPT_DIR}/0002-cmp40hx-pcie2-unlock.patch"
    "${SCRIPT_DIR}/0003-cmp40hx-rebar-unlock.patch"
)
DRIVER_VERSION="580.173.02"
# SHA256 of the official NVIDIA open-gpu-kernel-modules 580.173.02 source archive
SRC_SHA256="a2cd41cf100a81d90de9d5ca192b828ed8a63408330acef7931df00487acd82f"
# Directory for built artifacts (.ko files)
ARTIFACTS_DIR="${SCRIPT_DIR}/artifacts"

# Kernel
KERNEL_UNAME="${KERNEL_UNAME:-$(uname -r)}"

# On Arch Linux, kernel headers are located at /usr/lib/modules/<uname>/build
KERNEL_HDRS="/usr/lib/modules/${KERNEL_UNAME}/build"

# Sources
SRC_DIR="${SCRIPT_DIR}/open-gpu-kernel-modules-${DRIVER_VERSION}"
DOWNLOAD_URL="https://github.com/NVIDIA/open-gpu-kernel-modules/archive/refs/tags/${DRIVER_VERSION}.tar.gz"

# Local archives (the first matching archive is used)
LOCAL_TARBALLS=(
    "${SCRIPT_DIR}/open-gpu-kernel-modules-${DRIVER_VERSION}.tar.gz"
    "${SCRIPT_DIR}/NVIDIA-kernel-module-source-${DRIVER_VERSION}.tar.xz"
    "${SCRIPT_DIR}/NVIDIA-kernel-module-source-${DRIVER_VERSION}.tar.bz2"
    "${SCRIPT_DIR}/NVIDIA-${DRIVER_VERSION}.tar.xz"
)

# ---------- Output colors ----------
RED='\033[0;31m'
YEL='\033[1;33m'
GRN='\033[0;32m'
NC='\033[0m' # no color

info()  { echo -e "${GRN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YEL}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERR]${NC}   $*" >&2; }

# ---------- Arguments ----------
USE_LOCAL_SRC=0
for arg in "$@"; do
    case "${arg}" in
        --source-dir=*) SRC_DIR="${arg#--source-dir=}"; USE_LOCAL_SRC=1 ;;
        --no-download)  USE_LOCAL_SRC=1 ;;
        -h|--help)
            cat <<'EOF'
Usage: sudo ./install.sh [--source-dir=PATH] [--no-download]

  --source-dir=PATH  Use a local source directory (skip download/extraction)
  --no-download      Skip downloading (a local archive will still be extracted)

Environment variables:
  KERNEL_UNAME=...   Override the kernel version (default: uname -r)
  CC=clang           Force Clang (auto-detection normally works)
  CC=gcc             Force GCC
  JOBS=N             Number of build jobs (default: nproc)

Examples:
  sudo ./install.sh
  sudo KERNEL_UNAME=6.12.1-cachyos ./install.sh       # override the kernel version
  sudo env CC=clang ./install.sh                     # force Clang
  sudo env CC=gcc ./install.sh                       # force GCC
  sudo JOBS=4 ./install.sh                           # limit build parallelism

For CachyOS kernels (Clang):
  sudo pacman -S clang llvm   # if Clang is not installed
  sudo ./install.sh           # the script will auto-detect Clang
EOF
            exit 0 ;;
        *) error "Unknown argument: $arg (use -h for help)"; exit 1 ;;
    esac
done

# ---------- 0. Environment checks ----------
echo ""
echo "======================================================================"
echo "  CMP 40HX — Compute + PCIe Gen2 Unlock Installer (Arch Linux / CachyOS)"
echo "  Driver version: ${DRIVER_VERSION} | Kernel: ${KERNEL_UNAME}"
echo "======================================================================"
echo ""

info "[1/6] Checking environment"

# Root privileges
[ "$(id -u)" -eq 0 ] || { error "Run this script with sudo"; exit 1; }

# Required tools
MISSING_TOOLS=()
for tool in make git sha256sum lspci; do
    command -v "${tool}" >/dev/null || MISSING_TOOLS+=("${tool}")
done

if [ ${#MISSING_TOOLS[@]} -gt 0 ]; then
    error "Missing tools: ${MISSING_TOOLS[*]}"
    echo "  Install them with: sudo pacman -S base-devel git pciutils"
    exit 1
fi

# Check for curl or wget
if ! command -v curl &>/dev/null && ! command -v wget &>/dev/null; then
    if [ "${USE_LOCAL_SRC}" -eq 0 ]; then
        error "Neither curl nor wget is installed. Install one with: sudo pacman -S curl"
        exit 1
    fi
fi

# Check kernel headers
if [ ! -d "${KERNEL_HDRS}" ]; then
    error "Kernel headers not found: ${KERNEL_HDRS}"
    echo ""
    echo "  Install the headers package matching your kernel:"
    echo ""
    echo "  Standard Arch kernel:             sudo pacman -S linux-headers"
    echo "  CachyOS (main):                    sudo pacman -S linux-cachyos-headers"
    echo "  CachyOS BORE:                      sudo pacman -S linux-cachyos-bore-headers"
    echo "  CachyOS LTO:                       sudo pacman -S linux-cachyos-lto-headers"
    echo "  CachyOS BMQ:                       sudo pacman -S linux-cachyos-bmq-headers"
    echo "  CachyOS EEVDF:                     sudo pacman -S linux-cachyos-eevdf-headers"
    echo "  CachyOS HARDENED:                  sudo pacman -S linux-cachyos-hardened-headers"
    echo ""
    echo "  Or run: pacman -Ss linux-headers | grep -E '(linux-cachyos|linux-headers)'"
    echo "  and choose the package matching your kernel ($(uname -r))."
    exit 1
fi
info "Kernel headers: OK (${KERNEL_HDRS})"

# Detect distribution
if [ -f /etc/cachyos-release ]; then
    DISTRO="CachyOS"
elif grep -q "Arch Linux" /etc/os-release 2>/dev/null; then
    DISTRO="Arch Linux"
else
    DISTRO="Arch-based"
fi
info "Distribution: ${DISTRO}"

# Check GPU
GPU_LINE="$(lspci -nn 2>/dev/null | grep -i 'nvidia' | grep -i '1f0b' | head -1 || true)"
if [ -n "${GPU_LINE}" ]; then
    info "CMP 40HX detected: ${GPU_LINE}"
else
    warn "CMP 40HX (10de:1f0b) not found in lspci — continuing (possibly another TU106 card)."
fi

# Check patch files
for patch in "${PATCH_FILES[@]}"; do
    [ -f "${patch}" ] || { error "Patch file not found: ${patch}"; exit 1; }
    info "Patch file: OK — $(basename "${patch}")"
done

# Check installed NVIDIA userspace version (expected: 580.173.02)
NVIDIA_USERSPACE_VER=""
if command -v nvidia-smi &>/dev/null; then
    NVIDIA_USERSPACE_VER="$(nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -1 || true)"
fi
if [ -z "${NVIDIA_USERSPACE_VER}" ]; then
    # Fall back to pacman
    NVIDIA_USERSPACE_VER="$(pacman -Q nvidia nvidia-open nvidia-dkms 2>/dev/null \
        | awk '{print $2}' | sed 's/-[0-9]*$//' | head -1 || true)"
fi

if [ -n "${NVIDIA_USERSPACE_VER}" ]; then
    if [[ "${NVIDIA_USERSPACE_VER}" == "${DRIVER_VERSION}"* ]]; then
        info "NVIDIA userspace version: ${NVIDIA_USERSPACE_VER} — compatible ✓"
    else
        warn "NVIDIA userspace version: ${NVIDIA_USERSPACE_VER}"
        warn "This patch targets ${DRIVER_VERSION}. A version mismatch may cause kernel crashes!"
        warn "Upgrade/downgrade NVIDIA to ${DRIVER_VERSION} before installing."
        echo ""
        read -r -p "Continue at your own risk? [y/N] " yn
        [[ "${yn,,}" == "y" ]] || { echo "Cancelled."; exit 0; }
    fi
else
    warn "Could not determine the NVIDIA userspace version — verify manually that ${DRIVER_VERSION} is installed"
fi

# Warn about Secure Boot
if command -v mokutil &>/dev/null; then
    SB_STATE="$(mokutil --sb-state 2>/dev/null || echo 'unknown')"
    if echo "${SB_STATE}" | grep -qi "enabled"; then
        warn "Secure Boot is enabled! The kernel module will not load unless it is signed."
        warn "Disable Secure Boot in UEFI or sign the module with your own key."
        echo ""
        read -r -p "Continue anyway? [y/N] " yn
        [[ "${yn,,}" == "y" ]] || { echo "Cancelled."; exit 0; }
    fi
fi

# ---------- 1. Obtain sources ----------
info "[2/6] Obtaining NVIDIA ${DRIVER_VERSION} sources"

if [ "${USE_LOCAL_SRC}" -eq 1 ] && [ -d "${SRC_DIR}" ]; then
    info "Using local source directory: ${SRC_DIR}"
else
    TARBALL=""

    # Look for an existing archive
    for t in "${LOCAL_TARBALLS[@]}"; do
        if [ -f "$t" ]; then
            TARBALL="$t"
            info "Found local archive: ${TARBALL}"
            break
        fi
    done

    # Download if no archive was found
    if [ -z "${TARBALL}" ]; then
        if [ "${USE_LOCAL_SRC}" -eq 1 ]; then
            error "--no-download was specified, but no archive was found in ${SCRIPT_DIR}"
            exit 1
        fi

        OUT="${SCRIPT_DIR}/open-gpu-kernel-modules-${DRIVER_VERSION}.tar.gz"
        info "Downloading from GitHub (~20 MB)..."

        if command -v curl &>/dev/null; then
            curl -L --fail --retry 3 --progress-bar "${DOWNLOAD_URL}" -o "${OUT}" \
                || { error "Download failed: ${DOWNLOAD_URL}"; exit 1; }
        else
            wget --show-progress -O "${OUT}" "${DOWNLOAD_URL}" \
                || { error "Download failed: ${DOWNLOAD_URL}"; exit 1; }
        fi
        TARBALL="${OUT}"
    fi

    # Verify SHA256
    info "Verifying SHA256..."
    actual="$(sha256sum "${TARBALL}" | awk '{print $1}')"
    if [ "${actual}" != "${SRC_SHA256}" ]; then
        error "SHA256 mismatch!"
        error "  Expected: ${SRC_SHA256}"
        error "  Actual:   ${actual}"
        error "The archive is corrupted or is the wrong version. Delete it and try again."
        exit 1
    fi
    info "SHA256: OK"

    # Extract archive
    info "Extracting..."
    rm -rf "${SRC_DIR}"
    case "${TARBALL}" in
        *.tar.gz|*.tgz) tar xzf "${TARBALL}" -C "${SCRIPT_DIR}" ;;
        *.tar.xz)       tar xJf "${TARBALL}" -C "${SCRIPT_DIR}" ;;
        *.tar.bz2)      tar xjf "${TARBALL}" -C "${SCRIPT_DIR}" ;;
        *) error "Unknown archive format: ${TARBALL}"; exit 1 ;;
    esac

    # Find the extracted source directory (the name may differ)
    if [ ! -d "${SRC_DIR}" ]; then
        found="$(find "${SCRIPT_DIR}" -maxdepth 1 -type d -name '*580.173.02*' | head -1)"
        if [ -n "${found}" ]; then
            SRC_DIR="${found}"
        else
            error "Source directory not found after extraction"
            exit 1
        fi
    fi
fi

[ -d "${SRC_DIR}/kernel-open" ] || {
    error "Invalid source tree: ${SRC_DIR}"
    error "Expected a kernel-open/ subdirectory"
    exit 1
}
info "Sources ready: ${SRC_DIR}"

# ---------- 2. Apply patches ----------
info "[3/6] Applying unlock patches"
cd "${SRC_DIR}"

# Refuse to overwrite local source-tree changes.
# This avoids silently destroying manual edits when rerunning the installer.
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    if ! git diff --quiet --ignore-submodules -- . 2>/dev/null || ! git diff --cached --quiet --ignore-submodules -- . 2>/dev/null; then
        error "The NVIDIA source tree contains local changes."
        error "Commit/stash/remove them before rerunning the installer."
        exit 1
    fi
fi

for patch in "${PATCH_FILES[@]}"; do
    patch_name="$(basename "${patch}")"
    info "Applying ${patch_name}"

    if git apply --check "${patch}" 2>/dev/null; then
        git apply "${patch}"
        info "${patch_name}: applied successfully"
    elif git apply --reverse --check "${patch}" 2>/dev/null; then
        info "${patch_name}: already applied, skipping"
    else
        error "${patch_name}: failed to apply"
        error "The source version or patch contents may not match"
        error "Expected NVIDIA open-gpu-kernel-modules ${DRIVER_VERSION}"
        exit 1
    fi
done

# ---------- 3. Build ----------
info "[4/6] Building kernel modules (5–15 minutes)"

JOBS="${JOBS:-$(nproc)}"
info "Using ${JOBS} build jobs"

# --- Detect the kernel compiler (CachyOS commonly uses Clang/LLVM) ---
#
# Building a module with GCC when the kernel was built with Clang may cause errors
# or ABI mismatches. Detect it via
# /proc/version and the kernel config, or allow manual override via
# the CC environment variable.
#
EXTRA_MAKE_FLAGS=""

# Allow manual compiler selection through the environment: sudo env CC=gcc ./install.sh
FORCE_CC="${CC:-}"

if [ -n "${FORCE_CC}" ]; then
    info "Compiler forced via environment: CC=${FORCE_CC}"
    if [[ "${FORCE_CC}" == *"clang"* ]]; then
        command -v clang >/dev/null || { error "clang is not installed"; echo "  Install it with: sudo pacman -S clang llvm"; exit 1; }
        EXTRA_MAKE_FLAGS="CC=clang HOSTCC=clang LD=ld.lld LLVM=1"
    else
        command -v "${FORCE_CC}" >/dev/null || { error "Compiler not found: ${FORCE_CC}"; exit 1; }
        EXTRA_MAKE_FLAGS="CC=${FORCE_CC} HOSTCC=${FORCE_CC}"
    fi
else
    # Auto-detect the compiler used for the running kernel
    KERNEL_COMPILER=""
    if grep -qi "clang" /proc/version 2>/dev/null; then
        KERNEL_COMPILER="clang"
    elif [ -f "${KERNEL_HDRS}/.config" ] && grep -q "CONFIG_CC_IS_CLANG=y" "${KERNEL_HDRS}/.config" 2>/dev/null; then
        KERNEL_COMPILER="clang"
    elif [ -f "${KERNEL_HDRS}/include/config/auto.conf" ] && grep -q "CONFIG_CC_IS_CLANG=y" "${KERNEL_HDRS}/include/config/auto.conf" 2>/dev/null; then
        KERNEL_COMPILER="clang"
    fi

    if [ "${KERNEL_COMPILER}" = "clang" ]; then
        if command -v clang &>/dev/null; then
            CLANG_VER="$(clang --version | head -1)"
            info "Kernel was built with Clang — using Clang for the module"
            info "  ${CLANG_VER}"
            EXTRA_MAKE_FLAGS="CC=clang HOSTCC=clang LD=ld.lld LLVM=1"
        else
            error "Kernel was built with Clang, but clang is not installed!"
            echo "  Install it with: sudo pacman -S clang"
            exit 1
        fi
    else
        command -v gcc >/dev/null || { error "gcc is not installed"; echo "  Install it with: sudo pacman -S gcc"; exit 1; }
        info "Kernel compiler: GCC — using GCC for the module"
    fi
fi

if [ -n "${EXTRA_MAKE_FLAGS}" ]; then
    info "Additional make flags: ${EXTRA_MAKE_FLAGS}"
fi

# On Arch/CachyOS, pass KERNEL_UNAME and SYSSRC explicitly
# shellcheck disable=SC2086
make -j"${JOBS}" modules \
    KERNEL_UNAME="${KERNEL_UNAME}" \
    SYSSRC="${KERNEL_HDRS}" \
    ${EXTRA_MAKE_FLAGS} \
    || { error "Build failed — see the output above"; exit 1; }

info "Build completed successfully"

# ---------- 4. Install ----------
info "[5/6] Installing modules"

INSTALL_DIR="/lib/modules/${KERNEL_UNAME}/updates/cmpunlocker"
mkdir -p "${INSTALL_DIR}"

INSTALLED=0
for ko in nvidia nvidia-uvm nvidia-modeset nvidia-drm nvidia-peermem; do
    if [ -f "kernel-open/${ko}.ko" ]; then
        cp "kernel-open/${ko}.ko" "${INSTALL_DIR}/"
        INSTALLED=$((INSTALLED + 1))
    fi
done

if [ "${INSTALLED}" -eq 0 ]; then
    error "No .ko files found in kernel-open/"
    exit 1
fi
info "Installed modules: ${INSTALLED}"

# Updating the module dependency database
depmod -a "${KERNEL_UNAME}"
info "depmod -a completed"

# ---------- 5. Save build artifacts ----------
info "[6/6] Saving build artifacts"
mkdir -p "${ARTIFACTS_DIR}"
cp "${INSTALL_DIR}"/*.ko "${ARTIFACTS_DIR}/" 2>/dev/null || true
info "Artifacts saved to: ${ARTIFACTS_DIR}"

# ---------- Summary ----------
echo ""
echo -e "${GRN}======================================================================"
echo "  SUCCESS: CMP 40HX unlock modules installed"
echo -e "======================================================================${NC}"
echo ""
echo "  Install directory: ${INSTALL_DIR}"
echo ""
echo -e "${YEL}  NEXT STEP: run sudo mkinitcpio -P (Arch Linux), then perform a COLD REBOOT (remove power; do not just reboot)"
echo -e "${NC}    sudo shutdown -h now"
echo ""
echo "  After boot — verify with:"
echo "    sudo dmesg | grep CMP40"
echo "    Expected: CMP40_COMPUTE_UNLOCK_V525: ... SS0=0x88888888 SS1=0x00000008"
echo ""
echo "  Uninstall (restore the official driver):"
echo "    sudo rm -rf /lib/modules/\$(uname -r)/updates/cmpunlocker"
echo "    sudo depmod -a"
echo "    Reinstall the NVIDIA driver (nvidia-dkms / repository package)"
echo ""
echo -e "${YEL}  IMPORTANT for Arch/CachyOS:"
echo "    After updating the kernel through pacman, rebuild and reinstall both patches:"
echo "      sudo ./install.sh --no-download"
echo "    (the script will reapply the patches and rebuild for the new kernel)"
echo -e "${NC}"
echo "======================================================================"
