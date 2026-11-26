# EFI logging policy

FULL/ZERO use compact successful logs by default. The accepted logging-r1
FULL/ReBAR summary occupies 3662 bytes (about 3.6 KiB); its explicit diagnostic
log occupies 15422 bytes (about 15.1 KiB). These are measured sizes, not byte
caps. Input hashes, metadata, buffer addresses, FWSEC completion, native state,
ReBAR results and handoff checkpoints remain available. These observations
use existing reads. The logging-r1 sizes describe the frozen accepted images;
optimization-r1 adds the managed storage envelope described below.
The shipped Go installer still uses the C EFI.

## Summary and diagnostic modes

The case-sensitive load-option token `--trace` requests all retained traces.
It is independent of `norebar`, `--experimental-rebar` and parent handoff.
Handled errors before handoff automatically select full traces. A requested
ReBAR operation that cannot become `Active`, including a best-effort rollback,
also selects full traces even when EFI can continue booting. Gen2 preparation
refusals and reported errors select diagnostics in experimental Gen2 images.
Any dropped event selects full output and keeps the nonzero loss counter.

All completed phase traces stay in RAM until the overall pre-handoff result
is known. A late error therefore retains earlier CPU-RM and Gen2 observations.
`LOG_DETAIL` states the selected mode and its reasons. Successful compact logs
contain `TRACE_SUMMARY` lines; diagnostic logs contain named `TRACE_BEGIN` /
`TRACE_END` blocks for `CPU_RM`, `GEN2` and `REBAR` as applicable.
These records follow `HANDOFF_READY` on success and precede `ABORT` on failure.
`HANDOFF_READY` alone does not establish a sealed log.

CPU-RM still preallocates 131072 event slots before its hardware operation.
Gen2 and ReBAR use conservative bounds based on up to eight roots and the GPU
bus number, capped at the same 131072-slot limit. Bounds include dense live
functions, repeated conflict scans, capability walks, retrain polling and
restoration. Small topologies use smaller buffers; large topologies keep the
previous limit. Recording, data-port omission and single-event run counts
are unchanged. Capture limits are explicit even if an allocator supplies
more capacity than requested.
Compression runs only when serializing the retained buffer. Filesystem and
console operations stay outside `Transaction::run`, and no extra GPU read,
write, delay or cache barrier is introduced by logging.

Checkpoint verification still checks write position, flushes/closes/reopens,
rereads every byte with a streaming checksum and checks EOF. The final seal
closes the file before Windows StartImage or parent return. A write or seal
failure refuses handoff; a possibly mutated GPU retains the parked failure
path with no reset or retry. An unwritable volume cannot guarantee a complete
failure trace. A hard firmware stall can leave only the last verified
checkpoint because buffered traces require control to return. `StartImage`
errors after GPU mutation also enter the parked path. This error is printed
to the console after the pre-handoff log has been sealed; that log alone
cannot establish successful Windows initialization.

## Managed log storage

Optimization-r1 creates `EFI/40HX/LOGS/L0000001.LOG` and subsequent 8.3 names.
The header identifies format version 3, the generation and FULL/ZERO or
diagnostic control. A completion footer records the selected summary/trace
class, preceding byte length and FNV checksum. Trace blocks inside this
envelope retain version 2. The host decoder checks the envelope before
expanding events and recalculates its footer when converting a complete log.

The logger retains at most 32 summaries and four diagnostics within an 8 MiB
file-byte budget. An active record's buffered bytes and reserved footer count
toward that budget. Until its class is known, one working record can exist
alongside retained files; once classified, its class limit also applies.
Interrupted owned files count as diagnostics. Directory metadata and FAT
allocation rounding are outside the file-byte budget and can still consume
filesystem space.

Rotation recognizes only numbered files whose header generation matches the
filename. It rechecks ownership and content before each deletion. Unknown
files and legacy root `RF`/`RZ`/`RL`/`RE` logs are preserved and excluded from
the managed budget. Corrupt owned records remain diagnostic records; they
cannot serve as the verified record protected during replacement.

Count pressure removes the oldest eligible record of the affected class.
Byte pressure prefers old diagnostics before small summaries. The most recent
verified record remains protected until a new record is fully written,
reopened, checksum/EOF verified and closed. If both cannot fit, the logger
refuses continuation instead of deleting that last verified record.
Generation numbering continues beyond 999, up to 9999999; a bounded directory
scan refuses more than 4096 entries or exhaustion of the numbering space.
The existing directory must be readable, and required filesystem operations
must succeed.

Legacy log migration remains explicit: copy/export old logs before deciding
whether to remove them. This implementation performs no automatic migration
or deletion of those files. The shipped Go checker/uninstaller still needs
managed-format support before installer integration.

## Output and memory costs

