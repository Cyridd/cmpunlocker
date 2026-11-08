# Compatibility and known issues

## Linux

- **Proprietary CPU-RM compute unlock:** native initialization and CUDA
  throughput validated on `610.43.03`, `615.78.08` and `620.70.06` with GSP OFF, on an
  8 GiB CMP40HX/TU106. Use [the separate guide](../linux/cpu-rm/README.md).
  For `620.70.06`, the module build/offline checks and manual native
  initialization, numerical WMMA and runtime Gen2 tests passed on
  `7.3.3-1-cachyos`. NVIDIA and persistenced had already run after power-on;
  the initramfs hook before the first NVIDIA initialization remains untested.
  Keep the pinned 610.43.03 pre-driver assets with this release.
  Other releases, GRID/vGPU, suspend/resume and other models have not been
  independently validated.
  This path does not apply the open-module PCIe/ReBAR/profiling patches.
  A [community GRID 19.6 / 580.178.05 report](research/GRID_CPU_RM_REPORT.md)
  documents compute unlock and healthy host initialization on one Proxmox
  system, Linux guest CUDA/Tensor Core throughput and Windows guest OpenCL
  throughput. The reporter also ran clpeak repeatedly for about half an hour
  without noticed instability or driver errors. These guest results have not
  been independently reproduced; the physical link remains Gen1 x8.

- **NVIDIA open-gpu-kernel-modules `580.173.02`, `610.57.04`, `615.78.08` and `620.70.06` (default).** Select one with `--driver-version=VER`; other driver versions require porting and revalidation. The userspace/firmware version must match the selected kernel-module version.

The `620.70.06` port uses the native `KernelVbios` FWSEC/FRTS commands and
common boot-argument cleanup. Its signed `610.57.04` unlock Booter matches the
official archive, and the native 620 Booter is unchanged. Patch application
passes with fuzz 0 in normal and diagnostic PCIe modes. Manual runtime tests
confirmed compute, Gen2 x16, 8 GiB BAR1 and CUPTI counter collection. See the 620 follow-up in
[the Booter report](research/615_BOOTER_COMPATIBILITY.md).

Both PCIe variants with optional profiling built all five open modules on
`7.3.3-1-cachyos` with Clang/LLD `23.1.4`. NVIDIA's compiler check printed a
version-mismatch warning despite matching Clang versions: its probe compares
`__GNUC__` and `__GNUC_MINOR__` with the kernel compiler version. The builds
completed with exit 0 and also emitted warnings in upstream code.

Hardware tests on 2026-10-18 confirmed successful GPU initialization, compute
unlock and PCIe Gen2 x16 with the corrected `615.78.08` patches. The compute
patch uses the signed `610.57.04` TU102 Booter for the unlock transaction
and the native `615.78.08` Booter for GSP
initialization. Its PCIe patches also restore the policy setup omitted during
the original port; the boot log records `RETRAIN_PASS` on the first attempt
and `LnkSta: Speed 5GT/s, Width x16`. See
[`615_BOOTER_COMPATIBILITY.md`](research/615_BOOTER_COMPATIBILITY.md) for the
failure analysis and validation procedure.

- The optional glcore pipeline patch has runtime confirmation on `610.57.04`,
  `615.78.08` and `620.70.06`, plus offline signature validation on `580.173.02`. The
  620 comparison used nvidia-open with GSP ON and original versus patched glcore.
  Its helper selects the matching version automatically; unknown binary layouts
  are rejected.
- The optional `--profiling` patch is available for `615.78.08` and `620.70.06` and masks the
  CMP SKU query for 40HX. CUPTI gate support is hardware-confirmed on both
  versions; a range-profiling hardware counter was verified on 620. Other
  metrics and Nsight Compute remain untested. See
  [`CMP40_PROFILING.md`](research/CMP40_PROFILING.md).
- The project has been tested on CachyOS system with 7.2.0-1-cachyos kernel.
- The compute unlock changes the GSP/SEC2 boot payload. The VBIOS and VRAM are not modified.
- The PCIe patch changes the GSP/RM PCIe policy and retrains the link. It does not modify the VBIOS.
- **PCIe Gen2 x16 is verified.**
- **PCIe Gen3 is not currently unlocked.**
- Secure Boot must be disabled or the custom kernel modules must be signed with a trusted key.
- After a kernel update, rebuild and reinstall the patched modules.

For example, from the repository root:

```bash
sudo ./install.sh --no-download
```

## Windows

- Requires a UEFI/GPT installation with **Above 4G Decoding** enabled and
  **Secure Boot** and **CSM** disabled — the EFI application is unsigned.
- The unlock is volatile and is reapplied at every boot by the EFI entry. POST,
  FLR, a driver reset, or a power cycle clears it.
- The current small-WPR EFI supports **GSP OFF / CPU-RM**. The installer
  explicitly writes `EnableGpuFirmware=0`, replaces old login-task targets,
  and recommends upgrading the EFI first. I tested ZERO/FULL initialization,
  FULL unlock, and the installer's GSP-OFF upgrade; results are recorded in
  [the report](research/CPU_RM_SMALL_WPR.md);
  the exact final Windows driver version was not recorded.
- Earlier GSP-ON measurements reported about 8024 MB CUDA VRAM instead of
  8191 MB stock. Those are historical measurements, not a GSP requirement.
- The PCIe Gen2 bring-up depends on the platform accepting the retrain, and the
  8 GiB ReBAR path needs a free 8 GiB-aligned 64-bit MMIO span in the
  root-complex topology. Both fail closed and roll back.
- The Gen2 helper loads signed but known-vulnerable drivers on demand. HVCI and
  Microsoft's vulnerable-driver blocklist can block them.
- PCIe Gen3 and RT-core unlocks are not implemented here either.

## Both platforms

- CMP 40HX has no normal display outputs. Graphics use therefore requires a suitable headless, secondary-GPU, remote-display or similar setup.
