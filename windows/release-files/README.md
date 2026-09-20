# CMP 40HX Windows Unlock - Release Package

This package is for NVIDIA CMP 40HX (TU106, PCI ID `10de:1f0b`). It restores
the compute/Tensor path and can bring the PCIe endpoint to Gen2 after Windows
loads the NVIDIA driver.

## Quick Start

1. In BIOS, enable **Above 4G Decoding** and disable **Secure Boot**, **CSM**,
   and **Fast Boot** while testing. Use UEFI/GPT.
2. Run `40HXInstaller.exe` as Administrator.
3. Reboot and allow the `40HXUNLK.EFI` entry to run before Windows.
4. After login, let the Gen2 task finish, then run `40HXCheck.exe` as
   Administrator.

The installer GUI and logs support `-lang en`, `-lang ru`, and `-lang zh`.
English is the default. The same choice can be made with
`CMP40HX_LANG=en|ru|zh`.

## Files

- `40HXInstaller.exe` - install and configure the components.
- `40HXUninstaller.exe` - remove the components and restore the boot entry.
- `40HXCheck.exe` - verify compute selectors, GSP, ReBAR, and PCIe status.
- `OpenCL.exe` - optional compute smoke test.
- `files/40HXUNLK.EFI` - UEFI unlock application.
- `make_usb_efi.bat` - prepare a FAT32 USB fallback.

The unlock is volatile. POST, a driver reset, FLR, or a power cycle can clear
it. No VBIOS flash is required or recommended.

The packaged EFI uses the normal unlock path. Its VBIOS dump, Falcon preload
probe, and direct FEAT_OVR host-write probe are diagnostic build options and
are not enabled in this release image.

## ReBAR

The EFI attempts the CMP 40HX ReBAR unlock after a successful compute unlock.
It selects ReBAR selector `7` and a full 8 GiB (`8192 MiB`) BAR1, keeps BAR3
in the same bridge window, and verifies both PCI readback and actual VRAM
responses. The candidate MMIO64 window is selected from the upstream root
bridge resource descriptors when available, with a bridge-relative fallback
for firmware that returns no usable resource template. A failed safety check
restores the original BARs, bridge window, and XVE registers, so the normal
compute unlock can continue without ReBAR.

This is topology-dependent and is not a replacement for firmware resource
allocation. **Above 4G Decoding** and motherboard ReBAR support must be
enabled; another bridge may already consume the available 64-bit aperture.
Verify the result with `40HXCheck.exe` or GPU-Z rather than assuming that a
successful EFI log alone means that Windows retained the 8 GiB mapping.

## Verification and Logs

Successful compute unlock normally reports `SS0=0x88888888` and
`SS1=0x00000008`. The EFI log is `40hx_log.txt` on the ESP; Windows diagnostics
are collected under `%LOCALAPPDATA%\40HXUnlock\logs\`.

If Gen2 is not reached, run `40HXInstaller.exe -gen2` or use the GUI's Gen2
controls. `-gen2 -hard` enables the optional Link Disable fallback.

For full BIOS, recovery, Limine, build, and limitation documentation, see the
[repository README](../../README.md).

PCIe Gen3 and RT-core unlocks are research-only and are not guaranteed by this
release. Do not use this package on another GPU family without independent
hardware validation.
