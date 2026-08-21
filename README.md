# CMP 40HX — Compute & PCIe Unlock (NVIDIA Linux Driver 580.173.02)

Linux unlock project for the NVIDIA CMP 40HX (TU106, PCI device ID `10de:1f0b`).

The project currently provides two independent unlocks:

- **Compute / SM performance unlock** — restores full SM issue rate and compute performance.
- **PCIe Gen2 x16 unlock** — raises the link from the stock PCIe Gen1 x16 (2.5 GT/s) to PCIe Gen2 x16 (5 GT/s) using the GSP/RM policy path plus a real link retrain.

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

`install.sh` downloads `open-gpu-kernel-modules` 580.173.02 from GitHub and verifies its SHA256.

**Option B — place a local archive:**

If you do not want the script to download the source automatically, place one of the following archives next to `install.sh`:

- `open-gpu-kernel-modules-580.173.02.tar.gz`
- `NVIDIA-580.173.02.tar.xz`
- `NVIDIA-kernel-module-source-580.173.02.tar.xz`

SHA256 of the official source archive:

`a2cd41cf100a81d90de9d5ca192b828ed8a63408330acef7931df00487acd82f`

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

- **NVIDIA open-gpu-kernel-modules 580.173.02 only.** Other driver versions require porting and revalidation.
- The project has been tested on CachyOS system with 7.1.8-1-cachyos kernel.
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
| `0001-cmp40hx-unlock.patch` | Compute / SM unlock for NVIDIA 580.173.02 |
| `0002-cmp40hx-pcie2-unlock.patch` | PCIe Gen2 x16 unlock |
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

## Disclaimer
- Original compute unlock by @sbccc1888. I added the PCIe Gen2 x16 unlock.
- This project is intended for hardware research and experimentation.
- Use it at your own risk.
- Modified kernel modules may cause driver initialization failures or system instability.
- NVIDIA licensing, warranty, and support terms may be affected.
- Keep a way to boot without the patched modules so the official driver can be restored.

## License

MIT

NVIDIA open-gpu-kernel-modules remains subject to NVIDIA's applicable open-source license terms.
