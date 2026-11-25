# EFI logging policy

FULL/ZERO use compact successful logs by default. The accepted logging-r1
FULL/ReBAR summary occupies 3662 bytes (about 3.6 KiB); its explicit diagnostic
log occupies 15422 bytes (about 15.1 KiB). These are measured sizes, not byte
caps. Input hashes, metadata, buffer addresses, FWSEC completion, native state,
ReBAR results and handoff
checkpoints remain available. These observations use existing reads.
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

Each phase still preallocates 131072 event slots before its hardware operation.
Retaining three phases can keep about 6 MiB of trace RAM live until output.
Recording, data-port omission and single-event run counts are unchanged.
Compression runs only when serializing the retained buffer. Filesystem and
console operations stay outside `Transaction::run`, and no extra GPU read,
write, delay or cache barrier is introduced by logging.

Checkpoint verification still checks write position, flushes/closes/reopens,
rereads every byte with a streaming checksum and checks EOF. The final seal
closes the file before Windows StartImage or parent return. A write or seal
failure refuses handoff; a possibly mutated GPU retains the parked failure
path with no reset or retry. An unwritable volume cannot guarantee a complete
failure trace. A hard firmware stall can leave only the last verified
checkpoint because buffered traces require control to return.

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
measurement of compute or BAR1. Storage rotation and installer integration
remain pending.

## Logging-r1 hardware test

This procedure remains available for repetition and changed variants.

Build and package a separate FULL/ZERO variant:

```bash
cargo build --locked --offline --release --target x86_64-unknown-uefi --target-dir target/logging-r1/normal -p cmp40-efi -p cmp40-efi-boot-fixture --features cpu-rm,adapters
python3 tests/package_cpu_rm.py --logging --image-dir target/logging-r1/normal/x86_64-unknown-uefi/release --output-dir target/logging-r1/usb
```

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

## Retention work still pending

This revision preserves every existing numbered `RF`/`RZ` log. It neither
deletes old files nor implements a total storage budget. Number selection
still searches 1 through 999 and refuses when no unused name remains.

The proposed next step retains up to 32 summaries and four full traces within
an 8 MiB total budget, with the byte limit taking precedence. Rotation must
recognize owned logs, account for temporary files, preserve a previous verified
record until its replacement is verified, and provide an explicit migration /
export path for old diagnostics. Unrelated files must survive.
Validation must cover hundreds of simulated runs, interrupted writes,
invalid/stale files, variable sizes, I/O errors and `VOLUME_FULL` on a small
ESP. Go checker/uninstaller support and hardware acceptance are required
before Rust becomes the installer's default image.
