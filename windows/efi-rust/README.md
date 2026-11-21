# CMP40HX Rust EFI port

The port builds a read-only EFI carrier, separate experimental FULL/ZERO CPU-RM
EFI images, and an independent library for the existing Windows CPU-RM contract.
The library compares its results with functions compiled from this repository's
C EFI source. The frozen carrier-r1 FULL/ZERO pair has
[Windows acceptance](tests/evidence/stage4c/README.md), and FULL has
[Limine/Linux acceptance](tests/evidence/stage4d/README.md) with proprietary
NVIDIA 620.70.06, GSP OFF. The shipped installer still uses the C EFI.

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

## Experimental FULL/ZERO images

Build in this directory using the pinned inputs already used by the C loader:

```bash
cargo build --locked --offline --release --target x86_64-unknown-uefi -p cmp40-efi -p cmp40-efi-boot-fixture --features cpu-rm,adapters
python3 tests/test_assets.py
python3 tests/qemu_smoke.py --cpu-rm --adapters --raw
python3 tests/package_cpu_rm.py
```

The additional QEMU cases execute FULL/ZERO discovery/refusal without NVIDIA,
verify actual FAT log bytes after VM termination, and verify that a simulated
failed GPU attempt cannot return to its parent. `cmp40-logcheck.efi` is a
physical filesystem diagnostic using the same log code with no PCI/MMIO access.
It creates `RL000001.LOG` and reports `LOG_CHECK_READY` after verification.
The test-only fixture binaries remain for QEMU; do not deploy them to hardware.

`app/prepare_assets.py` verifies complete source sizes/SHA-256 before publishing
generated inputs into Cargo's output directory. The GSP boot source is 128 KiB;
only its first 24 KiB are copied by C and embedded by Rust, with a separate
prefix hash. ZERO changes only the two original selector operands. The current
production Booter signature is reapplied at `0x8700` immediately before port
loading. No generated firmware or EFI binary is added to source control.

The experimental adapter preserves RootBridgeIo standard/compact addressing
and segment-zero CF8 fallback, assigned BAR0 below 4 GiB, high reserved slots
with the existing low fallback, identity DMA and WBINVD. It does not disconnect
GOP, establish IOMMU mappings or relocate PCI resources. Reserved pages are
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

Each image requires a writable image volume for a new numbered
`RF000001.LOG` or `RZ000001.LOG` (incremented for later runs). These 8.3 names
avoid long-name handling in older firmware. Old logs are preserved. Each
checkpoint checks write position, flushes/closes/reopens the file, verifies all
bytes with a streaming checksum and checks EOF. The log volume/partition is
printed to the console. Verification happens before firmware transaction entry
and after return, never inside the transaction.
The file records input hashes, target/access route, buffer addresses, all
metadata bytes and register observations. The core transaction records into
preallocated RAM; no console/filesystem call occurs during it. IMEM/DMEM data
port contents are omitted and counted. Other reads/writes, delays and cache
barriers are ordered, with consecutive identical events compressed. `A` records
the FWSEC address as high/low DWORDs. Trace overflow is reported with `dropped`.
After native completion, the trace is written and the log closes before handoff.
A hard firmware stall can therefore leave only the pre-transaction checkpoint.
The current logger retains diagnostic detail on every run. Before installer integration,
the [production logging proposal](LOGGING_POLICY.md) calls for compact successful
summaries, failure/opt-in traces and a bounded retention budget (proposed 8 MiB).
The carrier port does not delete old logs or change transaction tracing.

Only native completion permits Windows StartImage or parent return. After a
failed attempt that could have modified the GPU, the application parks at
`FAILURE_PARKED` with interrupts disabled and requires physical power-off.
Returning an error alone allows a parent/firmware boot fallback; it is used only
before GPU mutation. Failures perform no GPU cleanup or retry. Booter failures
print `BOOTER_FAILURE` with the exact gate/register/observed/expected value,
using existing reads. Incompatible handoff options are refused. Use the
[physical test guide](tests/CPU_RM_HARDWARE.md) for changed GPU variants.

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
reports `TARGET_ROUTE`. The no-CPU-RM build exposes no callable RootBridge write
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
before accepting this changed loading path. A separate no-CPU-RM archive can be
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
[physical procedure](tests/CPU_RM_HARDWARE.md). Carrier-r1 implements raw loading,
boot-option parsing and shared discovery; see [stage 4a](tests/evidence/stage4a/README.md).
Read-only Windows/NVIDIA preload, return to Limine and subsequent Linux boot
are confirmed. The separately packaged carrier-r1 ZERO/FULL pair now
has [Windows acceptance](tests/evidence/stage4c/README.md), including verified
raw logs and confirmation of FULL compute unlock. The original pair is frozen
under `target/baseline-carrier-gpu-fe02bf7/`. FULL parent/Linux compute handoff
also has [acceptance](tests/evidence/stage4d/README.md) with proprietary driver
620.70.06, GSP OFF and no independent Linux unlocker. Bounded production
logging and Go checker/uninstaller support are still required before changing
the shipped installer image.
