# Fork notes

The Windows/UEFI tree in `windows/` originates as a fork of
[`PZH1gdmu/CMP40HX-Unlock`](https://github.com/PZH1gdmu/CMP40HX-Unlock). It
keeps the experimentally validated CMP 40HX EFI payload and adds small,
auditable changes around it. The `40HXUnlock_v3.2.0` package available
locally contains newer Windows Go binaries, but their matching source is not
published in the upstream repository. This tree therefore does not claim that
those binaries are reproducible from the v3.0 source.

## Changes in this fork

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
5. The EFI source was cleaned up for the CMP 40HX release path: helper symbols,
   PCI discovery names, multi-card NVRAM keys, and user-visible messages now
   use `CMP40`/`CMP40HX` terminology. Historical GA102 blobs and their numeric
   geometry remain explicitly marked as legacy data rather than being relabeled
   as unverified TU106 constants.

6. `40HXCheck.exe` reports Resizable BAR (ReBAR) state directly. NVIDIA APP and
   the NVIDIA Control Panel never show ReBAR for this card, so the checker reads
   the XVE Resizable BAR control register (like GPU-Z) and prints whether it is
   enabled and the current BAR1 size (8 GB when the unlock resize is in effect,
   256 MB stock).
7. The installer exposes ReBAR as a component. The unlock EFI resizes BAR1 to
   8 GiB at boot by default; the installer's *ReBar Unlock* checkbox (and the
   `-rebar` command line) ensure the boot entry has no `norebar` token, while
   `-norebar` writes that token to opt out. Because the default (no token) is
   "on", firmware that ignores load options keeps the previous behaviour.
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
argument). The application applies the unlock and the ReBAR resize, then returns
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
