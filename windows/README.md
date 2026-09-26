# CMP 40HX Windows / UEFI Unlock

UEFI and Windows tools for NVIDIA CMP 40HX (TU106, PCI ID `10de:1f0b`). This is
the Windows half of [`cmpunlocker`](../README.md); the Linux kernel-module
patches live at the repository root.

The project restores the compute/Tensor path and brings the endpoint to PCIe
Gen2 when the platform and firmware allow it.

This is a volatile boot-time unlock. It does not flash the VBIOS and must be
applied again after a full GPU reset or power cycle.

## Results

| Area | Observed result |
| --- | --- |
| Compute | `SS0=0x88888888`, `SS1=0x00000008`; about 50 TFLOPS FP16 on the reference card |
| PCIe | Gen2 x16 when the platform accepts the retrain |
| NVIDIA driver | GSP remains enabled; no Code 43 on the tested systems |

PCIe Gen3 and RT-core unlocks are research topics and are not advertised as
working features. This tree targets CMP 40HX only.

## ReBAR / 8 GiB BAR1

The EFI can optionally activate the CMP 40HX's standard PCIe Resizable BAR
control for the full 8 GiB framebuffer. It opens the TU106 XVE gate, selects
ReBAR selector `7`, sizes BAR1, preserves the existing BAR3 window, and
programs the upstream bridge's 64-bit prefetchable window before reenabling
decode. A successful run is reported by `40HXCheck.exe` and by GPU-Z as an
8192 MiB Resizable BAR.

The EFI does not assume a fixed address such as `0x800000000`. It first parses
the upstream root bridge's ACPI resource descriptors, prefers an 8-GiB-aligned
span immediately after the current bridge window, and otherwise uses the top of
the reported 64-bit MMIO aperture. Some desktop firmware does not implement a
useful `RootBridgeIo->Configuration()` resource list; on those systems it uses
a bridge-relative fallback and still requires BAR readback plus VRAM aperture
verification. If any check fails, all GPU and bridge registers are restored and
the machine continues with stock BAR size.

This is deliberately best-effort rather than a promise of universal PCI
resource allocation. Enable **Above 4G Decoding**, keep the motherboard's
Resizable BAR support enabled, and test the actual topology. An 8 GiB BAR may
be rejected when the root complex has no sufficiently large 64-bit MMIO
aperture or when another bridge already occupies the candidate range.

## Package Contents

This repository ships the source tree and the prebuilt tools, not a zipped
release. `tools/build_release.bat` assembles a package into `release-fork/`
from the following inputs:

- `tools/inst40hx/40HXInstaller.exe` - GUI and command-line installer.
- `tools/uninstall40x/40HXUninstaller.exe` - component-level cleanup and restore.
- `tools/check40x/40HXCheck.exe` - read-mostly status and diagnostic tool.
- `tools/inst40hx/embed/40HXUNLK.EFI` - the UEFI unlock application.
- [`release-files/`](release-files/) - documents and helpers that ship alongside
  the binaries: `make_usb_efi.bat` (FAT32 recovery USB),
  `EFI应急修复指南.md` (recovery guide), `README.md` (release readme),
  `AI辅助安装提示词.txt`, and the optional `OpenCL.exe` compute smoke test.

The source EFI application is [`tools/unlock40x`](tools/unlock40x).
Fork-specific changes and research boundaries are documented in
[`FORK_CHANGES.md`](FORK_CHANGES.md).

## Before Installing

Use a UEFI/GPT installation and keep a Windows installer USB or another boot
entry available for recovery. In firmware setup:

