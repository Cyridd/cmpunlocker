# CMP 40HX - Compute, PCIe, ReBAR & Vulkan Pipeline Unlocks (NVIDIA Linux 610.57.04)

Linux hardware-research project for the NVIDIA CMP 40HX (TU106, PCI device ID `10de:1f0b`). It contains three open-kernel-module patches and one optional proprietary-userspace patch.

The project currently provides four independent unlocks:

- **Compute / SM configuration unlock** - changes the protected GSP/SEC2 initialization state used by the tested compute paths.
- **PCIe Gen2 x16 unlock** — raises the link from the stock PCIe Gen1 x16 (2.5 GT/s) to PCIe Gen2 x16 (5 GT/s) using the GSP/RM policy path plus a real link retrain.
- **Resizable BAR unlock** — enables an 8 GiB BAR1 aperture on the CMP 40HX.
- **Pipeline bind / MME throttle unlock** - eliminates the artificial delay executed on classic `vkCmdBindPipeline` paths by patching the NVIDIA userspace `libnvidia-glcore.so` emitter.

None of these modifications changes the VBIOS or video memory. The compute, PCIe and ReBAR unlocks patch the NVIDIA open kernel module. The pipeline unlock is separate and patches a local copy of a proprietary userspace library.

## Important distinction: compute is not raster graphics

Earlier versions of this README described the pre-unlock `~0.39 TFLOPS` measurements as if the CMP 40HX had a global FP16/FP32 lock. That conclusion was too broad.

- The `~0.39 TFLOPS` values came from specific compute benchmarks and execution paths. They are not a measurement of all FP16/FP32 work performed by the GPU.
- Ordinary raster games already ran at normal performance before the compute patch, so their regular graphics-shader FP32/FP16 execution was not globally limited to `~0.39 TFLOPS`.
- The compute patch changes protected SM/security initialization state and restores performance or availability in the tested compute and Tensor Core paths.
- The severe GSP-enabled gaming slowdown investigated by this project was a separate classic Vulkan pipeline-bind throttle. Its current bypass is the optional `cmp_glcore_patch` userspace patch, not the compute patch.

## Results

### Compute unlock

Measured on the tested CMP 40HX system:

| Tested workload/path | Before unlock | After unlock |
|---|---:|---:|
| FP16 cuBLAS benchmark | ~0.39 TFLOPS | **11.42 TFLOPS** |
| FP32 compute benchmark | ~0.39 TFLOPS | **7.0 TFLOPS** |
| FP16 Tensor Core MMA benchmark | Unavailable in the tested configuration | **63.8 TFLOPS** |

These numbers describe the named compute tests on one system. In particular, the pre-unlock values must not be interpreted as the total FP32/FP16 capability available to graphics workloads.

### PCIe Gen2 unlock

Verified on real hardware:

| Metric | Stock | After unlock |
|---|---|---|
| PCIe link | **Gen1 x16 (2.5 GT/s)** | **Gen2 x16 (5 GT/s)** |
| `LnkCap` / `LnkSta` | 2.5 GT/s x16 | **5 GT/s x16** |
| FurMark average FPS (one test setup) | ~110 | **~120** |

The PCIe result is a real trained link state, not just a spoofed capability value.

## How it works

### Compute unlock

The tested CMP 40HX exposes a restricted SM/security configuration during GSP/SEC2 initialization. The patch injects a payload into the standard SEC2 Booter flow and, during its privileged execution phase, restores the state used by the validated compute workloads:

- `SS0 = 0x88888888`
- `SS1 = 0x00000008`
- `FECS_PLM = 0xFFFFFF8F`
- required `WPR2` / `SEC2 RESET_PLM` state

Expected driver log:

`CMP40_COMPUTE_UNLOCK_V525: ... SS0=0x88888888 SS1=0x00000008 FECS_PLM=0xffffff8f`

### PCIe Gen2 unlock

The CMP 40HX is restricted to PCIe Gen1 x16 (2.5 GT/s) by default.

The PCIe patch uses a protected GSP/RM policy path to enable the higher PCIe link rate, then performs an actual PCIe link retrain. It sets the Gen2 target on both ends and asserts Retrain Link on the upstream bridge. This normal retrain is the supported kernel path in this repository. A separate Link Disable experiment is documented in `PCIE_LINK_DISABLE_AUDIT.md`; it is not integrated because it can detach GSP/RM after the driver has initialized. The resulting hardware state is:

```text
LnkSta: Speed 5GT/s, Width x16
LnkCap2: Supported Link Speeds: 2.5-5GT/s
LnkCtl2: Target Link Speed: 5GT/s
```

This is a genuine PCIe Gen2 x16 link.

