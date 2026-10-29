# Compatibility and known issues

## Linux

- **NVIDIA open-gpu-kernel-modules `580.173.02`, `610.57.04` and `615.78.08` (default).** Select one with `--driver-version=VER`; other driver versions require porting and revalidation. The userspace/firmware version must match the selected kernel-module version.

Hardware tests on 2026-10-18 confirmed successful GPU initialization, compute
unlock and PCIe Gen2 x16 with the corrected `615.78.08` patches. The compute
patch uses the signed `610.57.04` TU102 Booter for the unlock transaction
and the native `615.78.08` Booter for GSP
initialization. Its PCIe patches also restore the policy setup omitted during
the original port; the boot log records `RETRAIN_PASS` on the first attempt
and `LnkSta: Speed 5GT/s, Width x16`. See
[`615_BOOTER_COMPATIBILITY.md`](research/615_BOOTER_COMPATIBILITY.md) for the
failure analysis and validation procedure.

- The optional `--profiling` patch is available for `615.78.08` and masks the
  CMP SKU query for 40HX. CUPTI gate support is hardware-confirmed; counter
  collection awaits validation. See
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
- GSP must be enabled (`EnableGpuFirmware=1`); the unlock depends on it. With GSP on
  the driver reserves some video memory: a card that reports 8191 MB stock reports
  about 8024 MB (CUDA).
- The PCIe Gen2 bring-up depends on the platform accepting the retrain, and the
  8 GiB ReBAR path needs a free 8 GiB-aligned 64-bit MMIO span in the
  root-complex topology. Both fail closed and roll back.
- The Gen2 helper loads signed but known-vulnerable drivers on demand. HVCI and
  Microsoft's vulnerable-driver blocklist can block them.
- PCIe Gen3 and RT-core unlocks are not implemented here either.

## Both platforms

- CMP 40HX has no normal display outputs. Graphics use therefore requires a suitable headless, secondary-GPU, remote-display or similar setup.