Output buffers 4 KiB batches and writes complete metadata lines, reducing the
256-byte metadata dump from 272 append calls to eight. Every physical batch
retains the successful-write position check. Checkpoints drain the buffer and
retain full streaming checksum/EOF verification. A short write, I/O error or
failed verification permanently poisons that output; its pending batch is
never replayed, and the file cannot receive a valid completion footer.
UEFI error statuses are preserved; warnings returned as errors cannot bypass
the CPU-RM failure tail. `LOG_IO` reports batches before the final seal.

Reserved radix memory is initialized before Rust slices are formed. Its table
builder now clears only the tables, avoiding a second full 29405184-byte
zero pass over the already initialized allocation. The general zero-image
builder retains its behavior for arbitrary initialized buffers.
Raw reads batch up to 64 KiB through aligned storage. FAT reading first
validates the complete chain, then combines adjacent clusters into bounded
runs, preserving fragmentation, partial tails and error handling. Console
`RAW_FILE_READ` records report firmware calls and blocks read.

These changes reduce initialization work and filesystem calls. Physical boot
time has not been measured for optimization-r1. The accepted Booter holds of
three seconds and one second, firmware callbacks and GPU operation order
remain unchanged.

## Trace format version 2

The event lines keep the legacy syntax. Identical adjacent MMIO read / stall
pairs serialize as the first two lines followed by:

```text
R 001100f4 00000000 count=1
S 00000000 00000064 count=1
REPEAT_PAIR additional=9999
```

This represents 10000 pairs. `additional` repeats both preceding event lines
in order, including each line's `count`. A changed address, value, delay or
count ends the run. Writes and other event kinds interrupt pair compression.

The header distinguishes captured event records (`records`), emitted body
lines (`encoded_records`) and retained operations (`operations`, the sum of
event counts before pair compression). `omitted_data_ports` counts the
intentionally excluded firmware data-port accesses. `dropped` counts capture
loss; zero is required for a complete retained control trace. Pair compression
does not restore omitted or dropped operations.

`tests/trace_log.py` reads legacy blocks and version 2. It validates counts,
pair placement and complete block boundaries, limits each expanded block to
131072 event records, and leaves single-event counts unexpanded. Unsupported
versions, malformed repeats and incomplete blocks are refused. It uses only
Python's standard library, limits total input and output to 64 MiB each, and
never overwrites an output file.

```bash
python3 tests/trace_log.py RF000001.LOG
python3 tests/trace_log.py RF000001.LOG --expand --output RF000001.expanded.LOG
```

The first command checks trace blocks. A summary has no event blocks to expand.
The second restores legacy event-line syntax for existing analysis tools.
`--compress --output <new-log>` supports offline conversion of old logs;
the original file is preserved.

## Logging-r1 acceptance

On 2026-11-25, I reported successful logging-r1 testing with no observed issues.
The supplied pair covers FULL with ReBAR enabled, without and with `--trace`.
Inspection confirms the following results:

| Observation | Default summary | Explicit `--trace` |
| --- | --- | --- |
| File size | 3662 bytes (3.6 KiB) | 15422 bytes (15.1 KiB) |
| Log mode | `summary`, `requested=false` | `trace`, `requested=true` |
| Native CPU-RM result | `Ready`, `NATIVE_READY Full` | `Ready`, `NATIVE_READY Full` |
| ReBAR result | `Active`, 8 GiB BAR1 | `Active`, 8 GiB BAR1 |
| Handoff checkpoint | `HANDOFF_READY Full` | `HANDOFF_READY Full` |
| Retained CPU-RM records / operations | 20499 / 20594 | 20499 / 20594 |
| Retained ReBAR records / operations | 111 / 111 | 111 / 111 |
| Dropped events | 0 in both phases | 0 in both phases |

Both logs report `failed=false`, `diagnostic=false`, direct Windows preparation
from `RawBuffer` and 99 ms of additional FWSEC completion waiting. The default
log contains phase summaries and no event blocks. The diagnostic log contains
two valid version 2 blocks: CPU-RM emits 307 body records for its 20499 captured
records, and ReBAR emits 111. The bounded decoder validates the expanded counts
against the headers, which agree with the summary log's counters.

This pair establishes the successful FULL/ReBAR logging behavior on the tested
configuration. It supplies no new ZERO or `norebar` log, physical rollback /
failure trace, Gen2 result or parent/Linux handoff evidence. EFI observations
and my reported successful testing remain distinct from an independent OS
measurement of compute or BAR1. These runs predate managed rotation.

## Historical logging-r1 hardware test

This procedure remains available for repetition and changed variants.

Keep the accepted logging-r1 pair and archive frozen as controls. Build the
current sources using the optimization-r1 commands below.

