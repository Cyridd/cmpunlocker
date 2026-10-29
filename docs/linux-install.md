# Linux installation guide

This guide covers the Linux half of the project: open-kernel-module patch
sets for NVIDIA `580.173.02`, `610.57.04` and `615.78.08` (default), installed
by [`install.sh`](../install.sh), plus the optional userspace pipeline patch in
[`cmp_glcore_patch/`](../cmp_glcore_patch/README.md).

For the Windows/UEFI procedure, see [`windows/README.md`](../windows/README.md) instead.

## 1. Prepare the environment

The installer is distribution-agnostic: it does not install packages or
require a specific package manager. Install the prerequisites with the package
manager used by your distribution, then run the same `install.sh` command.
You need a matching kernel headers/devel package, a C toolchain, `make`, GNU
`patch`, `tar`, `sha256sum`, `pciutils`, `kmod`/`depmod`, and `curl` or `wget`
if the NVIDIA source is downloaded automatically. The installed NVIDIA
userspace and firmware must match the selected driver version: `580.173.02`,
`610.57.04` or `615.78.08`.
The distribution's NVIDIA installation must provide a compatible open kernel
module stack; this project does not install the NVIDIA driver or firmware.

### Arch Linux / CachyOS

```bash
sudo pacman -S --needed base-devel pciutils patch curl \
  ca-certificates tar gzip xz bzip2 kmod bc flex bison libelf openssl

# Standard Arch kernel:
# sudo pacman -S linux-headers
# For CachyOS, install the headers matching the running kernel instead:
# sudo pacman -S linux-cachyos-headers
# sudo pacman -S linux-cachyos-bore-headers
# sudo pacman -S linux-cachyos-lto-headers
```

### Ubuntu / Debian

```bash
sudo apt update
sudo apt install build-essential linux-headers-$(uname -r) pciutils patch \
  curl ca-certificates tar gzip xz-utils bzip2 kmod initramfs-tools \
  bc flex bison libelf-dev libssl-dev
```

### Fedora / RHEL / Rocky / AlmaLinux

```bash
sudo dnf group install "Development Tools"
sudo dnf install kernel-devel-$(uname -r) kernel-headers \
  pciutils patch curl ca-certificates tar gzip xz bzip2 kmod dracut \
  bc flex bison elfutils-libelf-devel openssl-devel
```

### openSUSE

```bash
sudo zypper install -t pattern devel_basis pciutils patch curl \
  ca-certificates tar gzip xz bzip2 kmod dracut bc flex bison \
  libelf-devel libopenssl-devel
# Also install the -devel package matching the running kernel flavor,
# for example kernel-default-devel.
```

Other Linux distributions need the equivalent packages. The installer only
requires the standard Linux module build layout and accepts a non-standard
kernel build directory through `KERNEL_HDRS`.
The unlock itself is hardware- and driver-version-specific; the commands
above cover common packaging layouts, but distro kernels and NVIDIA packages
still need to be tested on the target system.

Verify that the 40HX is detected:

```bash
lspci -nn | grep -i nvidia   # expected: 10de:1f0b
```

## 2. Obtain the source code

**Option A — automatic download (recommended):**

`install.sh` downloads `open-gpu-kernel-modules` for the selected driver version
(`615.78.08` by default) from GitHub and verifies its SHA256.

Select another supported version with `--driver-version=VER` or the
`NVIDIA_VERSION` environment variable:

```bash
sudo ./install.sh --driver-version=610.57.04
sudo env NVIDIA_VERSION=580.173.02 ./install.sh
```

**Option B — place a local archive:**

If you do not want the script to download the source automatically, place one of the following archives next to `install.sh` (replace `615.78.08` with the selected version):

- `open-gpu-kernel-modules-615.78.08.tar.gz`
- `NVIDIA-615.78.08.tar.xz`
- `NVIDIA-kernel-module-source-615.78.08.tar.xz`
- `NVIDIA-kernel-module-source-615.78.08.tar.bz2`

The official GitHub `.tar.gz` archives are verified automatically:

| Version | SHA256 |
|---|---|
| `580.173.02` | `a2cd41cf100a81d90de9d5ca192b828ed8a63408330acef7931df00487acd82f` |
| `610.57.04` | `619d7b5ce1f79c3211afdbf87d02b2174d268b10d005c5b8f994be22299be681` |
| `615.78.08` | `eac0ecd52facab28ad2963cf7e03ae8d2289d596ad590c703b5f72d65899e2bf` |

For a differently named or compressed local archive, provide its own digest
explicitly; the installer will not silently accept an unverified archive:

```bash
sudo env SOURCE_SHA256=<sha256-of-your-archive> ./install.sh --no-download
```

## 3. Install

```bash
chmod +x install.sh
sudo ./install.sh
```

The installer performs:

`download → SHA256 verification → patch application → kernel module build → installation`

Patches are applied with GNU `patch -p1`; the NVIDIA source is treated as a
source tree, not as a Git checkout. This also works when the source directory
is located inside another Git repository.

