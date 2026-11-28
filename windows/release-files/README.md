# CMP 40HX Windows Unlock - Release Package

This package is for NVIDIA CMP 40HX (TU106, PCI ID `10de:1f0b`). It restores
the compute/Tensor path and can bring the PCIe endpoint to Gen2 after Windows
loads the NVIDIA driver.

## Quick Start

1. In BIOS, enable **Above 4G Decoding** and disable **Secure Boot**, **CSM**,
   and **Fast Boot** while testing. Use UEFI/GPT.
2. Run `40HXInstaller.exe` as Administrator.
   Select **Compute EFI** when upgrading. Select **Disable GSP** only if you
   want to switch the driver to GSP OFF.
3. Reboot and allow the `40HXUNLK.EFI` entry to run before Windows.
4. After login, let the Gen2 task finish, then run `40HXCheck.exe` as
   Administrator.

The installer GUI and logs support `-lang en`, `-lang ru`, and `-lang zh`.
English is the default. The same choice can be made with
`CMP40HX_LANG=en|ru|zh`.

This package uses the Rust FULL EFI with a 960 KiB Booter heap. Installation preserves
the CMP40HX registry setting by default and updates existing login
entries to use this installer. The Gen2 task no longer writes GSP settings.

## Files

- `40HXInstaller.exe` - install and configure the components.
- `40HXUninstaller.exe` - remove the components and restore the boot entry.
- `40HXCheck.exe` - verify compute selectors, GSP, ReBAR, and PCIe status.
- `files/40HXUNLK.EFI` - UEFI unlock application.
- `make_usb_efi.bat` and `copy_efi_verified.ps1` - prepare a FAT32 USB fallback.
- `WinRing0-LICENSE.txt` - the GPLv3 license for the bundled [WinRing0 driver](https://github.com/Cyridd/cmpunlocker/tree/main/windows/40HX_Gen2_Windows/source/WinRing0-master).

No benchmark is bundled. For an independent compute check, use a third-party
tool such as [clpeak](https://github.com/krrishnarraj/clpeak) (CUDA backend) or
[OpenCL-Benchmark](https://github.com/ProjectPhysX/OpenCL-Benchmark).

The unlock is volatile. POST, a driver reset, FLR, or a power cycle can clear
it. No VBIOS flash is required or recommended.

## ReBAR

ReBAR relocation is **experimental and disabled by default**. Compute unlock
and Gen2 keep working with the firmware-assigned BARs. Boots without an
explicit command-line or ESP-policy opt-in retain stock BARs.

Deploy the current Compute EFI and select **Experimental ReBAR relocation
(8 GB BAR1)** together. Later, `40HXInstaller.exe -rebar` can enable it as
Administrator. The installer verifies BCD loadoptions and writes
`\EFI\40HX\REBAR.CFG` on the ESP; the EFI reads and caches this policy before
GPU mutation. This also covers fallback launches on the same ESP, where BCD
loadoptions may not reach the image. Both deployed EFI images must match the
selected policy-aware build before enabling.

`40HXInstaller.exe -norebar` clears the file opt-in and saves the BCD opt-out.
Another boot manager can still pass `--experimental-rebar` explicitly;
literal `norebar` overrides both opt-in sources. Installer success confirms
saved configuration. Confirm activation through the EFI resize result and
actual Windows BAR1 size after a cold boot.

The experimental allocator's memory-map, bridge and VRAM checks do not prove
that every chosen address is routable or granted by ACPI `_CRS`. It remains
platform-dependent and requires separate hardware testing. Verify Windows'
actual BAR1 size using `40HXCheck.exe` or GPU-Z.

## Verification and Logs

Verify the ZIP against the SHA-256 published with the release. After extraction,
run `.\verify_source.ps1` in PowerShell to check the files listed in the
package's own `SHA256SUMS.txt`.

Successful compute unlock normally reports `SS0=0x88888888` and
`SS1=0x00000008`. Rust EFI writes numbered logs under `EFI/40HX/LOGS/` on
its boot volume. On the mounted ESP, the checker selects the newest matching
record, even if that run was interrupted. It looks for the old C EFI log
`40hx_log.txt` only when no matching Rust log exists. For a failure report,
include `diagnose.txt` and the EFI log it names from
`%LOCALAPPDATA%\40HXUnlock\logs\`.

If Gen2 is not reached, run `40HXInstaller.exe -gen2` or use the GUI's Gen2
controls. `-gen2 -hard` enables the optional Link Disable fallback.

For full BIOS, recovery, boot-manager handoff, build, and limitation
documentation, see the
[Windows guide](https://github.com/Cyridd/cmpunlocker/blob/main/windows/README.md).

PCIe Gen3 and RT-core unlocks are research-only and are not guaranteed by this
release. Do not use this package on another GPU family without independent
hardware validation.
