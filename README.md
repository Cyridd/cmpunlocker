# CMP 40HX - Compute, PCIe, ReBAR & Vulkan Pipeline Unlocks (Linux + Windows)

Hardware-research project for the NVIDIA CMP 40HX (TU106, PCI device ID `10de:1f0b`), covering both Linux and Windows.

- **Linux** (repository root) — open-kernel-module patches for NVIDIA `580.173.02`, `610.57.04` and `615.78.08` (default), plus one optional proprietary-userspace patch, installed by `install.sh`.
- **Windows / UEFI** ([`windows/`](windows/)) — a UEFI unlock application and Go installer/checker/uninstaller tools. See [`windows/README.md`](windows/README.md).

The two halves are independent: they unlock the same hardware by different means and share no code. Pick the one matching your OS.

## What it unlocks

- **Compute / SM configuration unlock** — changes the protected GSP/SEC2 initialization state used by the tested compute paths.
- **PCIe Gen2 x16 unlock** — raises the link from the stock PCIe Gen1 x16 (2.5 GT/s) to Gen2 x16 (5 GT/s) using the GSP/RM policy path plus a real link retrain.
- **Resizable BAR unlock** — enables an 8 GiB BAR1 aperture.
- **Pipeline bind / MME throttle unlock** (Linux, optional) — removes the artificial delay executed on classic `vkCmdBindPipeline` paths by patching the NVIDIA userspace `libnvidia-glcore.so` emitter.

None of these modifications changes the VBIOS or video memory. The compute, PCIe and ReBAR unlocks patch the NVIDIA open kernel module (Linux) or run from a UEFI application (Windows). The pipeline unlock is separate and patches a local copy of a proprietary userspace library. The unlocks are volatile and must be reapplied after a full GPU reset or power cycle.

## Results

