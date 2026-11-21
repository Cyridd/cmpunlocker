# Stage 3c: parked failure and persistent log diagnostics

Starting from `3cda611`, the first ZERO hardware run
[failed at Booter](physical-zero-failure.md). The Booter cause could not be resolved without exact register observations.
The original log was subsequently found, so no firmware logging failure is
established. The added logging diagnostics provide persistence checks. [Stage 3d](../stage3d/README.md) records the physical Booter trace.

Changes:

- A failed attempt after possible GPU modification cannot return to its parent.
  `FAILURE_PARKED` disables interrupts and halts until physical power-off.
- Eight Booter gate diagnostics retain the original MMIO read/write sequence,
  short-circuit behavior and delays, and report already observed values.
- Short log names (`RZ000001.LOG`, `RF000001.LOG`) preserve earlier logs.
  Position checks detect short successful writes; close/reopen/content/EOF
  verification runs at checkpoints outside firmware execution.
- Production LOGCHECK creates `RL000001.LOG` using the same logger, with no
  PCI/MMIO access. Confirm OS-visible persistence before another ZERO attempt.

25 core tests, C comparisons with GCC/Clang, asset checks, formatting and
Clippy passed. Twelve QEMU cases passed, including persisted multi-chunk
log bytes, two preserved logs, production LOGCHECK, and a failed child that
never resumes its parent. QEMU uses private local FAT images/variables and no
GPU passthrough, real Windows loader or host block devices.

Image/source hashes and case results are in [summary.json](summary.json).
The copied `.LOG` files are the bytes retrieved from the local FAT image
after VM termination, compared with exact expected fixture content.
These checks validate the logging/terminal paths under OVMF; they do not prove
an original missing-log defect or successful firmware execution on the tested board.
