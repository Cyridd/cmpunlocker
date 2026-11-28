# Rust EFI port: carrier, compute unlock core and experimental FULL/ZERO

The port starts on branch `port/rust` from commit `1c94f8e`. Stage 1 adds
[`windows/efi-rust/`](../../windows/efi-rust/README.md), a pure Rust core and
differential tests against the existing C EFI. The shipped EFI, its embedded
copy, installer, NVIDIA assets and payload are not changed by this stage.

## Runtime contract to preserve

The reference is the Windows build selected by `build_v70.sh`:
`CPU_RM_HANDOFF`, `HANDOFF_PRELOAD_DEFAULT`, and `STAGE_CONTROL=9` (FULL) or
`10` (ZERO). Porting preserves:

- CMP40HX TU106, PCI identity `10de:1f0b`, 8 GiB framebuffer.
- The existing signed Booter and board-specific FWSEC inputs.
- The existing FULL/ZERO payload bytes and their verified SHA-256 values.
- A `0x1bfadc8`-byte dummy image, `0x6000`-byte boot image, code offset `0x1800`,
  data offset `0x800`, and manifest offset zero.
- Heap size `0xf0000` (960 KiB), WPR start `0x1fe000000`, heap start
  `0x1fe100000`, image start `0x1fe1f0000`, and WPR end `0x1fff00000`.
- Native Booter completion, including HALT, mailbox 0, exact selectors,
  WPR2 teardown, and stock FEAT PLM `0xffffff8f`.
- No final FLR or extra SEC2/GSP cleanup. The current delays, sparse reads,
  and absence of a retry in the production controls are part of the baseline.
- Early Windows image preparation, parent-bootloader return options, and
  explicit experimental ReBAR opt-in with `norebar` taking precedence.
- Existing RootBridgeIo address encodings and CF8 fallback when the hardware
  access layer is ported; a PciIo-only replacement needs separate validation.

The independent implementation uses the current project's wire formats and
observable behavior. No code is imported from `nv-uefi-unlocker`.

## Stage 1 implementation and acceptance

The `cmp40-efi-core` library serializes bytes explicitly, builds radix tables
in caller-owned memory, parses existing boot policy, and checks recorded
register snapshots. It uses `no_std`, no heap, no dependencies and no unsafe
code. The host comparison compiles actual C functions instead of a manually
rewritten C reference.

The acceptance checks cover:

- All 256 metadata bytes with three sets of low/high DMA addresses, for both
  payload and stock signature sizes; fixed assertions also pin the measured
  compute unlock geometry independently of changes to the C reference.
- Radix output at partial-page boundaries, across leaf 511/512, and at the
  actual dummy-image size. Last-leaf padding and the caller's buffer tail are
  checked separately.
- Existing boot options, aliases, conflicting handoff modes, case sensitivity,
  whitespace, NUL termination, odd trailing bytes, binary OptionalData, and
  missing/invalid/disabled/enabled ReBAR policy.
- Entry and FULL/ZERO Booter statuses under selector, WPR2, CPU, mailbox,
  and PLM changes, including unreadable register values.
- Invalid DMA ranges and unsupported radix sizes refusing before stores.
- Formatting, Clippy with warnings denied, and Windows-target library checks.

The Rust API adds numeric DMA range/alignment validation and rejects images
that would exceed the existing layout's one L1 page. These rejection cases
are not executed through the C reference, whose corresponding input bounds
are not checked. An accepted numeric address does not establish actual DMA
mapping or GPU accessibility.

## Stage 2: EFI carrier and emulated boot validation

The `cmp40-efi` application now builds for `x86_64-unknown-uefi`, using uefi-rs
0.41 for standard EFI services. Its PCI adapter exposes only config reads and
GetLocation, with static x86-64 protocol-layout assertions. It inventories
devices, counts `10de:1f0b` targets and performs no application GPU writes.

Windows image preparation uses complete volume device paths. The application
tries its own volume first; otherwise it accepts one other Windows candidate,
refuses multiple candidates, and stops on corrupt or security-rejected images.
It preserves the parent's return aliases. It skips its own Windows-loader path
to avoid recursive launches and cleans up rejected/unstarted image handles.
EFI applications are unloaded by firmware on exit; returned child handles are
not reused. A raw LoadImage call retains the error-path out-handle that the
uefi-rs 0.41 wrapper otherwise discards.

