# Stage 3b: experimental CPU-RM EFI integration

Recorded on 2026-11-17, from the stage-3 core baseline `84d8c2d`.

Separate FULL/ZERO EFI images now use the existing CPU-RM core through EFI
RootBridgeIo/CF8, MMIO and reserved-page adapters. Local input verification,
the exact GSP boot runtime prefix, deferred Booter signature patching, early
Windows preload/parent return and numbered file logs are implemented.

Validation passed:

- 24 host core tests, including 4 new PCI policy cases.
- 2 asset tests, with missing/corrupt/truncated input subcases and unchanged
  previous output after refusal. The production signature is compared with C.
- Existing preparation/transaction comparisons with both GCC and Clang.
- Formatting and Clippy with warnings denied for the core, read-only carrier,
  test fixtures and opt-in CPU-RM application.
- Library checks for Windows GNU, GNU LLVM and MSVC targets.
- Release builds and x86-64 EFI PE/COFF checks for the bootable images.
- Seven existing read-only QEMU cases, plus FULL/ZERO no-NVIDIA refusal.
- USB archive payloads and SHA256SUMS checked against the final release images.

The final CPU-RM binaries passed their two QEMU cases after the last rebuild.
The seven carrier logs are from the preceding run. Image and source hashes
are in [summary.json](summary.json).

See [validation.txt](validation.txt), [cpu-full-no-gpu.log](cpu-full-no-gpu.log)
and [cpu-zero-no-gpu.log](cpu-zero-no-gpu.log). The other seven logs preserve
the carrier checks from this stage.

These checks validate compilation, data and decision contracts, and emulated
discovery and refusal paths. QEMU used no NVIDIA passthrough, no real Windows loader and no host
block devices. It did not execute GPU firmware, exercise physical DMA or
write the hardware log. The new file log and reserved-memory adapter require
physical validation along with native completion and Windows driver behavior.
Use the [hardware test guide](../../UNLOCK_HARDWARE.md) for that milestone.