The archive contains `EFI/CMP40/FULLLOG.EFI`, `ZEROLOG.EFI`, FULL at
`EFI/BOOT/BOOTX64.EFI`, an enabled `EFI/40HX/REBAR.CFG`, this guide and
checksums. `TOOLS/trace_log.py` is the standalone host decoder. It contains
no Gen2 experiment, Windows helper or installer. Keep the accepted binaries
available for recovery and preserve any existing USB fallback loader.
Use the firmware and NVIDIA settings from the accepted ReBAR tests.

Fully power off between runs. Test ZERO, then FULL, using both `norebar` and
the enabled ReBAR file policy. For each control, compare a normal run with
the same command line plus `--trace`. For example:

| Command line | Expected log | ReBAR |
| --- | --- | --- |
| `norebar` | Summary | Disabled |
| `norebar --trace` | Full compressed traces | Disabled |
| Empty | Summary | Enabled by the supplied file |
| `--trace` | Full compressed traces | Enabled by the supplied file |

With USB fallback boot and no load options, the first enabled-policy summary
can be tested directly. Use the existing bootloader entries to pass `--trace`
or `norebar`; the file policy does not enable tracing.
Collect each new `RF*.LOG` / `RZ*.LOG` from the image volume and record its
size, image checksum, Windows initialization, BAR1 size and FULL compute
result. The expected enabled result is `CMP40-REBAR: RESULT Active` with
8192 MB BAR1 in Windows. Disabled runs should retain stock BAR1.
Check `LOG_DETAIL mode=summary`, `TRACE_SUMMARY` and `HANDOFF_READY` for normal
success; explicit diagnostics should report `mode=trace`, version 2 and
`dropped=0`. Run the decoder against each diagnostic log.
Do not deliberately provoke a hardware rollback or failed GPU transaction;
the host models and QEMU fixtures cover those logging selections.

## Optimization-r1 hardware acceptance

On 2026-11-26 I tested FULL and ZERO in Windows and observed that the
expected unlocks persisted. All four EFI runs reached `HANDOFF_READY` for
the selected control. The host decoder validated the byte count and checksum
in each completion footer.

| Load options | Generation (FULL / ZERO) | EFI ReBAR result | Log size per run | Write batches before seal |
| --- | --- | --- | ---: | ---: |
| `norebar` | 1 / 2 | Skipped, stock BARs retained | 3274 bytes (summary) | 11 |
| `--trace` with ReBAR enabled | 3 / 4 | Active, 8 GiB BAR1 | 15583 bytes (trace) | 14 |

Each CPU-RM trace counted 20594 operations across 20499 records. Both
diagnostic logs also captured 111 ReBAR operations. Every phase reported
`dropped=0`. The four runs were below the rotation limits. They provide no
boot-time measurement or per-run Windows BAR1 readout.

## Optimization-r1 hardware test

Build into a new directory and package with the frozen logging-r1 FULL:

```bash
cargo build --locked --offline --release --target x86_64-unknown-uefi --target-dir target/opt-r1/normal -p cmp40-efi -p cmp40-efi-boot-fixture --features cpu-rm,adapters
python3 tests/package_cpu_rm.py --optimize --image-dir target/opt-r1/normal/x86_64-unknown-uefi/release --baseline-full target/logging-r1/normal/x86_64-unknown-uefi/release/cmp40-cpu-rm-full.efi --output-dir target/opt-r1/usb
```

`FULLOPT.EFI` and `ZEROOPT.EFI` are the new images. FULL is also the USB fallback;
`BASEFULL.EFI` is the hash-checked accepted logging-r1 control. The archive
includes an enabled ReBAR policy, checksums, this guide and the host decoder.
It contains no Gen2 experiment, installer or Windows helper.

Extract onto a separate writable FAT test USB, preserving its existing
fallback loader. Use the accepted firmware and NVIDIA settings. Fully power
off between trials. First compare `BASEFULL.EFI` with cold ZEROOPT and FULLOPT
using `norebar`, then test enabled ReBAR and repeat FULL with `--trace`.
Check Windows initialization, FULL compute, stock versus 8192 MB BAR1, native
completion, ReBAR results and `dropped=0` as in the historical procedure.
Collect new logs from `EFI/40HX/LOGS/`; BASEFULL still writes root `RF*.LOG`.
Run `TOOLS/trace_log.py` against the new managed logs and require a valid
completion footer. Record `LOG_IO` and any measured boot durations separately
from firmware-reported wait times.

The host tests exercise 500 simulated retention runs, malformed ownership,
quota pressure and short/failed writes. QEMU exercises real FAT files,
interruption, legacy/foreign preservation, numbering above 999, a full
filesystem and an actual failing StartImage child. GPU DMA scheduling,
Windows retention and filesystem behavior on other firmware require
separate hardware trials. The Go checker and
uninstaller still need support for managed logs before the installer can
ship the Rust image.
