# Fork notes

The Windows/UEFI tree in `windows/` originates as a fork of
[`PZH1gdmu/CMP40HX-Unlock`](https://github.com/PZH1gdmu/CMP40HX-Unlock). It
keeps the experimentally validated CMP 40HX EFI payload and adds small,
auditable changes around it. The `40HXUnlock_v3.2.0` package available
locally contains newer Windows Go binaries, but their matching source is not
published in the upstream repository. This tree therefore does not claim that
those binaries are reproducible from the v3.0 source.

## Changes in this fork

The current FULL EFI supports **GSP OFF / CPU-RM** using the tested 960 KiB
temporary Booter heap and complete native Booter handoff. It keeps return-to-bootloader options and signed-image deployment.
ReBAR resource relocation now requires an explicit experimental opt-in.
The installer now offers **Disable GSP (recommended)**, writes an explicit
`EnableGpuFirmware=0`, detects outdated ESP payloads, upgrades EFI before
switching GSP, and redirects existing Gen2 login entries to the current
installer. Gen2 no longer changes GSP; the obsolete GSP-ensure task is removed
on installation. See [the report](../docs/research/CPU_RM_SMALL_WPR.md).

I tested the integrated installer and its upgrade path: Disable GSP replaces
an existing `EnableGpuFirmware=1` with `0`, and the Gen2 task leaves it at `0`.
The notice asking for manual boot-order setup uses an information icon.

1. GSP registry fallback is hardware-bound. A display name such as `RTX 2060`
   or `RTX 2070` is no longer enough to select a class key; `MatchingDeviceId`
   must also contain `PCI\\VEN_10DE&DEV_1F0B`. This prevents a multi-GPU system
   from changing GSP settings for the wrong NVIDIA adapter.
2. UAC elevation preserves arguments correctly. Paths containing spaces,
   quotes, or trailing backslashes are encoded with the Windows command-line
   rules instead of being concatenated with `strings.Join`.
3. The shipped EFI honours a `--return-to-bootloader` load option at runtime.
   The normal path chainloads Windows after unlock; when the boot entry carries
   `--return-to-bootloader` (aliases `--return-to-grub` / `--return-to-limine`)
   the application applies the unlock and returns `EFI_SUCCESS` to the parent
   EFI boot manager (grub/Limine/rEFInd) instead. This is a single-binary
   runtime toggle — it replaces the earlier build-time `NO_AUTO_CHAINLOAD`
   experiment, which was removed because it required a separate EFI image and
   never worked reliably.
4. A source verification script and a reproducible Windows packaging script
   are included. The package manifest records hashes so a tester can distinguish
   the EFI and Go components actually used.
   Tagged releases contain a complete ZIP with the package directories and
   per-file checksums, plus a separate checksum for the archive.
5. The EFI source was cleaned up for the CMP 40HX release path: helper symbols,
   PCI discovery names, multi-card NVRAM keys, and user-visible messages now
   use `CMP40`/`CMP40HX` terminology. Historical GA102 blobs and their numeric
   geometry remain explicitly marked as legacy data rather than being relabeled
   as unverified TU106 constants.

6. `40HXCheck.exe` reports Resizable BAR (ReBAR) state directly. NVIDIA APP and
   the NVIDIA Control Panel never show ReBAR for this card, so the checker reads
   the XVE Resizable BAR control register (like GPU-Z) and prints whether it is
   enabled and the current BAR1 size (8 GB when the unlock resize is in effect,
   64 MB stock).
7. ReBAR resource relocation is experimental and disabled by default, including
   full installation and USB/fallback boot. The unchecked installer component
   or `-rebar` adds `--experimental-rebar` to the EFI entry; `-norebar` disables
   it. Compute unlock and Gen2 do not require relocation. See the
   [disk/PCI audit](../docs/research/WINDOWS_DISK_PCI_AUDIT.md).
8. Secure Boot can stay enabled. `tools/unlock40x/sign_efi.sh` signs the built
   EFI with the operator's own `db` key (`sbctl`, or `sbsign`), and the installer
   deploys a user-supplied image via `-efi <path>` or a `40HXUNLK.signed.efi`
   sitting next to the executable. The shipped image stays unsigned on purpose so
   that its hash keeps reproducing from source; signing is a local step whose
   output is gitignored. `40HXCheck.exe` reads the ESP image's Authenticode
   certificate table directly (`tools/40hxcore/pesign.go`) and reports
   "Secure Boot is on and the deployed EFI is unsigned" as its own verdict,
   because that combination otherwise looks identical to a silent failure to
   unlock. See the *Secure Boot* section of [`README.md`](README.md).

## Returning to a boot manager (grub / Limine / rEFInd)

Add the `--return-to-bootloader` load option to the EFI entry that launches
`40HXUNLK.EFI` (for example, a Limine `cmdline:` or a grub `chainloader`
argument). The application applies the unlock (ReBAR relocation requires its own opt-in), then returns
`EFI_SUCCESS` to the parent boot manager instead of chainloading Windows itself,
so your menu resumes normally. `--return-to-grub` and `--return-to-limine` are
accepted as aliases.

The standard `40HXUNLK.EFI` without the option remains the recommended path
because it chainloads Windows without returning through firmware and triggering
a new POST. Returning to a boot manager is still subject to the volatile-unlock
caveat: if the manager performs a reset after the EFI application returns, the
unlock state is lost.

## Research boundaries

- The PCIe Gen3 work remains research-only. The CMP 40HX endpoint still
  materializes Gen1/Gen2 capability shadows and rolls back a Gen3 request;
  this fork does not advertise Gen3 support.
- The Windows Gen2 path uses signed but vulnerable helper drivers. They are
  loaded on demand and normally removed after use. This fork does not disable
  Secure Boot, HVCI, Defender, anti-cheat, or the vulnerable-driver blocklist
  automatically.
- EFI, driver, and firmware changes are volatile hardware experiments. Keep a
  recovery path (Windows installer USB or a second boot entry) before testing.

## Build and verification

On Windows, run `windows/tools/build_release.bat`. It builds the three Go
utilities with the GUI subsystem, packages the embedded EFI and helper drivers,
and writes `SHA256SUMS.txt` to `windows/release-fork/` (a build output, not
tracked in git). `windows/tools/verify_source.ps1` checks that the expected
source inputs are present and prints EFI hashes.

## Gen2 PL0 parity with upstream v3.2.0 by PZH1gdmu (`tools/inst40hx/gen2_pl0_v2.go`)

- PL0 register table now matches 3.2.0: new `PL_LINK_RATE` (0x8C1C0) write and
  read-modify-write for `LINK_CONFIG_0` / `PL_LINK_RATE` (the fork previously wrote
  `LINK_CONFIG_0` as the constant `0x80085800`).
- BAR0 / BOOT_0 validation now runs on every PL0 write (including after Link Disable
  and PnP), not only once at start.
- `Gen2AutoHard` default changed from on to off (matches 3.2.0). Manual `-hard` is unchanged.
- New PnP fallback between Stage 1 and Stage 2 (`Gen2PnpFallback`, default on).
- New read-only `-gen2dry` mode and a 100 ms log fsync loop (`setupLog`).