Seven QEMU/OVMF cases verify actual EFI entry, PCI reads, LoadImage/StartImage,
parent resumption and failure paths with no NVIDIA device. The private FAT
volumes contain small test EFI images instead of real Windows. The smoke
runner verifies the PE32+ image, EFI subsystem, entry point and base relocations,
and records serial logs and image hashes. CI builds and runs the same cases.
The [stage-2 evidence](../../windows/efi-rust/tests/evidence/stage2/README.md)
records the seven passing boot cases and identifies the tested EFI images.

Physical CMP40HX discovery was subsequently confirmed (`0000:10:00.0`,
`10de:1f0b`, 34 PCI handles, one target, zero read errors) and successful
Windows boot from another disk. The [physical report](../../windows/efi-rust/tests/evidence/stage2/hardware-report.md)
records the reported partition layout and evidence limits. This accepts the
read-only hardware milestone for that machine. RootBridgeIo/CF8 fallbacks,
the C loader's raw BlockIo/FAT path and file logging remain pending.

## Stage 3a: compute unlock firmware operations against a host backend

The independent `no_std` core now implements:

- The existing reserved-page placement policy: 12 dynamic high slots, the
  three static high fallbacks, then AllocateMaxAddress below 4 GiB. Dynamic
  success advances by allocation size plus 16 MiB; static/low success does
  not advance the cursor. No automatic release is introduced.
- PTIMER seed arithmetic and the original high-word/low-word write order.
  The C arithmetic omits the day of the month and timezone; the port keeps
  that behavior. Invalid calendar fields additionally use the existing
  fallback instead of risking out-of-bounds month-table access.
- GFW stop, reset propagation reads and the conditional SEC2 Falcon switch;
  the board's FWSEC WITH_LOADER descriptor and FRTS command patch; IMEM/DMEM
  port loading, tags and checked readbacks; the direct Booter launch with
  full 64-bit metadata address and sparse completion reads.
- Native FULL/ZERO completion: exact selectors, WPR2 down, stock FEAT PLM,
  Booter HALT/mailbox 0 and idle/halted GSP checks. The final path performs
  no reset, mailbox clear, FLR or DMA release.

`Transaction::run` retains the attempt state on error and refuses reuse before
any backend call. Its deferred callbacks keep FWSEC allocation/copy after
GFW stop, and the Booter recopy/signature patch immediately after SEC2 health.
These callbacks must flush the copied buffers before the firmware routines.
`run_prepared` is a host convenience for synthetic, already prepared buffers.
The EFI adapter must retain one transaction object for the boot attempt and
must not replace it to retry after failure.

The tests compile the actual corresponding C functions and the pre-FWSEC
main-entry segment, retaining diagnostic register reads and replacing WBINVD
with recorded barriers. Synthetic buffers exercise 147 firmware-operation
comparisons, 896 reserved-allocation comparisons and 180 PTIMER seeds. Writes,
delays and barriers retain exact order. Consecutive read groups are sorted
to accommodate C's unspecified diagnostic argument evaluation order; every
read and returned value is still compared. Additional tests run the whole
prepared firmware sequence, verify deferred preparation ordering, reject
malformed inputs, and confirm that failure never retries or adds cleanup.
GCC and Clang both pass the new comparisons. See [stage-3 evidence](../../windows/efi-rust/tests/evidence/stage3/README.md).

The core includes no firmware or payload bytes and no `unsafe` code. The host
execution milestone supplies no EFI adapters; those belong to stage 3b below.

## Stage 3b: experimental EFI compute unlock integration

The opt-in `unlock` feature builds separate FULL/ZERO EFI applications. The
default application remains the read-only carrier. The adapters implement the
existing RootBridgeIo encodings/CF8 route, verified BAR enable, volatile MMIO,
PTIMER seeding and retained reserved memory. High-slot placement and payload,
Booter, boot image, dummy image, radix, metadata and deferred FWSEC allocation
follow the C order. Booter recopy/signature patch remains in the deferred callback.