Measured with the independent [clpeak](https://github.com/krrishnarraj/clpeak) (CUDA backend) on one Windows system, NVIDIA driver `617.14`:

<table>
  <tr>
    <td><b>Before unlock</b></td>
  </tr>
  <tr>
    <td><a href="docs/img/clpeak-cuda-before.png"><img alt="clpeak CUDA backend, stock CMP 40HX" src="docs/img/clpeak-cuda-before.png" width="100%" style="max-width: 900px;" /></a></td>
  </tr>
</table>

<table>
  <tr>
    <td><b>After unlock</b></td>
  </tr>
  <tr>
    <td><a href="docs/img/clpeak-cuda-after.png"><img alt="clpeak CUDA backend, unlocked CMP 40HX" src="docs/img/clpeak-cuda-after.png" width="100%" style="max-width: 900px;" /></a></td>
  </tr>
</table>

> The unlock restores Tensor Core throughput (about 63x), INT8 `dp4a` (about 33x) and PCIe Gen2 (2x), plus clpeak's plain CUDA FP32/FP16 loops (about 3x). It is **not** a global FP32 lock removal: FP32 through cuBLASLt, OpenCL and Vulkan runs at about 8.3 to 9 TFLOPS before and after. Earlier versions of this README claimed a `~0.39 TFLOPS` FP32 figure; that was wrong. Details in [`docs/how-it-works.md`](docs/how-it-works.md).

The full tables (including OpenCL and Vulkan), PCIe Gen2, ReBAR and pipeline measurements, and how to reproduce the screenshots are in [`docs/results.md`](docs/results.md).

## Quick start

### Linux

Requires the NVIDIA open kernel module stack and matching userspace/firmware for one of the supported versions (`580.173.02`, `610.57.04`, `615.78.08` default), a matching kernel headers package, and a C toolchain. Select the version with `--driver-version=VER`; per-distribution package lists and all installer options are in the [Linux installation guide](docs/linux-install.md).

```bash
lspci -nn | grep -i nvidia   # expected: 10de:1f0b
sudo ./install.sh            # download -> SHA256 check -> patch -> build -> install
sudo shutdown -h now         # full cold power-off, not a warm reboot
sudo dmesg | grep CMP40      # expect SS0=0x88888888 SS1=0x00000008 FECS_PLM=0xffffff8f
```

The optional pipeline unlock is separate, selects the library version automatically,
and has runtime confirmation on 610.57.04 and 615.78.08. See
[`cmp_glcore_patch/README.md`](cmp_glcore_patch/README.md).

An optional profiling unlock for 615.78.08 is available with
`sudo ./install.sh --profiling`. It ports CMP170HX's CMP SKU query mask;
the CUPTI support gate is hardware-confirmed on 40HX; counter collection
still needs a separate test. See
[`CMP40_PROFILING.md`](docs/research/CMP40_PROFILING.md).

### Windows

Download the latest build from the [Releases page](https://github.com/Cyridd/cmpunlocker/releases) (each release includes `SHA256SUMS.txt`; prebuilt executables are no longer committed to the repository), then follow [`windows/README.md`](windows/README.md) for firmware prerequisites, installation, verification and recovery. The EFI application is unsigned, so Secure Boot must be off or you must sign it yourself.

## Compatibility at a glance

- **Linux:** open-gpu-kernel-modules `580.173.02`, `610.57.04` and `615.78.08` (default); other versions need porting and revalidation. Secure Boot must be disabled or the modules signed. Rebuild after a kernel update.
- **Windows:** UEFI/GPT, Above 4G Decoding on, Secure Boot and CSM off.
- **PCIe Gen2 x16 is verified; Gen3 and RT-core unlocks are not implemented.**
- The CMP 40HX has no display outputs; graphics use needs a headless, secondary-GPU or remote-display setup.

Full list: [`docs/compatibility.md`](docs/compatibility.md).

## Documentation

| Document | Contents |
|---|---|
| [`docs/linux-install.md`](docs/linux-install.md) | Linux prerequisites, installer options, verification, removal |
| [`windows/README.md`](windows/README.md) | Windows/UEFI guide: prerequisites, installation, Secure Boot, recovery |
| [`docs/how-it-works.md`](docs/how-it-works.md) | Technical description of the four unlocks |
| [`docs/results.md`](docs/results.md) | Benchmark screenshots and measured results |
| [`docs/compatibility.md`](docs/compatibility.md) | Supported versions, platform notes, known issues |
| [`docs/file-structure.md`](docs/file-structure.md) | What each file and directory is |
| [`docs/licensing.md`](docs/licensing.md) | Per-component licenses and attribution |
| [`cmp_glcore_patch/README.md`](cmp_glcore_patch/README.md) | Userspace pipeline-throttle patch usage and rollback |
| [`docs/research/`](docs/research/README.md) | Research notes and evidence: PCIe Gen2 diagnostics, Gen3 investigation, Link Disable audit, pipeline throttle findings |
| [`ROADMAP.md`](ROADMAP.md) | Research directions and plans |

**Origin and attribution.** This tree is a fork of
[`PZH1gdmu/CMP40HX-Unlock`](https://github.com/PZH1gdmu/CMP40HX-Unlock), whose
UEFI unlock is in turn built on the Windows CMP90HX codebase from
[`WildFlash1st/cmp90hx-unlock-for-windows`](https://github.com/WildFlash1st/cmp90hx-unlock-for-windows),
while its CMP40HX-specific boot flow, register state, and cleanup logic
substantially build on the CMP40HX research and implementation published in
this repository. Changes made in this fork — hardware-bound GSP matching,
correct Windows command-line quoting under UAC elevation, the runtime
`--return-to-bootloader` EFI handoff, the 8 GiB ReBAR path with a GPU-Z-style
ReBAR readout in the checker, and a reproducible packaging/verification flow —
are documented in
[`windows/FORK_CHANGES.md`](windows/FORK_CHANGES.md). See
[Licensing](#licensing) and [`docs/licensing.md`](docs/licensing.md) for the third-party components it redistributes.

## Disclaimer

- This project is intended for hardware research and experimentation.
- Use it at your own risk.
- Modified kernel modules or NVIDIA userspace libraries may cause driver initialization failures, application crashes, or system instability.
- The pipeline throttle unlock creates a modified local copy of the matching `libnvidia-glcore.so.<version>`; never overwrite the original system library, so rollback stays trivial.
- NVIDIA licensing, warranty, and support terms may be affected.
- Keep a way to boot without the patched modules so the official driver can be restored.
- On Windows, the unlock runs from a UEFI application and the Gen2 helper loads
  signed low-level drivers with known vulnerabilities. Keep a Windows installer
  or recovery USB available before installing the EFI entry, and review the
  Secure Boot / HVCI / vulnerable-driver-blocklist notes in
  [`windows/README.md`](windows/README.md).

## Licensing

The repository's own license is the MIT License in [`LICENSE`](LICENSE). The repository also contains code derived from other authors and redistributes third-party components (a GPL-3.0 driver, a proprietary signed driver, NVIDIA firmware images) that keep their original terms, so the repository as a whole is not distributable under MIT alone. See [`docs/licensing.md`](docs/licensing.md) for the per-component list.
