# CPU-RM core execution and preparation evidence

Stage 3a builds on carrier commit `0a82aa4`. The core remains `no_std`,
dependency-free and free of unsafe code. All host buffers use synthetic bytes;
no NVIDIA code is executed or reproduced in these test artifacts.

The full core suite contains 20 tests. The new differential cases are:

| Operation | Comparisons | Coverage |
|---|---:|---|
| GFW stop / pre-FWSEC SEC2 check | 15 | Normal state, Falcon switch recovery/refusal, all-ones config, residual BADF |
| FWSEC WITH_LOADER | 24 | Immediate/delayed WPR2, scrub timeout, invalid IMEM geometry, loader/descriptor readback mismatch, no WPR2, advisory RESET_READY timeout |
| Direct Booter | 24 | Low/high metadata addresses, pre-start HALT, NS/signature readback, mailbox write rejection, native mailbox error, no final HALT |
| Native finish | 84 | FULL/ZERO, GSP halt delay/timeout, engine/DMA/scrub state, selectors/WPR2/PLM faults, unreadable values, ignored mailbox 1 |
| Reserved allocations | 896 | Seven production page counts, exhaustion of all 12 dynamic / three static / low-memory candidates, two consecutive allocations with occupied ranges |
| PTIMER | 180 | Year bounds, leap years, month boundaries, unavailable RTC and unchanged day-of-month omission |

Each firmware routine is the actual function extracted from this repository's
C EFI; the pre-FWSEC case compiles its actual main-entry segment. Allocation
and timer comparisons likewise extract their actual C functions. Host adapters
retain Print argument reads, intercept reset/port access, use synthetic memory
and record WBINVD. Consecutive read groups are sorted for C argument-order
differences; every value and read count is compared. Writes, delays and barriers
remain in exact order. The C finish's seal/StartImage tail is checked separately
from the Rust core's verdict; Rust terminal handoff integration is still pending.

The behavioral model also runs 28 complete prepared-buffer firmware sequences
and checks deferred allocation/copy points, callback failure, malformed inputs
and reuse after success/failure. At most one FWSEC and one Booter STARTCPU occur.
Booter start is followed by the unchanged 3-second hold with no reads, one CPU
read, and a 1-second delay. Its tail issues no GPU writes or cache barriers.
There are no host selector/PLM writes, SEC2 reset or implicit DMA releases.

[validation.txt](validation.txt) records checks and [summary.json](summary.json)
records statuses, tool versions and hashes of the compared sources. New
comparisons run with both GCC and Clang. The library is checked for Windows
and UEFI; the existing carrier/fixtures are built and run in seven QEMU cases.

This evidence does not establish DMA reachability, physical allocation timing,
firmware scheduling, PCI routing or Windows CPU-RM initialization. The runner's
deferred callback points are tested, but their EFI allocation/copy implementations
are not yet present. The only physical Rust result is the earlier
[read-only hardware report](../stage2/hardware-report.md). No new unlock image is
deployed or substituted into the installer by this stage.