Source assets are verified by full length and SHA-256 before output publication.
The original 128 KiB GSP boot input produces the exact 24 KiB C runtime prefix,
with an independent hash; FULL/ZERO and the signature match the existing inputs.
No generated asset is committed. The unlock uses its 960 KiB heap and native
completion with no reset/cleanup/FLR/retry tail. Windows is prepared before PCI
mutation, or the existing parent return option is honored after completion.

Numbered logs preserve earlier runs and record identity/route, asset hashes,
allocated addresses, metadata and a bounded, preallocated transaction trace.
The trace captures existing observations rather than adding diagnostic MMIO.
Firmware data ports are omitted and counted; repeated events are compressed.
No disk/console operation occurs inside `Transaction::run`. The trace is dumped
after return, and logs close before terminal handoff. Hard stalls leave only
the checkpoint written before transaction entry. This differs from C's in-flight
printing and must be considered when comparing physical behavior.

The [stage-3b evidence](../../windows/efi-rust/tests/evidence/stage3b/README.md)
records checks and image hashes. Four PCI policy tests cover scan encodings,
bus bounds/multifunction discovery and BAR refusal/readback. Asset checks include
corruption outside the runtime prefix and refusal without changing prior output.
QEMU exercises both no-NVIDIA paths in addition to the seven carrier cases.
The [hardware guide](../../windows/efi-rust/tests/UNLOCK_HARDWARE.md) defines the
remaining ZERO/FULL comparison; physical GPU transactions are not yet accepted.

## Stage 3c: first ZERO failure and diagnostic recovery

The first reported ZERO hardware run used Limine `protocol: efi`, empty
`cmdline`, and a cold boot. It reached Booter, returned `DEVICE_ERROR`, then
Windows started with a long black screen and NVIDIA Code 43. The initial report
said no log was found on the Linux EFI volume; the log was later located and
the missing-log claim was withdrawn. That first report was transcribed from video;
the subsequent raw ZERO trace is documented in stage 3d below.

Returning an EFI error did not prevent Limine/firmware from selecting another
boot target. Failed attempts after possible GPU mutation now park without
returning to the parent, resetting the GPU or releasing DMA pages. A QEMU
parent/child test verifies that the shared terminal failure path does not resume
the parent after the failure marker. Pre-mutation errors can still return safely.

The logger uses short numbered `RZ`/`RF` names, detects short successful writes
through file-position checks, and flushes/closes/reopens/verifies content at each
checkpoint. LOGCHECK uses the same logger without PCI/MMIO access and creates
an `RL` log for a physical visibility check before another GPU attempt. QEMU
checks multi-chunk persistence, append positions, preservation of two logs and
the production LOGCHECK image on local FAT images. These remain verification
hardening; no original firmware logging failure is established.

Eight Booter gate diagnostics report the values already read; host comparisons
with C preserve the original register traffic and delays. No payload, firmware,
heap geometry or retry policy is changed. The [stage-3c record](../../windows/efi-rust/tests/evidence/stage3c/README.md)
contains the manual report and validation. Next: confirm LOGCHECK's visible file,
then obtain the corrected ZERO's exact failure detail before changing execution.

## Stage 3d: physical mailbox 0x91 and FWSEC completion barrier

The raw `RZ000001.LOG` identifies Booter `MailboxStatus=0x91`, halted SEC2,
`DEBUGINFO=0xdeadb00d`, unchanged ZERO selectors/stock FEAT PLM, and WPR2 still
up. The input code/signature and metadata mailbox readbacks passed. All 256
logged metadata bytes match the actual C builder for the physical addresses.
The trace contains no dropped records and all DMA buffers are above 4 GiB.
This does not prove SEC2 DMA read the same bytes or identify a unique cause.

FWSEC produced WPR2 after three explicit 1 ms waits, but the immediately
following SEC2 health snapshot still read GSP `CPUCTL=0`. Both C and the initial
Rust entry returned on WPR2 appearance without waiting for FWSEC HALT; C's
in-flight output adds incidental timing. The [Linux implementation](../../linux/preboot/kernel/unlock.c)
waits for HALT and checks scratch0e/FRTS. The reference TU10x analyses associate
`0x91` with WPR validation, but timing/geometry/DMA alternatives remain open.