1. Enable **Above 4G Decoding**.
2. Disable **Secure Boot** and **CSM**. Secure Boot can stay on if you sign the
   EFI application with your own key — see [Secure Boot](#secure-boot).
3. Disable **Fast Boot** while testing.
4. Prefer the CPU-connected PCIe x16 slot for the CMP 40HX.
5. Leave Resizable BAR on Auto or Enabled when available.

The installer checks the boot mode and reports when an MBR/Legacy conversion
is required. Do not flash the VBIOS as part of this project.

## Secure Boot

`40HXUNLK.EFI` is registered as a **firmware boot entry**, so the firmware
itself validates it against the platform's signature database (`db`) before
executing it. No shim is involved, which means **MOK enrollment does not apply
here** — `mokutil`/`MokManager` only matter when the firmware loads shim and
shim loads the next stage. The key has to be in `db`.

The shipped `40HXUNLK.EFI` is deliberately **unsigned**. A signature is bound to
one person's private key, so signing it upstream would break the reproducible
build (the SHA-256 in `SHA256SUMS.txt` could no longer be reproduced from
source) and would be unverifiable for everyone else anyway. So there are two
supported configurations:

| | Secure Boot | EFI |
| --- | --- | --- |
| Default | off | shipped, unsigned |
| Coexistence | on | signed by you, your key enrolled in `db` |

With Secure Boot on and an unsigned EFI, most firmware silently skips the entry:
Windows boots normally, the unlock never runs, and nothing reports an error.
`40HXCheck.exe` names this case explicitly instead of reporting a plain
"not unlocked" — see [Verify the Unlock](#verify-the-unlock).

### Signing the EFI with your own key

On Linux, with [`sbctl`](https://github.com/Foxboron/sbctl):

```bash
sudo sbctl create-keys                       # once, if you have no keys yet
sudo sbctl enroll-keys --microsoft           # once — see the warning below
cd windows/tools/unlock40x
./build_v70.sh                               # reproducible, unsigned
sudo ./sign_efi.sh                           # -> 40HXUNLK.signed.efi
```

`sign_efi.sh` never overwrites the unsigned build, refuses an already-signed
input, verifies that the output really carries a certificate table, and prints
the signer's subject so you can confirm it is your key. It works with `sbsign`
too if `sbctl` is not installed (`SBSIGN=`, `KEY=`, `CERT=`).

> **`--microsoft` is not optional in practice.** Enrolling your keys without
> the Microsoft certificates removes them from `db`, which stops Windows' own
> boot manager — and on many boards the discrete GPU's option ROM — from
> validating. Keep them unless you know exactly why you don't want them.

On Windows you can sign with the SDK's `signtool` instead, using the same key
pair exported as a PFX:

```bat
signtool sign /f db.pfx /fd sha256 /p <password> 40HXUNLK.signed.efi
```

Then install with the signed image:

```text
40HXInstaller.exe -efi C:\path\to\40HXUNLK.signed.efi
```

A file named `40HXUNLK.signed.efi` placed next to `40HXInstaller.exe` is picked
up automatically, including when the GUI is used. The installer prints which
image it deployed, its SHA-256, and its signature state; a signed image's hash
intentionally differs from the one in `SHA256SUMS.txt`, because the signature is
part of the file. Keep signed copies local — `.gitignore` excludes
`*.signed.efi` for that reason.

Note that the installer also writes the same image to the standard fallback path
`\EFI\Boot\bootx64.efi` (backing up the original as `bootx64.efi.40hx.bak`), so
an unsigned image breaks that path under Secure Boot as well. The uninstaller
restores the backup.

### Firmware quirks worth knowing

- `sbctl status` reports known firmware quirks. **FQ0001 — "Defaults to
  executing on Secure Boot policy violation"** means the board runs a binary
  that fails validation instead of refusing it. If your firmware has this quirk,
  an unsigned EFI may appear to work with Secure Boot on. Do not rely on it:
  it is a firmware bug, it can be fixed by a BIOS update, and it also means
  Secure Boot is not protecting you.
- Setup Mode must be enabled in firmware setup before `sbctl enroll-keys` can
  write `PK`/`KEK`/`db`.
- Some boards clear custom keys on a CMOS reset. After one, re-enroll before
  expecting the signed entry to boot.

### Confirming it works

Reboot with Secure Boot enabled, then from Windows:

```text
40HXCheck.exe -json
```

`firmware.unlock_efi_state` must read `"signed"`, and `verdict.status` must not
be `"secure_boot_blocks_unsigned_efi"`. `firmware.unlock_efi_signer` shows the
certificate's Common Name, which should be the key you enrolled.

## Installation

Run `40HXInstaller.exe` as Administrator. The GUI scans the current state and
preselects only missing components. A complete installation normally enables
GSP, installs the EFI entry, deploys the Gen2 helper, registers the login
task, and disables Windows Fast Startup and PCIe ASPM.

After installation, reboot. The EFI screen should appear briefly before
Windows starts. After login, the Gen2 task performs the link operation and
normally removes its helper driver when finished.

The installer UI and log support three languages:

```text
40HXInstaller.exe -lang en   # English (default)
40HXInstaller.exe -lang ru   # Russian
40HXInstaller.exe -lang zh   # Chinese
```

`CMP40HX_LANG=en|ru|zh` can be used instead of the command-line option. The
selection applies to the GUI and translated legacy log messages.

## Command Line

```text
40HXInstaller.exe              # open the management GUI
40HXInstaller.exe -task        # register the Gen2 login task
40HXInstaller.exe -gen2        # run Gen2 once now
40HXInstaller.exe -gen2 -hard  # allow the Link Disable fallback
40HXInstaller.exe -status      # print the current status
40HXInstaller.exe -uninstall   # uninstall the components
40HXInstaller.exe -efi <path>  # deploy your own signed EFI (see Secure Boot)
```

The default Gen2 strategy is use-and-remove. The optional retry and resident
strategies are configurable in the GUI or through `HKLM\SOFTWARE\40HXUnlock`:

| Value | Meaning |
| --- | --- |
| `DriverStrategy=0` | Load the helper only for the operation, then remove it. |
| `DriverStrategy=1` | Retry failed Gen2 bring-up according to the retry settings. |
| `DriverStrategy=2` | Keep the helper and let the login task monitor the link. |
| `Gen2AutoHard=0` | Disable the Link Disable fallback. |
| `Gen2RetryCount` | Number of automatic retries. |
| `Gen2RetryIntervalMin` | Delay between retries in minutes. |

## Verify the Unlock

After Windows login, run `40HXCheck.exe` as Administrator. A healthy result
shows:

- compute selectors `SS0=0x88888888` and `SS1=0x00000008`;
- a Gen2 target/link result when the platform completed retraining;
- GSP and ReBAR status without Code 43;
- an `Unlock EFI:` line reporting whether the image deployed on the ESP is
  signed, and by which certificate. With Secure Boot on and an unsigned image,
  the verdict says so directly rather than reporting a generic "not unlocked".

The diagnostic bundle is collected under
`%LOCALAPPDATA%\40HXUnlock\logs\`. The EFI application writes
`40hx_log.txt` to the ESP. Send those files when reporting a hardware-specific
failure.

An idle GPU can temporarily report Gen1 because of link power management. The
target link speed and a sustained-load measurement are more useful than one
idle snapshot.

## Recovery

If the firmware entry hangs, boot a Windows installer or recovery USB, mount
the EFI System Partition, and remove `EFI\40HX`. Rebuild the Windows boot
entry with the normal Microsoft recovery tools.
[`release-files/EFI应急修复指南.md`](release-files/) contains a step-by-step
recovery procedure, and `release-files/make_usb_efi.bat` prepares a FAT32 USB
for a one-time EFI launch.

## Returning to a boot manager (grub / Limine / rEFInd)

The normal EFI build chainloads Windows directly so firmware does not perform a
second POST and clear the volatile unlock. If you boot through your own EFI boot
manager instead (grub, Limine, rEFInd, …), add the `--return-to-bootloader` load
option to the entry that launches `40HXUNLK.EFI`. The application then applies
the unlock (and the ReBAR resize) and returns `EFI_SUCCESS` to the parent boot
manager instead of chainloading Windows itself, so your menu resumes normally.

```
# example Limine entry (limine.conf)
/40HX Unlock
    protocol: efi
    path: boot():/EFI/40HX/40HXUNLK.EFI
    cmdline: --return-to-bootloader
```

`--return-to-grub` and `--return-to-limine` are accepted as aliases. This
replaces the old experimental `NO_AUTO_CHAINLOAD` build — no separate EFI binary
is needed; the single shipped `40HXUNLK.EFI` honours the load option at runtime.
Note the unlock is still volatile: if the boot manager triggers a warm reset
after the application returns, the unlock state is lost.

## Source Layout

| Path | Purpose |
| --- | --- |
| `tools/inst40hx` | Installer GUI and command-line entry point |
| `tools/check40x` | Status and diagnostic utility |
| `tools/uninstall40x` | Component-level uninstaller |
| `tools/40hxcore` | Shared Windows operations and Gen2 logic |
| `tools/unlock40x` | EFI source, embedded firmware blobs, and build script |
| `tools/unlock40x/sign_efi.sh` | Signs the built EFI with your own Secure Boot key |
| `tools/winres_gen` | Generator for the installer's Windows resource object |
| `tools/build_release.bat` | Packages `release-fork/` with a SHA-256 manifest |
| `tools/verify_source.ps1` | Checks that expected source inputs are present |
| `release-files` | Documents and helpers packaged with the binaries |
| `40HX_Gen2_Windows` | Legacy standalone Gen2 scripts, helper drivers, and WinRing0 source |

## Build the Go Tools

Use Go 1.26 or newer. On Windows:

```bat
cd tools\inst40hx
go build -trimpath -ldflags="-H=windowsgui -s -w" -o 40HXInstaller.exe .
cd ..\uninstall40x
go build -trimpath -ldflags="-H=windowsgui -s -w" -o 40HXUninstaller.exe .
cd ..\check40x
go build -trimpath -ldflags="-H=windowsgui -s -w" -o 40HXCheck.exe .
```

The same tools cross-compile from Linux, which is useful for a quick check that
the tree is complete:

```bash
cd tools/inst40hx
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-H=windowsgui -s -w" -o 40HXInstaller.exe .
cd ../uninstall40x
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-H=windowsgui -s -w" -o 40HXUninstaller.exe .
cd ../check40x
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-H=windowsgui -s -w" -o 40HXCheck.exe .
```

`tools/inst40hx` embeds `embed/40HXUNLK.EFI`, `embed/ThrottleStop.sys`, and
`embed/WinRing0x64.sys` with `//go:embed`, so those files must be present for
the installer to build.

Run `tools/build_release.bat` on Windows to create a package with a SHA-256
manifest in `release-fork/`. The source verification helper is
`tools/verify_source.ps1`.

## EFI Build

`tools/unlock40x/build_v70.sh` requires gcc, binutils, gnu-efi headers and
libraries, and the `.bin` blobs in the same directory. Intermediate `.o`/`.so`
files and the resulting `unlock40x_v70.efi` are build outputs and are not
tracked; the authoritative copy of the built EFI is
`tools/inst40hx/embed/40HXUNLK.EFI`.

The normal build chainloads Windows after the unlock; add the
`--return-to-bootloader` load option instead to hand control back to a parent
EFI boot manager (see *Returning to a boot manager* above) — no separate build
is required. The optional VBIOS capture is disabled in the normal build; enable
it with `VBIOS_DUMP=1`. The host-side `FEAT_OVR` write probe is diagnostic only
and is disabled by default; enable it explicitly with `DIRECT_WRITE_PROBE=1`
when testing a board that is known to tolerate those MMIO transactions.
The Falcon preload probe is also disabled by default; use `PRELOAD_PROBE=1`
only for a dedicated diagnostic run.
Linux/gnu-efi builds use a SysV-ABI internal logger; EFI service callbacks
remain Microsoft-ABI. This avoids corrupted `%s` diagnostics in the EFI log.

The production path is CMP 40HX/TU106. Legacy GA102 helpers remain in the
source only as historical research code and are not a claim of support for
another CMP model. PCIe Gen3 and RT-core unlocks remain unverified research.

The EFI release also contains a best-effort CMP 40HX ReBAR path. It selects
standard ReBAR selector `7` (8 GiB / 8192 MiB BAR1), relocates BAR1 and BAR3
inside the upstream bridge's 64-bit prefetch window, and verifies the VRAM
aperture before accepting the change. The window is chosen from the root
bridge resource descriptors when firmware exposes them; otherwise a
topology-relative fallback is tried. Any failed readback or aperture check
rolls the PCI configuration and XVE state back to the original values.
The ReBAR path is validated on the author's CMP 40HX system, but remains
platform-dependent: it needs Above 4G Decoding and a free 8 GiB-aligned
64-bit MMIO span in the root-complex topology.

## Scope and Safety

- This tree is for CMP 40HX hardware and the tested TU106 firmware path.
- The unlock is not permanent and can be cleared by POST, FLR, driver reset,
  or a power cycle.
- The Gen2 helper uses signed low-level drivers with known vulnerabilities.
  They are loaded on demand by default; keep Secure Boot/HVCI and the
  vulnerable-driver blocklist policy in mind for your system. This project does
  not disable Secure Boot, HVCI, Defender, anti-cheat, or the blocklist
  automatically.
- Do not use the installer on another GPU family without a separate port and
  hardware validation.

Use the code for hardware research and comply with local law, vendor terms,
and the licensing terms described in the
[root README](../README.md#licensing).
