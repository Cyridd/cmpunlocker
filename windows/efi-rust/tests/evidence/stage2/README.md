# Read-only EFI boot evidence

Recorded on 2026-11-17 using Rust 1.100.0, uefi-rs 0.41.0, QEMU 11.1.3,
and the x86-64 OVMF image identified in [summary.json](summary.json).
The files contain the application/fixture lines extracted from the serial
logs; unfiltered logs remain in the local `target/qemu-smoke/` runs.

| Case | Observed result |
|---|---|
| [windows](windows.log) | Own-volume LoadImage and StartImage reach the test Windows EFI; child returns success |
| [unique-windows](unique-windows.log) | One Windows candidate on another FAT volume is found and started |
| [parent](parent.log) | `--return-to-grub` returns success and the parent fixture resumes |
| [no-windows](no-windows.log) | No Windows target: NOT_FOUND, no StartImage attempt |
| [invalid-windows](invalid-windows.log) | Invalid PE input: UNSUPPORTED, no StartImage attempt |
| [ambiguous-windows](ambiguous-windows.log) | Two Windows candidates: NO_MAPPING, neither is started |
| [self-windows](self-windows.log) | The copied application at the Windows path skips itself; no repeated recursive start |

All cases enumerate emulated PCI devices with zero read errors and no CMP40HX.
No GPU is passed through. The Windows EFI is a fixture that returns a marker;
these results do not verify real Windows or NVIDIA initialization. No NVIDIA
firmware or real Windows image is included in this evidence.

Reproduce with the build and QEMU commands in [the port README](../../../README.md).

The later [physical boot report](hardware-report.md) records CMP40HX discovery
and reported successful cross-disk handoff to real Windows.