`Transaction::run` now has `FwsecCompletion` between WPR2 appearance and SEC2
health. It performs a bounded 5-second HALT wait, checks scratch0e's high-half
FRTS error code, and rechecks the WPR2 window before preparing Booter. It reports
`FWSEC_COMPLETION` with the observed registers and wait. Timeout or invalid
results stop before Booter preparation/start. Firmware writes, source assets,
payloads, allocation order and no-retry/no-cleanup behavior remain unchanged.
This added read-only barrier intentionally differs from the early-return C
orchestration; low-level C comparisons are retained separately.

The [stage-3d evidence](../../windows/efi-rust/tests/evidence/stage3d/README.md)
archives the raw trace and physical metadata comparison. Model tests exercise
WPR2 appearing before HALT, delayed completion, timeout, FRTS error, window
drift and unreadable results, all without premature Booter preparation or retry.
Physical success of the new completion barrier is not yet established.

## Stage 3e: accepted FULL/ZERO hardware milestone

Supplied `RF000001.LOG` and `RZ000002.LOG` confirm both modes work, with FULL
compute unlock reported to survive OS handoff. Both logs observe 99 ms
of extra FWSEC completion waiting, then GSP HALT, zero FRTS error, native
Booter mailbox0 zero, exact FULL/ZERO selectors, stock FEAT PLM and WPR2
teardown. DMA placement and all metadata bytes are identical to the earlier
failed ZERO trace; the control traces of FULL/ZERO match apart from PTIMER
and selector observations. `DEBUGINFO=deadb00d` also appears on success.

The [stage-3e evidence](../../windows/efi-rust/tests/evidence/stage3e/README.md)
accepts the Rust compute unlock execution milestone on this tested configuration.
EFI hashes were not separately reconfirmed; numeric benchmark, driver-version,
repeated-boot and suspend/resume records were not supplied. Physical parent-return
is not covered by these `parent=false` runs. The accepted provided binaries
remain frozen while subsequent changes are evaluated separately.

## Stage 3f: accepted memory optimization

The memory-r1 runtime (`e1422c1`) constructs the zero-filled image directly in
the radix leaves, removing the duplicate 29,339,648-byte reserved allocation
(27.98 MiB of system RAM). Validation precedes stores; padding and unused
entries remain zero, with no change to GPU heap/WPR geometry, pinned firmware,
payloads, cache barriers or failure policy. Host comparisons cover the real
image and leaf boundaries; Clang's host C reference now uses the EFI build's
`-fno-builtin` to avoid an incompatible host `wcslen` substitution.

Successful cold FULL and ZERO runs with FULL compute unlock in the OS are
confirmed. `RF000002.LOG` verifies the expected shifted DMA addresses, all 256
metadata bytes against C, 99 ms of FWSEC completion waiting, native FULL state
and zero trace drops. Exactly five retained records differ from the earlier
successful FULL: two PTIMER writes, one FWSEC address event and two metadata
mailbox address observations. Every other retained record is identical.
ZERO's raw log was not copied, so its success is recorded as a reported observation.

The [stage-3f record](../../windows/efi-rust/tests/evidence/stage3f/README.md)
accepts memory-r1 on this tested configuration and freezes the provided pair in
`target/baseline-memory-e1422c1/`, preserving the earlier accepted pair.
Executable hashes were not separately reconfirmed; physical parent/Linux
handoff and other boards remain outside this acceptance.

## Stage 4a: raw loading, file policy and read-only discovery

The carrier now uses a dependency-free read-only FAT16/FAT32 parser over
aligned BlockIo reads for Windows preload and its own ReBAR policy file.
It checks BPB/partition bounds, active FAT selection, LFN sequence/checksum,
directory cycles and complete size-consistent file chains. Primary MBR FAT
partitions and GPT ESPs preserve their native logical block size; GPT header
and entry-table CRCs are verified. Exact volume paths deduplicate firmware
partition handles and whole-disk views. Extended MBR chains, FAT12, backup-GPT
recovery and raw filesystem writing are outside this implementation.

Windows image bytes reach LoadImage as SourceBuffer with the complete source
volume + filename path. Its loaded device must resolve to that volume; rejected
images are unloaded. Own-volume preference, ambiguous-candidate refusal and
self-Windows recursion checks remain. Unsupported raw formats can retain
firmware loading, while supported-format corruption and security/image failures
stop instead of bypassing verification. Logging still uses SimpleFileSystem
outside the GPU transaction, so raw preload alone is not an SFS-free logger.

