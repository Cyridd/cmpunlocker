# CMP40HX Rust EFI port

The port builds a read-only EFI carrier and separate FULL and ZERO images
with opt-in ReBAR relocation. A shared library implements the unlock transaction.
The library compares its results with functions compiled from this repository's
C EFI source. The frozen carrier-r1 FULL/ZERO pair has
[Windows acceptance](tests/evidence/stage4c/README.md), and FULL has
[Limine/Linux acceptance](tests/evidence/stage4d/README.md) with proprietary
NVIDIA 620.70.06, GSP OFF. I also confirmed FULL compute unlock in Windows
with GSP enabled. See the [GSP test results](../../docs/results.md#gsp-enabled-on-linux-and-windows).
The Windows installer embeds the Rust FULL image. The installer
and standalone checker read its logs from the mounted ESP.
ReBAR-r1 now has [FULL/ZERO hardware acceptance](tests/evidence/stage5/README.md),
including `norebar` precedence, OS-visible 8 GiB BAR1 and retained FULL compute.

`core/` is `no_std`, uses no allocator or third-party dependencies, and forbids
unsafe Rust. It provides:

- Explicit little-endian serialization of the 256-byte WPR metadata for the
  8 GiB CMP40HX profile, including the measured 960 KiB heap.
- Radix planning and construction in a caller-owned buffer, with the current
  root + one L1 + L2 + image arrangement.
- The existing bounded UTF-16LE boot-option matching.
- Initial-state and native FULL/ZERO completion checks, including GSP HALT/idle.
- The current reserved-page allocation policy and PTIMER seed arithmetic.
- Backend-driven GFW stop, FWSEC WITH_LOADER, SEC2 port loading and sparse
  Booter completion, with per-attempt failure state and no cleanup/retry tail.
- Bounded file-path comparison to avoid recursively launching the application
  when it has been placed at the Windows loader path.
- Read-only FAT16/FAT32 lookup and size-consistent file chains, LFN checksum and
  sequence validation, bounded regions and cyclic-directory refusal.
- Primary MBR FAT partition and GPT ESP parsing, native 512/4096-byte blocks,
  header/table CRC checks and checked partition bounds.
- The C EFI's 8 GiB BAR1/BAR3 relocation, upstream-bridge window changes,
  memory-map and bridge conflict checks, sizing/readback and rollback.
- An optional Gen2 experiment with masked policy preparation, live capability
  checks, GPU-only leaf-bus validation and one immediate-port retrain.

## Experimental FULL/ZERO images

Build in this directory using the pinned inputs already used by the C loader:

```bash
cargo build --locked --offline --release --target x86_64-unknown-uefi -p cmp40-efi -p cmp40-efi-boot-fixture --features unlock,adapters
python3 tests/test_assets.py
python3 tests/qemu_smoke.py --unlock --adapters --raw
python3 tests/package_unlock.py
```

The `unlock` feature produces `cmp40-unlock-full.efi` and
`cmp40-unlock-zero.efi`. Frozen baselines retain their original filenames.

The additional QEMU cases execute FULL/ZERO discovery/refusal without NVIDIA,
verify actual FAT log bytes after VM termination, and verify that a simulated
failed GPU attempt cannot return to its parent. `cmp40-logcheck.efi` is a
physical filesystem diagnostic using the same log code with no PCI/MMIO access.
It creates a managed log in `EFI/40HX/LOGS/` and reports `LOG_CHECK_READY`
after verification.
The test-only fixture binaries remain for QEMU; do not deploy them to hardware.

`app/prepare_assets.py` verifies complete source sizes/SHA-256 before publishing
generated inputs into Cargo's output directory. The GSP boot source is 128 KiB;
only its first 24 KiB are copied by C and embedded by Rust, with a separate
prefix hash. ZERO changes only the two original selector operands. The current
production Booter signature is reapplied at `0x8700` immediately before port
loading. The installer keeps its reviewed FULL image in `tools/inst40hx/embed/`
under `windows/`. Intermediate firmware and diagnostic images stay in Cargo's
output directory.

The experimental adapter preserves RootBridgeIo standard/compact addressing
and segment-zero CF8 fallback, assigned BAR0 below 4 GiB, high reserved slots
with the existing low fallback, identity DMA and WBINVD. It does not disconnect
GOP or establish IOMMU mappings. PCI resources stay assigned unless ReBAR is
explicitly enabled after native completion. Reserved pages are
retained on success and failure; allocation and copying follow the accepted
memory-r1 sequence, constructing the zero image directly in radix leaves.
Page padding is initialized before Rust slices are made.

The physical ZERO log showed WPR2 appearing while FWSEC was still running,
followed by Booter mailbox `0x91`. `Transaction::run` now waits up to 5 seconds
for GSP HALT, checks scratch0e's FRTS error field and rechecks WPR2 before SEC2
health/preparation. The new `FwsecCompletion` phase and `FWSEC_COMPLETION` log
record expose this boundary. This adds a read-only completion barrier to the
C entry's early WPR2 return; firmware writes, payloads and metadata stay the
same. The low-level C comparisons remain unchanged, while orchestration tests
cover delayed HALT, timeout and invalid FRTS/window results. Two subsequent FULL/ZERO runs succeeded after this fix.
Both needed 99 ms of additional completion waiting. See the
[acceptance record](tests/evidence/stage3e/README.md) for observations and limits.

Each image requires a writable image volume. Optimization-r1 stores managed
8.3 logs at `EFI/40HX/LOGS/L0000001.LOG` and subsequent generations, retaining
up to 32 summaries and four diagnostics within an 8 MiB file-byte budget.
Legacy root `RF`/`RZ` logs and unrelated files are preserved. Each
checkpoint checks write position, flushes/closes/reopens the file, verifies all
bytes with a streaming checksum and checks EOF. The log volume/partition is
printed to the console. Verification happens before firmware transaction entry
and after return, never inside the transaction.
The file records input hashes, target/access route, buffer addresses, all
metadata bytes and register observations. Successful runs use compact logs
with phase trace counters by default. The accepted logging-r1 FULL/ReBAR runs
produced a 3662-byte summary (3.6 KiB) and a 15422-byte diagnostic log (15.1 KiB).
I reported successful testing with no observed issues; the supplied pair
confirms native FULL completion, active 8 GiB ReBAR and zero trace drops in
both modes. See the [logging acceptance](LOGGING_POLICY.md#logging-r1-acceptance)
for the verified counters and scope. `--trace`, handled errors
before handoff, ReBAR refusal/rollback and capture overflow select full
compressed traces. Completed phase buffers remain available until the overall
result is known, so a late error retains earlier observations.
The core transaction records into preallocated RAM; no console/filesystem
call occurs during it. IMEM/DMEM data ports are omitted and counted. Other
operations retain their order and single-event counts. Serialization compresses
identical adjacent MMIO read/stall pairs using version 2 `REPEAT_PAIR` records.
`A` records the FWSEC address as high/low DWORDs; `dropped` reports capture loss.
Traces are appended after the final result and the log closes before handoff.
A hard firmware stall can leave only the last verified checkpoint.
The [logging policy and test procedure](LOGGING_POLICY.md) describe the format,
bounded host decoder, managed ownership/completion records and separate
`--optimize` USB package with a frozen logging-r1 FULL control. Buffered 4 KiB
writes preserve checkpoint verification, and a failed batch is never replayed.
Gen2/ReBAR trace capacities follow conservative topology bounds; compute unlock keeps
its previous capture limit. Radix tables are built on already initialized DMA
leaves, avoiding a duplicate 28 MiB zero pass. Raw loading batches aligned
reads and adjacent FAT clusters without bypassing chain checks.
Both FULL and ZERO completed the
[optimization-r1 hardware trials](LOGGING_POLICY.md#optimization-r1-hardware-acceptance)
with stock BARs under `norebar` and active 8 GiB BAR1 with ReBAR enabled.
Physical speedup for this variant remains unmeasured. `StartImage` errors
after GPU mutation now enter the parked failure path.

Only native completion permits Windows StartImage or parent return. After a
failed attempt that could have modified the GPU, the application parks at
`FAILURE_PARKED` with interrupts disabled and requires physical power-off.
Returning an error alone allows a parent/firmware boot fallback; it is used only
before GPU mutation. Failures perform no GPU cleanup or retry. Booter failures
print `BOOTER_FAILURE` with the exact gate/register/observed/expected value,
using existing reads. Incompatible handoff options are refused. Use the
[physical test guide](tests/UNLOCK_HARDWARE.md) for changed GPU variants.

## Experimental ReBAR

FULL and ZERO cache `EFI/40HX/REBAR.CFG` from their own image volume before GPU
mutation, using the same exact LF-terminated policy as the C EFI. A missing or
invalid file supplies no opt-in. `--experimental-rebar` also enables the path;
`norebar` overrides both sources. ReBAR starts only after native compute unlock
completion and does not change the firmware transaction or BAR0.

The allocator preserves the C sizing, 12-candidate placement and rollback
sequence. It searches above the EFI memory-map top and excludes mapped ranges
and other bridges' live prefetchable windows. RootBridge `Configuration()`
apertures are diagnostic. Bridge discovery uses the GPU's encoding and PCI
segment; CF8 remains segment zero. Malformed memory-map arithmetic refuses
placement. These checks do not establish an ACPI `_CRS` grant or root routing
on every topology. A failed ReBAR check attempts the C rollback and continues
handoff; a failed compute unlock transaction never enters ReBAR.

For a separate FULL/ZERO test USB, build and package an isolated variant:

```bash
cargo build --locked --offline --release --target x86_64-unknown-uefi --target-dir target/rebar-r1 -p cmp40-efi -p cmp40-efi-boot-fixture --features unlock,adapters
python3 tests/qemu_smoke.py --image-dir target/rebar-r1/x86_64-unknown-uefi/release --unlock --adapters --raw --rebar
python3 tests/package_unlock.py --rebar --image-dir target/rebar-r1/x86_64-unknown-uefi/release --output-dir target/rebar-r1
```

The archive contains both images, FULL at the USB fallback path, an enabled
policy, checksums and the [physical test guide](tests/REBAR_HARDWARE.md).
It preserves existing archives and excludes QEMU fixtures and the installer.
Cold-boot with `norebar` first, then compare ZERO and FULL with relocation
enabled. The [ReBAR-r1 acceptance record](tests/evidence/stage5/README.md)
covers all four controls on the tested configuration: the supplied logs verify
native completion and relocation, and my OS tests confirmed 8192 MB BAR1
with `nvidia-smi -q`/GPU-Z and compute unlock in both FULL runs. I observed
about 10% higher game FPS than with stock 64 MB BAR1. Physical rollback and
ReBAR parent/Linux handoff remain untested; the FPS observation is specific
to these tests.

`core/tests/c_rebar_reference.py` compiles actual C placement, sizing and
rollback functions against synthetic hardware boundaries. The two VRAM loads
become model reads; diagnostic Print arguments still execute. Tests compare
every PCI/MMIO read and write, including rollback order, and separately compare
ACPI descriptors and policy bytes. QEMU exercises actual policy-file loading,
literal opt-in, opt-out and unrelated-volume refusal without a GPU. ReBAR
checkpoints and a separate buffered trace are appended to the managed log;
a hard stall can leave only the pre-ReBAR checkpoint.

## Experimental pre-driver Gen2

The `gen2-experiment` feature adds three separate FULL images. They run after
native compute completion and before optional ReBAR: `cmp40-gen2-prepare`
applies the masked GPU policy, `cmp40-gen2-targets` also seeds both TLS fields
with 2, and `cmp40-gen2-retrain` additionally requests one upstream retrain.
Normal FULL/ZERO entries keep Gen2 off. `--no-gen2` disables the experiment
in all three new entries.

The policy uses the register masks documented in the
[Linux pre-driver Gen2 research](../../docs/research/CPU_RM_PCIE_GEN2.md), with
XP3G extensions excluded for this first EFI test. PCIe v2 fields are accessed
only after the live version becomes 2. Policy is applied even if Gen2
capability was retained, while OVR=6 is written last only when adoption is
needed. GPU and immediate upstream port targets use true 16-bit stores,
preserving adjacent W1C status. The path preserves unrelated policy/control
bits and requires both ends to retain their original width.

There are no resets, Link Disable, FLR, PnP operations, Windows helper drivers
or retrain retries. A healthy two-second Gen1 timeout keeps TLS=2 for OS
initialization and reports `Gen1Timeout`. Failures after mutation park through
the existing terminal path; saved TLS fields are restored only while they
remain owned by the experiment. This does not roll back partial MMIO policy.

Build the r2 diagnostic separately from the accepted ReBAR-r1 and Gen2-r1 binaries:

```bash
cargo build --locked --offline --release --target x86_64-unknown-uefi --target-dir target/gen2-r2 -p cmp40-efi -p cmp40-efi-boot-fixture --features gen2-experiment,adapters
python3 tests/qemu_smoke.py --image-dir target/gen2-r2/x86_64-unknown-uefi/release --unlock --adapters --raw --rebar --gen2
python3 tests/package_unlock.py --gen2 --gen2-revision r2 --image-dir target/gen2-r2/x86_64-unknown-uefi/release --baseline-full target/baseline-rebar-r1/cmp40-cpu-rm-full.efi --output-dir target/gen2-r2
```

The package includes the three experimental images, the frozen accepted FULL
control, a disabled ReBAR policy, checksums and the standalone
[hardware procedure](tests/GEN2_HARDWARE.md). Its fallback image is Prepare.
Disable both Windows Gen2 tasks and the `40HXGen2` Run entry before each cold
trial. The installer writes the Run entry under HKCU; check HKLM and custom
startup sources too. Existing installer behavior is unchanged.

`core/tests/gen2.rs` checks policy writes against actual C functions extracted
from `linux/preboot/kernel/gen2_core.h` on 128 synthetic inputs, and models
capability adoption, topology changes, target restoration and failed links.
The extracted GPL C runs only in the host reference harness and is not linked
into EFI. QEMU checks all three real image entries and `--no-gen2` without a
GPU. These checks do not establish physical link training or Windows retention.
Earlier Linux tests observed NVIDIA initialization restoring Gen1 after a
successful pre-driver Gen2 retrain. The Windows r1 Retrain log also reached
Gen2 x16 on both ends within 10 ms, before ReBAR. With NVIDIA 621.14, I
observed Gen1 under games and GPU-Z Render Test in all three Gen2 modes,
despite GPU-Z advertising PCIe 2.0 support. I confirmed compute unlock in
Base and all three modes with Gen2 helpers and ASPM disabled. All four r1 logs
show active ReBAR; the subsequent r2 controls isolate its effect.

The [OnlyEFI fork's post-bind helper](https://github.com/BardKing-CN/CMP40HX-Unlock-OnlyEFI/blob/f12f8bb92abb0f0a36da08bd1c9a8bcd858687c7/CMP40HX-Unlock-OnlyEFI-v0.1.1-GitHub-Repo/source/windows/CMP40HXGen2_prod.c)
restores CFG bits 18:19 and MISC bit 13 after NVIDIA initialization, then
requests upstream retraining using WinRing0/ThrottleStop. Source inspection
on 2026-11-25 found that its pre-driver policy and TLS do not remove this
post-driver step. These bits are already set by the Rust EFI policy; moving
the helper's writes into EFI would repeat that early preparation.

The r2 images retain the same Gen2 writes and add a read-only observation
after ReBAR, before `HANDOFF_READY`. `GEN2_FINAL_LINK` and `GEN2_FINAL_POLICY`
show whether the link or policy changed during later EFI work. The observer
has no write or delay interface, performs no repair/retrain, and rechecks
native compute state. A healthy Gen1 observation remains diagnostic; an
unreadable device, invalid topology, width loss or broken native state parks.
This sample precedes log sealing and OS entry; it cannot observe NVIDIA
initialization. Both r2 Retrain controls, with `norebar` and with
`--experimental-rebar`, reached Gen2 x16 in 10 ms and retained it on both ends
at the final observation. The final policy matched in both logs, every trace
reported `dropped=0`, and both reached `HANDOFF_READY Full`. I confirmed
compute in both Windows runs, 8 GiB BAR1 only with ReBAR enabled, and Gen1 x16
under load in both. GPU-Z still advertised a PCIe 2.0 x16 maximum.

The loss therefore occurs after the final EFI sample and also occurs without
ReBAR. Its exact cause during handoff or OS initialization remains unresolved.
The [hardware guide](tests/GEN2_HARDWARE.md) records these completed controls;
the r2 package has no demonstrated fix for Windows Gen2 retention.

The optional [stock-driver registry trial](tests/GEN2_HARDWARE.md#optional-stock-driver-registry-trial)
uses the same frozen r2 images with an explicit RM policy, then separately
tests `RmForceEnableGen2`. It requires NVIDIA 621.14 and an administrator
PowerShell terminal, with an exclusive backup and restoration of the changed
DWORDs. Its script uses built-in Windows APIs and adds no project dependency.
With NVIDIA 621.14, I observed no Gen2 under load in `Policy`,
`PolicyPlatform`, or a manual `RMPcieLinkSpeed=0x1` variant with the other
`PolicyPlatform` values, using both ReBAR command-line controls. I reported
equivalent EFI logs without errors; the additional logs were not supplied
for independent inspection. These tested settings do not provide Windows
Gen2 retention. The next diagnostic needs policy observations after driver
initialization; the current EFI observer cannot collect them.

For reproduction, package it from the frozen inputs without rebuilding EFI:

```bash
python3 tests/package_unlock.py --gen2 --gen2-revision r2 --gen2-registry --image-dir target/gen2-r2/x86_64-unknown-uefi/release --baseline-full target/baseline-rebar-r1/cmp40-cpu-rm-full.efi --output-dir target/gen2-registry-r1/usb
```

The packager checks the frozen image hashes, includes `WINDOWS/gen2_registry.ps1`
and selects Retrain as the USB fallback. It creates a separate
`cmp40-rust-gen2-registry-r1-usb.zip`; existing r1/r2 archives remain intact.
The host model check is
`pwsh -NoProfile -File tests/gen2_registry_model.ps1 -ScratchDirectory <scratch-directory>`.
It verifies value restoration, failed-apply rollback and device-selection
refusals without accessing a real registry or GPU. The negative hardware
results do not verify the model's failure/rollback paths on Windows. The guide
also covers restoration after the manually tested value 1.

## Build the installer EFI

The installer uses FULL with compute unlock, opt-in ReBAR and managed ESP logs.
The release image omits the Gen2 experiment and the ZERO control payload.
Build it on x86-64 Linux with Python 3 and Rust 1.100.0.

```bash
rustup toolchain install 1.100.0 --profile minimal --target x86_64-unknown-uefi
cargo +1.100.0 fetch --locked --target x86_64-unknown-uefi
RUSTUP_TOOLCHAIN=1.100.0 bash build_installer_efi.sh
python3 tests/compare_installer_efi.py \
  target/x86_64-unknown-uefi/release/cmp40-unlock-full.efi \
  ../tools/inst40hx/embed/40HXUNLK.EFI
```

Run these commands from `windows/efi-rust/`. The build remaps compiler paths
and omits variable linker metadata so CI can compare every byte. QEMU boots
the same FULL build. The Go test `TestEmbeddedRustEfiIsFull` checks the
SHA-256 of the installer copy.

## Local checks

Run these commands in `windows/efi-rust/` with Rust, Python 3 and a native host
C compiler. The C comparison currently runs on x86-64 Linux; it uses `gcc` by
default (`CC` overrides it, and `PYTHON` selects the Python executable).
On a fresh checkout, fetch the workspace dependencies with the command below
before running checks with `--offline`.

```bash
cargo fmt --all -- --check
cargo test --locked --offline -p cmp40-efi-core
cargo clippy --locked --offline -p cmp40-efi-core --all-targets -- -D warnings
cargo check --locked --offline -p cmp40-efi-core --lib --target x86_64-pc-windows-gnu
python3 -m unittest discover -s tests -p 'test_trace_log.py' -v
```

The Windows command checks the library; it does not run Windows test binaries
or require a Windows linker. The CI job uses Rust 1.100.0. This dependency-free
core declares Rust 1.85 as its minimum supported compiler.

## Build and boot-check the read-only EFI

The EFI application requires Rust 1.91 or newer, the `x86_64-unknown-uefi`
target and the locked uefi-rs 0.41 dependencies. Fetch once before offline builds:

```bash
rustup target add x86_64-unknown-uefi
cargo fetch --locked --target x86_64-unknown-uefi
cargo build --locked --offline --release --target x86_64-unknown-uefi -p cmp40-efi
cargo clippy --locked --offline --target x86_64-unknown-uefi -p cmp40-efi -- -D warnings
```

The result is `target/x86_64-unknown-uefi/release/cmp40-readonly.efi`. For a
separate FAT32 test USB, use that image as `EFI/BOOT/BOOTX64.EFI`; back up any
existing file first. Keep the shipped unlock EFI and system ESP intact.
The unsigned diagnostic image needs Secure Boot disabled or a signature trusted
by the firmware. It prints to the EFI console; physical-boot file logging is
not implemented in this stage.

PCI inventory uses a small read-only standard PciIo ABI adapter, with layout
assertions and no callable write/DMA methods. It does not access GPU BARs,
disconnect controllers, modify PCI configuration or NVRAM, or launch firmware.
It also exercises the shared RootBridgeIo Standard/Compact and CF8 finder and
reports `TARGET_ROUTE`. The read-only build exposes no callable RootBridge write
function or MMIO transaction. CF8 address selection is followed by config reads.

By default it prepares Windows from its own volume, or a single other volume
containing `\\EFI\\Microsoft\\Boot\\bootmgfw.efi`, then calls StartImage.
On supported FAT16/FAT32 volumes it reads through aligned BlockIo buffers and
passes the file to LoadImage as SourceBuffer with the exact volume/file device
path. The returned image's device must resolve to that volume. MBR partitions,
GPT ESPs and firmware partition handles are deduplicated by exact device path.
Unsupported raw formats may retain the firmware loading path; corrupt supported
formats and image/security errors do not trigger that fallback. GPU logging
still uses SimpleFileSystem outside the transaction.
Multiple candidates on other volumes cause `WINDOWS_AMBIGUOUS` and `NO_MAPPING`;
it does not guess a boot disk. Corrupt/security-rejected images stop the search.
`--return-to-bootloader`, `--return-to-grub` and `--return-to-limine` return
success to the parent instead. Follow the [carrier hardware procedure](tests/CARRIER_HARDWARE.md)
before accepting this changed loading path. A separate read-only archive can be
prepared with `python3 tests/package_carrier.py`; its default input is the
isolated `target/carrier-r1/readonly/` build. It includes only the carrier and
hardware guide, without QEMU fixture images.

For QEMU/OVMF checks, build the two test-only EFI targets as well:

```bash
cargo build --locked --offline --release --target x86_64-unknown-uefi -p cmp40-efi -p cmp40-efi-boot-fixture
python3 tests/qemu_smoke.py
```

The script checks x86-64 PE/COFF headers, EFI subsystem and base relocations,
then boots seven scenarios using read-only temporary FAT volumes and private
OVMF variables. The Windows target is a small test EFI, not a copy of Windows.
Logs and image hashes remain in `target/qemu-smoke/`; CI retains the serial
logs. Never deploy the fixture binaries to a real ESP or USB. The cases cover
own-volume and other-volume chainloading, parent return, missing/corrupt or
ambiguous Windows targets, and recursion avoidance. None uses GPU passthrough.
The [recorded stage-2 evidence](tests/evidence/stage2/README.md) includes the
seven passing logs and image hashes.
The later [hardware report](tests/evidence/stage2/hardware-report.md)
confirms CMP40HX discovery and Windows boot from another disk on the tested machine.

## What the comparison verifies

`core/tests/c_reference.py` extracts selected functions from
`windows/tools/unlock40x/unlock40x_v70.c`, compiles them in a temporary host
program, and supplies artificial input. It reads no other project's source.
The tests compare every metadata byte, every meaningful radix byte, boot-option
decisions, and EFI status categories under changed register snapshots.

The radix reference substitutes host allocation for EFI allocation and removes
its single `WBINVD` instruction. Table pointers are normalized to the supplied
DMA base. The Rust builder additionally clears last-leaf padding; the C source
does not define that padding's contents, so padding is tested separately.
The in-place zero-image builder is compared against the actual C builder fed
a zero-filled source, including the real image size and leaf-table boundaries.
The host reference uses `-fno-builtin` like the EFI build so Clang cannot replace
CHAR16 length loops with the host's incompatible 32-bit `wcslen` ABI.
These checks establish data and decision parity over the tested inputs.
PCI/DMA reachability, firmware execution and Windows driver compatibility
require separate validation.

`c_execution_reference.py` additionally compiles actual FWSEC/Booter/native
finish routines and the pre-FWSEC entry segment against synthetic host buffers.
It retains reads embedded in Print arguments, records WBINVD, and compares
every write, delay and read result. Only consecutive read groups are sorted,
because C diagnostic argument evaluation order differs across compilers.
`c_preparation_reference.py` exercises the actual allocation policy and PTIMER
seed, including high-slot exhaustion, occupied ranges and calendar fallbacks.
The [stage-3 record](tests/evidence/stage3/README.md) documents the tested cases.

An EFI adapter must use `Transaction::run`'s deferred FWSEC and Booter callbacks
to preserve allocation, recopy, signature-patch and cache-barrier timing.
`run_prepared` only supplies synthetic/prepared-buffer orchestration for host
checks. Input hashes, target identity, physical buffer ownership, Windows
preparation and terminal handoff remain the EFI caller's responsibility.

## Next stages

The [port record](../../docs/research/RUST_EFI_PORT.md) describes the fixed
runtime contract and remaining work. The accepted memory-r1 variant constructs the
zero-filled image directly in the radix buffer, removing 27.98 MiB of duplicate
reserved memory. Cold FULL/ZERO success and FULL compute unlock are confirmed
in the [acceptance record](tests/evidence/stage3f/README.md), which includes
the verified FULL log. ZERO success is reported without a raw log.
The provided pair is frozen under `target/baseline-memory-e1422c1/`; the earlier
pair remains under `target/baseline-9f0882b/`. Future variants should follow the
[physical procedure](tests/UNLOCK_HARDWARE.md). Carrier-r1 implements raw loading,
boot-option parsing and shared discovery; see [stage 4a](tests/evidence/stage4a/README.md).
Read-only Windows/NVIDIA preload, return to Limine and subsequent Linux boot
are confirmed. The separately packaged carrier-r1 ZERO/FULL pair now
has [Windows acceptance](tests/evidence/stage4c/README.md), including verified
raw logs and confirmation of FULL compute unlock. The original pair is frozen
under `target/baseline-carrier-gpu-fe02bf7/`. FULL parent/Linux compute handoff
also has [acceptance](tests/evidence/stage4d/README.md) with proprietary driver
620.70.06, GSP OFF and no independent Linux unlocker. ReBAR-r1 FULL/ZERO with and without `norebar`
is accepted on the tested configuration in [stage 5](tests/evidence/stage5/README.md).