The patched modules are installed under:

`/lib/modules/$(uname -r)/updates/cmpunlocker/`

The installer applies the patch set for the selected version from
`patches/<version>/`:

- `0001-cmp40hx-unlock.patch`
- `0002-cmp40hx-pcie2-unlock.patch` (or `0002-cmp40hx-pcie2-diagnostic.patch` with `--pcie-diagnostic`)
- `0003-cmp40hx-rebar-unlock.patch`
- Optional `0004-cmp40hx-profiling-unlock.patch` with `--profiling` (615.78.08
  only; CUPTI support gate confirmed, counter collection still unverified).

The diagnostic PCIe patch is available for `610.57.04` and `615.78.08`; the
`580.173.02` set has no diagnostic variant.

This installs only the kernel-module patches. It does not apply the optional `cmp_glcore_patch` userspace modification.
For the optional profiling patch and CUPTI checks, see
[`CMP40_PROFILING.md`](research/CMP40_PROFILING.md).
It also does not install or replace the NVIDIA userspace driver or firmware;
install a matching NVIDIA stack through your distribution first.

Useful options and environment overrides:

```bash
sudo ./install.sh --driver-version=615.78.08
sudo ./install.sh --pcie-diagnostic
sudo ./install.sh --profiling
sudo ./install.sh --source-dir=/absolute/path/to/open-gpu-kernel-modules-615.78.08
sudo ./install.sh --no-download
sudo env KERNEL_UNAME=6.12.1-custom KERNEL_HDRS=/path/to/kernel/build ./install.sh
sudo env JOBS=4 INITRAMFS_TOOL=dracut ./install.sh
```

`KERNEL_HDRS` defaults to `/lib/modules/$(uname -r)/build` and then
`/usr/lib/modules/$(uname -r)/build`. `INITRAMFS_TOOL=auto` follows the
distribution configuration: it uses `mkinitcpio` when its configuration is
present, `update-initramfs` on Debian-style systems, and otherwise `dracut`
when available (or `mkinitrd` on systems that provide only that command).
Use `INITRAMFS_TOOL=none` only when you will rebuild the initramfs yourself.
`JOBS` controls parallel compilation and must be a positive integer.
`--no-download` uses an existing source directory next to the installer or a
local source archive; the archive is still SHA256-verified.

## 4. Cold reboot (required)

```bash
sudo shutdown -h now
```

A full power-off is recommended. Do not rely on a simple warm reboot when validating the unlock. If an earlier experimental build containing the Link Disable sequence was installed, reinstall the current patch and perform a full cold power-off before testing again.

## 5. Verify

```bash
sudo dmesg | grep CMP40
```

For the compute unlock, expect messages containing:

```text
SS0=0x88888888
SS1=0x00000008
FECS_PLM=0xffffff8f
```

For the PCIe unlock:

```bash
sudo lspci -vvv -s 10:00.0 | grep -iE 'LnkCap|LnkSta|LnkCtl2'
```

Expected:

```text
LnkSta: Speed 5GT/s, Width x16
LnkCtl2: Target Link Speed: 5GT/s
```

Also verify the GPU is usable:

```bash
nvidia-smi
```

If the diagnostic log shows `CAP2=00000002` after cold boot and the GPU is
otherwise healthy, the optional `.04` recovery experiment can be run from a
root shell after stopping GPU clients:

```bash
sudo ./tools/cmp40hx-driver-reprobe-gen2.sh --confirm-driver-reprobe
```

This resets the NVIDIA driver for the selected GPU. A disabled-by-default
experimental systemd unit is provided separately; neither the helper nor the
unit is installed by `install.sh`. Review the warnings and full procedure in
[`research/PCIE_GEN2_DIAGNOSTIC.md`](research/PCIE_GEN2_DIAGNOSTIC.md) before use.

## Optional: pipeline throttle unlock (userspace)

`install.sh` does **not** touch the userspace driver. If games on the GSP-enabled
stack are slow, generate the local patched `libnvidia-glcore.so` as described in
[`cmp_glcore_patch/README.md`](../cmp_glcore_patch/README.md); it is loaded per
application through `LD_LIBRARY_PATH` and never replaces the system library.

## Removing the patched modules

```bash
sudo ./install.sh --uninstall
```

The installer records what it installed (module names and SHA-256 hashes, the
driver version, and the patch hashes) in
`/var/lib/cmp40hx/install-<kernel>.manifest`. `--uninstall` removes exactly
those files, then runs `depmod` and rebuilds the initramfs. Installs made by
older versions have no manifest; for those the installer lists the files it
found and refuses to delete anything unless you pass `--force`, so it cannot
remove modules that another tool put next to them.

To check an installation without changing anything:

```bash
sudo ./install.sh --verify
```

This compares the installed modules against the manifest and reports any file
that is missing or does not match.

Then reinstall the official NVIDIA driver package if necessary.

On Windows, run `40HXUninstaller.exe` (or `40HXInstaller.exe -uninstall`) as
Administrator instead; it removes the components and restores the boot entry.