`EFI/40HX/REBAR.CFG` reads preserve the C policy and `norebar` precedence.
The read-only carrier reports effective opt-in without resizing; FULL/ZERO
refuse it until ReBAR allocation is implemented. The read-only carrier also
exercises shared RootBridgeIo Standard/Compact and CF8 discovery without BAR
enable or GPU MMIO. The accepted compute unlock preparation/transaction and reserved
DMA code are preserved; this changed OS preparation still needs hardware checks.

Eleven new host disk tests cover FAT, fragmentation, LFN boundaries, cycles,
geometry, I/O errors, MBR and native-block GPT/CRC. All 39 core tests pass, along
with GCC/Clang comparisons, both EFI feature configurations and Windows checks.
All 25 QEMU scenarios pass; a failing filesystem shim forces raw preload/policy,
and GPT/FAT32 disks exercise actual 512/4096-byte BlockIo. Four further default
build cases verify the read-only carrier. The
[stage-4a record](../../windows/efi-rust/tests/evidence/stage4a/README.md)
archives evidence and identifies the isolated preflight image.

The [physical report](../../windows/efi-rust/tests/evidence/stage4a/hardware-report.md)
now accepts the default read-only carrier on the tested configuration:
Windows/NVIDIA preload works, return to Limine works, and Linux subsequently
starts. This is reported console/OS behavior without a raw capture or separately
reconfirmed image checksum. The supplied read-only archive is frozen unchanged
under `target/baseline-carrier-fe02bf7/`.

The previously QEMU-tested carrier-r1 ZERO/FULL images (`fe02bf7`) are packaged
without rebuilding, preserving the recorded EFI hashes. Follow the standalone
[GPU comparison guide](../../windows/efi-rust/tests/CARRIER_GPU_HARDWARE.md)
for cold ZERO/FULL Windows preload, then FULL return/Linux compute checks.
Their combined Windows GPU/carrier acceptance is now recorded in stage 4c
below; memory-r1 stays frozen as the earlier baseline.

The first reported combined ZERO/FULL attempts appeared to skip into Windows
without visible messages or logs, using existing working Limine entries and
matching image checksums. The [stage-4b record](../../windows/efi-rust/tests/evidence/stage4b/README.md)
provides separate ENTRYCHK diagnostics: early verified logs, policy/read-only
PCI checks, candidate LoadImage/unload and Windows preparation without any
StartImage or GPU transaction. A subsequently supplied successful `RE000001.LOG` followed removal of
`REBAR.CFG`, which was confirmed to have enabled ReBAR with value 1. This identifies the early unsupported-ReBAR gate before
RF/RZ log creation. The physical diagnostic is accepted; reuse the original
GPU pair with explicit `cmdline: norebar` for GPU comparison. That comparison
subsequently passed, as recorded below.

## Stage 4c: accepted carrier-r1 FULL/ZERO Windows path

Supplied `RZ000003.LOG` and `RF000004.LOG` accompany confirmation that
Windows/NVIDIA work in both controls and FULL compute unlock is present in
Windows. Both
logs identify RawBuffer handoff, 99 ms of FWSEC completion waiting, successful
native Booter status, expected FULL/ZERO selectors, stock FEAT PLM and WPR2
teardown, with 20,499 retained records and zero drops. All 256 metadata bytes
match the actual C builder using GCC and Clang and the earlier memory-r1 FULL.
FULL's retained trace differs from memory-r1 only at two PTIMER seed writes.

The [stage-4c record](../../windows/efi-rust/tests/evidence/stage4c/README.md)
accepts carrier-r1 Windows GPU/handoff behavior on this configuration. Original
provided images and archives are frozen unchanged under
`target/baseline-carrier-gpu-fe02bf7/`, preserving earlier GPU/carrier baselines.
Both logs use `parent=false`; FULL-to-Limine/Linux compute handoff is recorded
in the subsequent stage below. No runtime source or firmware input changed
for this evidence update.

## Stage 4d: accepted FULL parent/Linux handoff

`RF000007.LOG` records `HANDOFF_PREPARED parent=true source=None` followed by
successful native FULL completion. Return to Limine, Linux boot, working
NVIDIA and compute unlock are confirmed with **620.70.06 proprietary, GSP OFF**,
without another unlocker. Metadata and asset manifests match Windows FULL;
the retained trace differs only at two PTIMER seed writes, with 20,499 records
and zero drops. FWSEC completion waits the same additional 99 ms.

