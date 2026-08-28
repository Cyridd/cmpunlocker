# CMP 40HX — Compute, PCIe & Resizable BAR Unlock (NVIDIA Linux Driver 610.57.04)

Linux unlock project for the NVIDIA CMP 40HX (TU106, PCI device ID `10de:1f0b`).

The project currently provides four independent unlocks:

- **Compute / SM performance unlock** — restores full SM issue rate and compute performance.
- **PCIe Gen2 x16 unlock** — raises the link from the stock PCIe Gen1 x16 (2.5 GT/s) to PCIe Gen2 x16 (5 GT/s) using the GSP/RM policy path plus a real link retrain.
- **Resizable BAR unlock** — enables an 8 GiB BAR1 aperture on the CMP 40HX.
- **Pipeline bind / MME throttle unlock** — removes the artificial delay executed on classic `vkCmdBindPipeline` paths by patching the NVIDIA userspace `libnvidia-glcore.so`.

The unlock is implemented in the NVIDIA open kernel module driver and does not modify the VBIOS or video memory.

## Results

### Compute unlock

Verified on real hardware:

| Metric | Before unlock | After unlock |
|---|---:|---:|
| FP16 (cuBLAS) | ~0.39 TFLOPS | **11.42 TFLOPS** |
| FP32 | ~0.39 TFLOPS | **7.0 TFLOPS** |
| FP16 Tensor Core (mma) | Disabled | **63.8 TFLOPS** |
| VRAM | 8 GB | 8 GB |

### PCIe Gen2 unlock

Verified on real hardware:

| Metric | Stock | After unlock |
|---|---|---|
| PCIe link | **Gen1 x16 (2.5 GT/s)** | **Gen2 x16 (5 GT/s)** |
| `LnkCap` / `LnkSta` | 2.5 GT/s x16 | **5 GT/s x16** |
| FurMark average FPS | ~110 | **~120** |

The PCIe result is a real trained link state, not just a spoofed capability value.

## How it works

### Compute unlock

The CMP 40HX has an SM performance restriction enforced during GSP/SEC2 initialization. The patch injects a payload into the standard SEC2 Booter flow and, during its privileged execution phase, restores:

- `SS0 = 0x88888888`
- `SS1 = 0x00000008`
- `FECS_PLM = 0xFFFFFF8F`
- required `WPR2` / `SEC2 RESET_PLM` state

Expected driver log:

`CMP40_COMPUTE_UNLOCK_V525: ... SS0=0x88888888 SS1=0x00000008 FECS_PLM=0xffffff8f`

### PCIe Gen2 unlock

The CMP 40HX is restricted to PCIe Gen1 x16 (2.5 GT/s) by default.

The PCIe patch uses a protected GSP/RM policy path to enable the higher PCIe link rate, then performs an actual PCIe link retrain. The resulting hardware state is:

```text
LnkSta: Speed 5GT/s, Width x16
LnkCap2: Supported Link Speeds: 2.5-5GT/s
LnkCtl2: Target Link Speed: 5GT/s
```

This is a genuine PCIe Gen2 x16 link.

> PCIe Gen3 is **not** currently implemented by this project. Work on higher CMP models may provide a future reference for a Gen3 port.

### Resizable BAR unlock

The CMP 40HX exposes a restricted Resizable BAR configuration. The patch unlocks the required XVE registers, configures the BAR1 size selector, and enables the ReBAR state before the NVIDIA driver performs its normal PCI BAR resizing.

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