The normal sequence is verified on the maintainer's AMD root port. On Intel
Alder Lake, a manual Link Disable experiment can train the physical link to
Gen2 x16 but can also leave `nvidia-smi` unable to access the GPU; that
platform-specific experiment is intentionally excluded from the kernel patch.

> PCIe Gen3 is **not** currently implemented by this project. Work on higher CMP models may provide a future reference for a Gen3 port.

### Resizable BAR unlock

The patch configures the required XVE registers and BAR1 size selector, then lets the NVIDIA driver perform its normal PCI BAR resizing.

The current configuration uses selector 7, corresponding to an 8 GiB BAR1 aperture.

The resulting hardware and driver state is:

```text
BAR 1: current size: 8GB
BAR1 Memory Usage
    Total : 8192 MiB
```
---

## Quick Start

### 1. Prepare the environment

The installer is distribution-agnostic: it does not install packages or
require a specific package manager. Install the prerequisites with the package
manager used by your distribution, then run the same `install.sh` command.
You need a matching kernel headers/devel package, a C toolchain, `make`, GNU
`patch`, `tar`, `sha256sum`, `pciutils`, `kmod`/`depmod`, and `curl` or `wget`
if the NVIDIA source is downloaded automatically. The installed NVIDIA
userspace and firmware must be version `610.57.04`.
The distribution's NVIDIA installation must provide a compatible open kernel
module stack; this project does not install the NVIDIA driver or firmware.

#### Arch Linux / CachyOS

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

#### Ubuntu / Debian

```bash
sudo apt update
sudo apt install build-essential linux-headers-$(uname -r) pciutils patch \
  curl ca-certificates tar gzip xz-utils bzip2 kmod initramfs-tools \
  bc flex bison libelf-dev libssl-dev
```

#### Fedora / RHEL / Rocky / AlmaLinux

```bash
sudo dnf group install "Development Tools"
sudo dnf install kernel-devel-$(uname -r) kernel-headers \
  pciutils patch curl ca-certificates tar gzip xz bzip2 kmod dracut \
  bc flex bison elfutils-libelf-devel openssl-devel
```

#### openSUSE

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

### 2. Obtain the source code

**Option A — automatic download (recommended):**

`install.sh` downloads `open-gpu-kernel-modules` 610.57.04 from GitHub and verifies its SHA256.

**Option B — place a local archive:**

If you do not want the script to download the source automatically, place one of the following archives next to `install.sh`:

- `open-gpu-kernel-modules-610.57.04.tar.gz`
- `NVIDIA-610.57.04.tar.xz`
- `NVIDIA-kernel-module-source-610.57.04.tar.xz`
- `NVIDIA-kernel-module-source-610.57.04.tar.bz2`

The official GitHub `.tar.gz` archive is verified automatically:

`619d7b5ce1f79c3211afdbf87d02b2174d268b10d005c5b8f994be22299be681`

For a differently named or compressed local archive, provide its own digest
explicitly; the installer will not silently accept an unverified archive:

```bash
sudo env SOURCE_SHA256=<sha256-of-your-archive> ./install.sh --no-download
```

### 3. Install

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

The installer applies:

- `0001-cmp40hx-unlock.patch`
- `0002-cmp40hx-pcie2-unlock.patch`
- `0003-cmp40hx-rebar-unlock.patch`

This installs only the three kernel-module patches. It does not apply the optional `cmp_glcore_patch` userspace modification.
It also does not install or replace the NVIDIA userspace driver or firmware;
install a matching `610.57.04` NVIDIA stack through your distribution first.

Useful options and environment overrides:

