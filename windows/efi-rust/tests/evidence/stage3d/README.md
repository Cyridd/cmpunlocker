# Stage 3d: physical ZERO 0x91 and explicit FWSEC completion

Baseline: `978fd6e`. `/path/to/RZ000001.LOG` was supplied from the new ZERO
run, and the originally reported missing log was confirmed to have existed.
LOGCHECK was not run. The retained log excerpt is stored as
[physical-zero-before-halt.LOG](physical-zero-before-halt.LOG).

Confirmed observations:

- Target `0000:10:00.0`, `BOOT0=166000a1`, RootBridgeIo Standard route.
- Initial ZERO selectors, WPR2 down; all allocated buffers above 4 GiB.
- WPR2 becomes `01ffee00:01ffe000` after three explicit 1 ms FWSEC waits.
- SEC2 health then reads GSP `CPUCTL=0`: HALT has not been observed.
- Booter code/signature and mailbox address verification pass; one SEC2 start.
- After the unchanged 3+1 second hold: SEC2 HALT, mailbox0 `00000091`,
  DEBUGINFO `deadb00d`, selectors still zero, stock PLM, WPR2 still up.
- 20,270 retained trace records, 14,943 omitted data-port operations, no drops.
- All 256 metadata bytes match the C builder for the logged physical addresses.

Interpretation: `0x91` is consistent with TU10x WPR validation rejection. The
trace proves the previous completion predicate allowed preparation while GSP
was still running; it does not prove GSP was still running at SEC2 START or that
this race uniquely caused `0x91`. DMA-read correctness and other live state are
not established by host metadata equality. No post-Booter GSP sample is present.
Cold-boot, image-hash and terminal-screen details for the new run were not supplied.

Correction: `FwsecCompletion` waits up to 5 seconds for HALT, then checks
scratch0e's FRTS error field and WPR2 before SEC2 health/Booter preparation.
The Linux CPU-RM path already uses this completion discipline. No additional
GPU writes, resets, payload changes or DMA releases are introduced.

27 core tests, existing GCC/Clang C comparisons, asset checks, formatting,
Clippy and 12 QEMU cases passed. New model cases reject premature preparation,
timeout, invalid scratch/window and unreadable result states without retry.
QEMU does not execute NVIDIA firmware; physical success remains pending.
[summary.json](summary.json) records the image/source/log hashes and analysis.

Stored log excerpts omit obsolete boot-option policy diagnostics. File checksums
describe these excerpts; EFI image hashes and GPU observations remain historical.