```bash
# Arch Linux / CachyOS
sudo pacman -S --needed base-devel pciutils

# Install headers for your kernel:

# Standard Arch kernel:
sudo pacman -S linux-headers

# CachyOS (choose the matching variant):
sudo pacman -S linux-cachyos-headers
# sudo pacman -S linux-cachyos-bore-headers
# sudo pacman -S linux-cachyos-lto-headers

# Verify that the 40HX is detected
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

SHA256 of the official source archive:

`619d7b5ce1f79c3211afdbf87d02b2174d268b10d005c5b8f994be22299be681`

### 3. Install

```bash
chmod +x install.sh
sudo ./install.sh
```

The installer performs:

`download → SHA256 verification → patch application → kernel module build → installation`

The patched modules are installed under:

`/lib/modules/$(uname -r)/updates/cmpunlocker/`

The installer applies:

- `0001-cmp40hx-unlock.patch`
- `0002-cmp40hx-pcie2-unlock.patch`
- `0003-cmp40hx-rebar-unlock.patch`

### 4. Cold reboot (required)

```bash
sudo shutdown -h now
```

A full power-off is recommended. Do not rely on a simple warm reboot when validating the unlock.

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
- CMP 40HX has no normal display outputs; this project is intended for compute use.
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
| `0003-cmp40hx-rebar-unlock.patch` | 8 GiB Resizable BAR unlock |
| `cmp_glcore_patch/` | Userspace Vulkan pipeline/MME throttle unlock for `libnvidia-glcore.so.610.57.04` |
| `install.sh` | Build and installation script |
| `README.md` | This document |

The compute unlock mainly modifies the GSP/SEC2 initialization path.

The PCIe unlock extends the GSP/RM PCIe policy and adds a host-side retrain path to bring the link up at 5 GT/s x16.

## Technical summary

### Compute unlock

The stock CMP 40HX exposes a restricted SM issue-rate configuration. The patch injects a custom payload into the SEC2 Booter flow and uses privileged HS execution to restore the required FECS/SM state.

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

The observed FurMark result improved from roughly 110 FPS to roughly 120 FPS on the same test setup.

### Resizable BAR unlock

Verified on real hardware:

| Metric | Stock | After unlock |
|---|---|---|
| BAR1 size | 64 MiB | **8 GiB** |
| `lspci` BAR 1 | 64 MB | **8 GB** |
| NVIDIA `BAR1 Total` | 64 MiB | **8192 MiB** |

The 8 GiB BAR1 aperture is exposed to the NVIDIA driver and is actively usable by applications. For example, War Thunder was observed using approximately 80 MiB of BAR1 memory in the main menu.

A FurMark test showed a small performance improvement from approximately 120 FPS to **~122 FPS** with ReBAR enabled.

### Pipeline bind / MME throttle unlock

The CMP 40HX also has a separate userspace performance restriction affecting classic Vulkan pipeline binding.

The throttle was identified experimentally in the NVIDIA userspace driver. Each classic `vkCmdBindPipeline` path invokes:

```text
NVC597_CALL_MME_MACRO(52), argument 0xf0
```

The selected MME macro executes a repeated sequence of:

```text
NVC597_PIPE_NOP
NVC597_WAIT_FOR_IDLE
```

for approximately 240 iterations. The important point is that the slowdown is caused by the **combination** of the two operations in the MME macro, not by either operation in isolation.

The relevant emitters were located in:

```text
/usr/lib/libnvidia-glcore.so.610.57.04
```

at file offsets:

```text
0xb20c2d
0xdcbf60
```

The unlock patches those userspace emitters so the expensive throttle sequence is no longer used.

Verified results:

| Test | Stock | After pipeline unlock |
|---|---:|---:|
| 4 binds | ~0.739 ms | **~0.0024 ms** |
| 1000 binds | ~183.3 ms | **~0.77 ms** |
| 1000 binds speed-up | 1× | **~238×** |

The unlock was additionally validated in real applications:

- FurMark improved from approximately **131 FPS average / 134 FPS max** to **134 FPS average / 137 FPS max** in the tested configuration.
- War Thunder reached approximately **90 FPS at Ultra with DLSS 4 Native** after the throttle was removed.
- Cyberpunk 2077 reached approximately **60.2 FPS average at High settings with DLSS Transformer Quality** in the tested configuration.

These application results are workload- and configuration-dependent and should not be treated as universal performance guarantees.

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

Important:

- This unlock is currently specific to **`libnvidia-glcore.so.610.57.04`**.
- Other NVIDIA driver versions require new emitter signatures and revalidation.
- NVIDIA 32-bit userspace components require separate signatures / patching.
- The system `/usr/lib/libnvidia-glcore.so.*` should be backed up before replacing or otherwise modifying it.
- The kernel-module unlocks (`0001`–`0003`) are independent of this userspace patch and are not modified by it.

## Technical summary: pipeline throttle unlock

The classic Vulkan pipeline bind path in the tested NVIDIA userspace driver emits:

```text
NVC597_CALL_MME_MACRO(52), argument 0xf0
```

The corresponding MME code performs a long sequence of `PIPE_NOP` + `WAIT_FOR_IDLE` pairs. Microbenchmarks demonstrated that replacing this throttled emitter path with the non-throttled argument/path removes the dominant bind overhead while leaving the actual pipeline bind functionality intact.

The patch is applied to the two identified emitter locations in `libnvidia-glcore.so.610.57.04`:

```text
0xb20c2d
0xdcbf60
```

This unlock is therefore a **userspace Vulkan command-generation patch**, not a GSP/SEC2 hardware-security unlock.

## Disclaimer
- Original compute unlock by @sbccc1888 (https://github.com/sbccc1888/cmpunlocker).
- The PCIe Gen2 and Resizable BAR unlocks were ported to the CMP 40HX from the CMP 50HX unlock work by @xrip (https://github.com/xrip/cmp50hx-unlock).
- This project is intended for hardware research and experimentation.
- Use it at your own risk.
- Modified kernel modules or NVIDIA userspace libraries may cause driver initialization failures, application crashes, or system instability.
- The pipeline throttle unlock modifies `libnvidia-glcore.so.610.57.04`; keep an untouched copy of the original library for rollback.
- NVIDIA licensing, warranty, and support terms may be affected.
- Keep a way to boot without the patched modules so the official driver can be restored.

## License

MIT

NVIDIA open-gpu-kernel-modules remains subject to NVIDIA's applicable open-source license terms.
