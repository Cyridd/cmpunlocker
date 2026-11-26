# Rust ReBAR-r1 physical test

Current builds log `CMPUNLOCKER-REBAR:`. The examples from the earlier images use `CMP40-REBAR:`.

The package contains experimental FULL/ZERO compute unlock images with the C EFI's
8 GiB BAR1 relocation. Host comparisons cover PCI/MMIO decisions and rollback;
QEMU checks policy loading, memory-map access and boot refusal without a GPU.
ReBAR-r1 now has hardware acceptance: the supplied four logs confirm ZERO/FULL
completion and policy behavior, and my OS tests
confirmed 8192 MB BAR1 in both enabled controls with FULL compute retained.
This procedure remains available for changed variants and repetition.
The repository's `windows/efi-rust/tests/evidence/stage5/README.md` records
the acceptance and its scope.
Keep the accepted carrier-r1 pair and the working C EFI available for recovery.

## Package and setup

| File | Purpose |
| --- | --- |
| `EFI/BOOT/BOOTX64.EFI` | FULL image for a separate USB fallback boot |
| `EFI/CMP40/FULLR1.EFI` | Same FULL image under a distinct name |
| `EFI/CMP40/ZEROR1.EFI` | ZERO selectors with the same ReBAR path |
| `EFI/40HX/REBAR.CFG` | Explicit opt-in: `CMP40HX_REBAR_V1=1` followed by LF |
| `SHA256SUMS.txt` | Checksums of the images, policy and guide |

Verify the checksums before use. The package has no installer or QEMU fixture.
Extract it onto a separate writable FAT test USB, preserving any existing
fallback loader first. Secure Boot must accept these unsigned images, Above
4G Decoding must be enabled, and CSM must be disabled. Use the same firmware
and NVIDIA GSP-OFF settings as the accepted carrier tests.

When copying the images to another writable EFI volume, the policy must be at
`EFI/40HX/REBAR.CFG` on that same volume. A policy on the Windows disk or another
ESP does not enable this image. The policy enables ReBAR in both controls.
For Limine, add separately named EFI entries for `FULLR1.EFI` and `ZEROR1.EFI`
using the existing working path syntax. An empty command line uses direct
Windows handoff and the file policy.

Before testing relocation, cold-boot each new image with `norebar` to compare
its compute unlock and Windows behavior with the accepted pair. This token overrides
both the file policy and `--experimental-rebar`. For a USB fallback entry
without load options, set the file to `CMP40HX_REBAR_V1=0` followed by LF for
this baseline check, then restore the enabled file for the relocation tests.

## Cold ZERO, then cold FULL

Fully power off between attempts. Run ZERO with the enabled policy first and
collect the new `RZ*.LOG` from the root of the image volume. It should contain,
in order:

```text
CMP40-REBAR: POLICY requested=true
FWSEC_COMPLETION
TRANSACTION Ready
NATIVE_READY Zero
CMP40-REBAR: BEGIN requested=true bytes=8589934592
REBAR_SAVED
REBAR_PROBE
CMP40-REBAR: RESULT Active
HANDOFF_READY Zero
```

Markers are fragments of longer lines. Check that Windows and NVIDIA start
without Code 43, and that `40HXCheck.exe` or GPU-Z reports an 8 GiB BAR1.
ZERO intentionally leaves the compute selectors zero. After ZERO passes,
cold-boot FULL, collect `RF*.LOG`, check `NATIVE_READY Full` and
`HANDOFF_READY Full`, and repeat the accepted compute test with the same driver.
Record the image checksum, driver version, BAR1 size, compute result and logs.

`RESULT Active` establishes matching EFI BAR1 readback and two VRAM samples
that are not both all ones.
Driver behavior and the actual Windows BAR1 size must also pass. An EFI
success status alone does not establish a successful ReBAR test.

## Parent handoff

After the Windows checks pass, cold-boot FULL with `--return-to-limine` (or
`--return-to-bootloader`). ReBAR still uses the same file policy. Confirm the
menu returns, then boot the intended OS and check driver initialization,
compute and BAR1 size. Record any driver-side relocation, reset or independent
unlocker, since those can change what survives from EFI. A literal
`--experimental-rebar` also enables relocation without a policy file.

## Refusal, rollback and logs

`SKIPPED` retains stock BARs. `RESULT XveRejected`, `Bar1SizeRejected`,
`Bar3SizeRejected`, `NoWindow` or `ApertureRejected` means the C rollback path
was attempted; boot continues after native compute unlock completion. Preserve the
log and check the resulting BAR1 size instead of treating that run as success.

The allocator keeps the C algorithm: up to 12 candidates, 8 GiB alignment,
EFI memory-map exclusion and other bridges' prefetchable-window checks.
RootBridge `Configuration()` ranges are diagnostic. These checks do not prove
an ACPI `_CRS` grant, root-complex routing or safety for every PCI topology.
The Rust adapter limits bridge discovery to the GPU's PCI segment and rejects
malformed memory-map arithmetic. It never relocates BAR0.

Logging checkpoints precede ReBAR entry and follow return. PCI/MMIO operations
are buffered in RAM; a stalled firmware or VRAM access can leave only the
pre-ReBAR checkpoint. On a stall, black screen or `FAILURE_PARKED`, fully power
off before another attempt. Failed compute unlock completion never enters ReBAR.
Existing numbered logs are preserved.

The second `TRACE_BEGIN` block belongs to ReBAR. `P/Q` are GPU config
reads/writes, `B/J` are bridge config reads/writes (root bits 31:28, bus 27:20,
device 19:15, function 14:12, register 7:0). Empty bridge scan slots returning
all ones are omitted. `R/W` are BAR0 MMIO; `T` is memory-map top; `M/I/O` hold
candidate, span and overlap result; `V/D` hold VRAM address and value. `A/E`
hold diagnostic aperture minimum/maximum. Address pairs use high DWORD in
the first numeric column and low DWORD in the second. `dropped=0` is required
for a complete trace. `REBAR_SAVED` records original resources and bridge;
`REBAR_PROBE` records sizing and aperture checks.