The [stage-4d record](../../windows/efi-rust/tests/evidence/stage4d/README.md)
accepts the GPU parent/Linux path on this configuration and closes outstanding
carrier/handoff acceptance. EFI-native completion is verified by the raw log;
post-return menu/driver/compute behavior is a reported observation. This does
not extend acceptance to GSP ON, other drivers/boards, repeated-boot statistics
or suspend/resume. Original carrier-r1 GPU images remain frozen unchanged.

## Stage 5: accepted ReBAR-r1 FULL/ZERO

I tested both controls with the enabled policy file, first with `norebar`
and then with an empty command line, and noticed no errors. Compute unlock
survived OS handoff in both FULL runs. In both ReBAR-enabled controls,
`nvidia-smi -q` and GPU-Z showed 8192 MB BAR1. I observed about 10% higher
game FPS than with the stock 64 MB BAR1. These are reported observations;
no game names, raw FPS samples, driver version or OS version were supplied.

Verified: inspection of all four complete logs confirms native FULL/ZERO,
`RawBuffer` Windows preload with `parent=false`, 99 ms of extra FWSEC waiting,
and zero trace drops. All 256 metadata bytes per run match the actual C
builder; input manifests match the pinned sources and derived runtime bytes.
Same-control compute unlock traces differ only at two PTIMER writes. The enabled
ZERO/FULL ReBAR traces are identical across all 111 records: 8 GiB BAR1,
32 MiB BAR3, placement at attempt 0, matching readback and two zero VRAM
samples. Both `norebar` logs skip ReBAR despite the reported enabled file.

The [stage-5 record](../../windows/efi-rust/tests/evidence/stage5/README.md)
contains redacted checkpoint excerpts and a report separating inspected EFI
results from my OS observations. This accepts ReBAR-r1 on the tested
configuration. The delivered archive and pair were preserved locally with
their original hashes, without rebuilding or changing runtime code. Physical
rollback and ReBAR parent/Linux handoff are outside these four runs; the FPS
observation does not establish a general performance gain.

## Remaining work and validation

1. **Experimental ReBAR:** the [ReBAR-r1 C allocator port](../../windows/efi-rust/README.md#experimental-rebar)
   has FULL/ZERO OS acceptance with explicit opt-in/`norebar`. Physical
   rollback and parent/Linux handoff with ReBAR still need validation.
   A resource planner and EFI backend with real routing, GCD reservation,
   decode-off extended configuration and consistent firmware/OS resource
   descriptions remain later work. Preserve the accepted carrier-r1 and memory-r1 pairs.
2. **Production logging:** replace unbounded per-boot diagnostic files with
   compact success summaries, failure/opt-in traces and a bounded retention
   budget. The [proposal](../../windows/efi-rust/LOGGING_POLICY.md) targets 8 MiB
   and at most four full traces, subject to the byte limit; existing logs remain
   intact during these milestones. This is a release gate for default Rust
   distribution on small Windows ESPs, with checker/uninstaller format support.
3. **Go distribution integration:** update the embedded Rust image, deployment
   checks and log support in the existing Go tools after carrier/ReBAR parity
   and bounded logging. C remains the installer default until those requirements
   are complete; a Go rewrite is not required.

Mapped DMA, map-based placement and explicit releases remain separate later
changes; none is required to accept the completed memory optimization.

The host tests do not model firmware scheduling, DMA coherency/reachability,
PCI routing, device reset propagation or driver initialization. QEMU without
GPU passthrough checks the EFI carrier and negative path; it does not validate
the NVIDIA transaction. The accepted Rust pair now provides a physical baseline
on this board; C remains the shipped image until carrier/distribution parity.

The Windows Go applications remain in place during EFI porting. They already
share `40hxcore` for diagnostics, configuration and uninstall operations. Rust
EFI integration should update their embedded image, deployment checks and log
support after carrier parity. A full Go rewrite is not a prerequisite and has
no established unlock/Gen2 performance benefit. Reassess a shared Rust library
or GUI replacement only when a concrete maintenance or product need warrants
the Windows API, localization, installation and recovery regression work.