```bash
sudo ./install.sh --pcie-diagnostic
sudo ./install.sh --source-dir=/absolute/path/to/open-gpu-kernel-modules-610.57.04
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

### 4. Cold reboot (required)

```bash
sudo shutdown -h now
```

A full power-off is recommended. Do not rely on a simple warm reboot when validating the unlock. If an earlier experimental build containing the Link Disable sequence was installed, reinstall the current patch and perform a full cold power-off before testing again.

### 5. Verify

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

## Removing the patched modules

```bash
sudo rm -rf /lib/modules/$(uname -r)/updates/cmpunlocker
sudo depmod -a
```

Then reinstall the official NVIDIA driver package if necessary.

## Compatibility and known issues

- **NVIDIA open-gpu-kernel-modules 610.57.04 only.** Other driver versions require porting and revalidation.
- The project has been tested on CachyOS system with 7.2.0-1-cachyos kernel.
- The compute unlock changes the GSP/SEC2 boot payload. The VBIOS and VRAM are not modified.
- The PCIe patch changes the GSP/RM PCIe policy and retrains the link. It does not modify the VBIOS.
- **PCIe Gen2 x16 is verified.**
- **PCIe Gen3 is not currently unlocked.**
- CMP 40HX has no normal display outputs. Graphics use therefore requires a suitable headless, secondary-GPU, remote-display or similar setup.
- Secure Boot must be disabled or the custom kernel modules must be signed with a trusted key.
- After a kernel update, rebuild and reinstall the patched modules.

For example:

```bash
sudo ./install.sh --no-download
```

## File structure

| File | Description |
|---|---|
| `0001-cmp40hx-unlock.patch` | Compute / SM unlock for NVIDIA 610.57.04 |
| `0002-cmp40hx-pcie2-unlock.patch` | PCIe Gen2 x16 unlock |
| `0002-cmp40hx-pcie2-diagnostic.patch` | Verbose replacement for `0002`; captures XVE/BAR0 and PCI capability state |
| `0003-cmp40hx-rebar-unlock.patch` | 8 GiB Resizable BAR unlock |
| `PCIE_GEN2_DIAGNOSTIC.md` | Diagnostic installation, collection and interpretation guide |
| `PCIE_LINK_DISABLE_AUDIT.md` | Evidence and limitations of the Link Disable experiment |
| `tools/cmp40hx-alder-lake-link-disable-test.sh` | Manual, explicitly-confirmed research reproducer |
| `cmp_glcore_patch/` | Userspace Vulkan pipeline/MME throttle unlock for `libnvidia-glcore.so.610.57.04` |
| `CMP40_GSP_PIPELINE_THROTTLE_FINDINGS.md` | Reproducible evidence and reverse-engineering notes for the pipeline throttle |
| `install.sh` | Build and installation script |
| `README.md` | This document |

The compute unlock mainly modifies the GSP/SEC2 initialization path.

The PCIe unlock extends the GSP/RM PCIe policy and adds a host-side retrain path to bring the link up at 5 GT/s x16.

## Technical summary

### Compute unlock

The tested CMP 40HX exposes a restricted SM/security configuration. The patch injects a custom payload into the SEC2 Booter flow and uses privileged HS execution to restore the FECS/SM state exercised by the compute benchmarks.

Key values:

```text
SS0      = 0x88888888
SS1      = 0x00000008
FECS_PLM = 0xFFFFFF8F
```

These writes cannot be reliably replaced with normal `setpci` or `devmem` writes because the relevant registers are protected during the secure initialization path.

### PCIe Gen2 unlock

The CMP 40HX is stock-limited to PCIe Gen1 x16.

The PCIe patch:

1. opens the required GSP/RM PCIe policy;
2. requests a 5 GT/s link;
3. retrains the endpoint/upstream bridge;
4. verifies the resulting `LnkSta`.

The validated final state is:

```text
Speed 5GT/s
Width x16
```

One test setup improved from roughly 110 FPS to roughly 120 FPS in FurMark. This is an observed workload result, not a guaranteed consequence of Gen2 on every system.

### Resizable BAR unlock

Verified on real hardware:

| Metric | Stock | After unlock |
|---|---|---|
| BAR1 size | 64 MiB | **8 GiB** |
| `lspci` BAR 1 | 64 MB | **8 GB** |
| NVIDIA `BAR1 Total` | 64 MiB | **8192 MiB** |

The 8 GiB BAR1 aperture is exposed to the NVIDIA driver and was usable by applications in the tested system. For example, War Thunder was observed using approximately 80 MiB of BAR1 memory in the main menu.

A FurMark test on the same system showed a small change from approximately 120 FPS to **~122 FPS** with ReBAR enabled. This result is setup-dependent and should not be treated as a general ReBAR performance claim.

### Pipeline bind / MME throttle unlock

The CMP 40HX also has a separate userspace performance restriction affecting classic Vulkan pipeline binding.

The reproduced trigger is the classic pipeline path. The shader-object binding diagnostic did not reproduce the same delay, so this README does not claim a global shader-execution throttle.

The throttle was identified experimentally in the NVIDIA userspace driver. Each classic `vkCmdBindPipeline` path invokes:

```text
NVC597_CALL_MME_MACRO(52), argument 0xf0
```

The selected MME macro executes a repeated sequence of:

```text
NVC597_PIPE_NOP
NVC597_WAIT_FOR_IDLE
```

for 240 iterations. The important point is that the slowdown is caused by the **combination** of the two operations in the MME macro, not by either operation in isolation.

The relevant emitters were located in:

```text
/usr/lib/libnvidia-glcore.so.610.57.04
```

at file offsets:

```text
0xb20c2d
0xdcbf60
```

The supplied patcher changes only the emitted MME loop argument from `0xf0` (240) to a user-selected DWORD. The bundled library uses `0`, retaining the macro call and command shape while eliminating all 240 delay iterations.

Verified results:

| Test | Stock | After pipeline unlock |
|---|---:|---:|
| 4 binds | ~0.739 ms | **~0.0023 ms** (argument 0) |
| 1000 binds | ~183.3 ms | **~0.0054 ms** (argument 0) |
| 1000 binds speed-up | 1× | **~33,700x** (argument 0) |

The argument-0 value is the bundled-library configuration. Three follow-up
runs measured `0.005248`, `0.005440` and `0.005568 ms` (median
`0.005440 ms`). The ratio is a GPU timestamp microbenchmark result: it shows
that the artificial loop has effectively disappeared, and is not a claim that
every application will become 33,700 times faster.

The unlock was additionally validated in real applications:

- FurMark improved from approximately **131 FPS average / 134 FPS max** to **134 FPS average / 137 FPS max** in one tested configuration.
- War Thunder native Vulkan reached approximately **90 FPS average during gameplay at Ultra with DLAA 4**.
- Cyberpunk 2077 through Proton-CachyOS reached approximately **66 FPS average in the benchmark at High settings with DLSS 4 Quality**.

Before the userspace pipeline fix, the affected game configurations showed a very large slowdown, reported as up to roughly 10-15x. The application figures above are observations from one system, not universal performance guarantees.

### How the pipeline unlock works

Unlike the compute, PCIe and ReBAR patches, this unlock does **not** modify the open kernel module.

The restriction is present in the proprietary userspace component:

```text
libnvidia-glcore.so.610.57.04
```

The supplied `cmp_glcore_patch` directory contains:

```text
cmp_glcore_patch/
├── libnvidia-glcore.so.610.57.04   # patched library
├── patch_glcore                    # patcher
├── patch_glcore.cpp                # patcher source
└── README.md                       # dedicated installation / usage instructions
```

The supplied patched library can be tested locally without replacing the system copy. Follow the instructions in `cmp_glcore_patch/README.md` for installation and rollback.

See [cmp_glcore_patch/README.md](cmp_glcore_patch/README.md) for usage and [CMP40_GSP_PIPELINE_THROTTLE_FINDINGS.md](CMP40_GSP_PIPELINE_THROTTLE_FINDINGS.md) for the command-level evidence.

Important:

- This unlock is currently specific to **`libnvidia-glcore.so.610.57.04`**.
- The validated patch signatures cover the 64-bit library only.
- Other NVIDIA driver versions require new emitter signatures and revalidation.
- NVIDIA 32-bit userspace components require separate signatures / patching.
- Do not replace the system `/usr/lib/libnvidia-glcore.so.*`; use the local library through `LD_LIBRARY_PATH` as documented in `cmp_glcore_patch/README.md`.
- The kernel-module unlocks (`0001`–`0003`) are independent of this userspace patch and are not modified by it.

## Technical summary: pipeline throttle unlock

The classic Vulkan pipeline bind path in the tested NVIDIA userspace driver emits:

```text
NVC597_CALL_MME_MACRO(52), argument 0xf0
```

The corresponding MME code performs 240 `PIPE_NOP` + `WAIT_FOR_IDLE` pairs. Microbenchmarks demonstrated that changing the emitter argument from 240 to 0 removes the dominant bind overhead while leaving the actual pipeline bind functionality intact in the tested applications.

The patch is applied to the two identified emitter locations in `libnvidia-glcore.so.610.57.04`:

```text
0xb20c2d
0xdcbf60
```

This unlock is therefore a **userspace Vulkan command-generation patch**, not a GSP/SEC2 hardware-security unlock.

## Disclaimer
- This project is intended for hardware research and experimentation.
- Use it at your own risk.
- Modified kernel modules or NVIDIA userspace libraries may cause driver initialization failures, application crashes, or system instability.
- The pipeline throttle unlock modifies `libnvidia-glcore.so.610.57.04`; keep an untouched copy of the original library for rollback.
- NVIDIA licensing, warranty, and support terms may be affected.
- Keep a way to boot without the patched modules so the official driver can be restored.

## Licensing

This project contains code derived from multiple authors:

- Compute Unlock — originally developed by @sbccc1888 (https://github.com/sbccc1888/cmpunlocker).
  Licensed under the MIT License. Original attribution is preserved.
- PCIe / ReBAR patches — ported from @xrip (https://github.com/xrip/cmp50hx-unlock).
  The original source did not specify a license; these portions are therefore
  not relicensed under this project's MIT License.
- NVIDIA open-gpu-kernel-modules remains subject to NVIDIA's applicable open-source license terms.
- CMP 40HX pipeline throttle unlock and related modifications —
  Copyright (c) 2026 Cyridd, licensed under the MIT License.
