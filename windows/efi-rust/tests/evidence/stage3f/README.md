# Stage 3f: in-place zero image, memory-r1 physical acceptance

This variant follows the accepted `9f0882b` runtime and `bfcc09c` evidence
checkpoint. Successful cold runs of both memory-r1 images and FULL compute
unlock in the OS are confirmed. The supplied [RF000002.LOG](RF000002.LOG)
verifies FULL native completion; the ZERO log was not copied from the ESP, so
ZERO acceptance rests on reported success without errors. Details
are recorded in [hardware-report.json](hardware-report.json).
The provided pair is frozen in `target/baseline-memory-e1422c1/`; the previous
accepted pair remains in `target/baseline-9f0882b/`.

`RadixPlan::write_zero_image` validates the caller buffer and DMA region before
the first store, then constructs tables and zero-filled leaves without a
source image. It clears unused table entries and last-leaf padding, preserving
the caller's tail. `write_image` shares this builder and copies its source only
after validation. The EFI adapter removes the separate dummy allocation while
retaining initialized reserved-page ownership and cache barriers.

The removed allocation is 7,163 pages, 29,339,648 bytes (27.98 MiB) of system
RAM, retained across handoff by the accepted version. The radix remains
7,179 pages: 65,536 table bytes plus 29,339,648 leaf bytes. The meaningful image
stays `0x1bfadc8` bytes. The GPU WPR geometry and 960 KiB heap, pinned inputs,
FULL/ZERO payloads, completion gates and terminal failure behavior are unchanged.
DMA mapping, placement policy and FreePages retain their existing behavior.

Removing an allocation also removes its 16 MiB high-slot cursor advance. If
every initial request succeeds, the new radix base is `0x113025000`, metadata
is `0x115c30000`, and deferred FWSEC is `0x116c31000`. Firmware allocation
failures may change these placements. All 256 metadata bytes are compared with
the actual C builder using the new expected radix/payload/boot addresses as
well as the earlier low/high cases.

## Local checks

- All 28 core tests pass with GCC, including new zero-image/C parity at seven
  image sizes: partial pages, the leaf 511/512 boundary and the real image.
  Padding, unused entries and untouched tails are checked separately; invalid
  zero-image buffers/addresses refuse before stores.
- All five data/option/state comparisons pass with Clang. The host reference
  now uses `-fno-builtin`, matching `build_v70.sh`. Without it, Clang 23.1.5
  replaces the C CHAR16 length loop with Linux `wcslen` using a 32-bit wchar ABI:
  a standalone `--return-to-bootloader` is wrongly reported absent. This was
  reproduced before fixing the host compiler flags; EFI parser code is unchanged.
- Both asset tests, core/UEFI Clippy with warnings denied, formatting, Windows
  GNU core compilation and isolated UEFI release build pass.
- All 12 QEMU cases pass. Their report is recorded in [summary.json](summary.json).
  These exercise carrier boot, parent return, no-GPU FULL/ZERO, persistent logs,
  LOGCHECK and parked failure. With no GPU passthrough, QEMU never enters the
  changed radix/DMA preparation path and does not establish NVIDIA acceptance.

## Physical observations

The FULL log identifies the same target and Standard RootBridgeIo route and
records exactly the expected new radix, metadata and FWSEC DMA addresses above.
All 256 metadata bytes match the actual C builder with GCC and Clang. Relative
to the accepted FULL log, only the radix pointer field changes; the pinned
asset manifest is identical. `MEMORY_LAYOUT` records the in-place image and
29,339,648-byte reserved-memory reduction.

FWSEC observes HALT after the same additional 99 ms, zero FRTS error and the
expected WPR2 window. Booter mailbox0 is zero; FULL selectors are
`88888888:00000008`, WPR2 is down, FEAT PLM is `ffffff8f`, and GSP/SEC2 halt.
The trace retains 20,499 records with zero drops. Compared record by record
against the earlier successful FULL, exactly five records differ: two PTIMER
seed writes, the deferred FWSEC allocation address and two metadata-address
mailbox observations. Every other retained record, including delays and
barriers, is identical. Firmware data ports remain omitted from the trace.

These results accept the memory optimization on the tested configuration.
Executable hashes were not separately reconfirmed, and numeric benchmarks
were not supplied. FULL uses `parent=false`; physical parent/Linux handoff, other
boards, repeated-boot statistics and suspend/resume are not covered. The ZERO
raw log can be added later to strengthen its already reported success.

## Provided artifacts

Generated images and packages stay under ignored `target/memory-r1/`:

| Control | EFI SHA-256 |
| --- | --- |
| ZERO | `b5523e1e33b2e7d1cac31966f89455d27ce0291c467915d00ac75d6a747bb01b` |
| FULL | `ff7009e0b30114c0384221baa671388940d70596a042f612b28f742abb8f527e` |

Each `cmp40-rust-cpu-rm-{zero,full}-usb.zip` contains `EFI/BOOT/BOOTX64.EFI`,
the updated physical guide and `SHA256SUMS.txt`. Hashes inside both accepted
FULL/ZERO baseline archives were rechecked and still match the stage-3e record.

The accepted memory-r1 packages are copied unchanged to
`target/baseline-memory-e1422c1/`, with hashes recorded in `summary.json`.
Future changes should follow the [hardware procedure](../../CPU_RM_HARDWARE.md)
against these frozen images. Any failed GPU attempt parks until physical
power-off; no same-session retry is added. Next work is carrier parity and
physical parent/Linux handoff, followed by existing Go distribution integration.

## Reproduction

From `windows/efi-rust/`:

```bash
cargo fmt --all -- --check
cargo test --locked --offline -p cmp40-efi-core
CC=clang cargo test --locked --offline -p cmp40-efi-core --test c_parity
python3 tests/test_assets.py
cargo clippy --locked --offline -p cmp40-efi-core --all-targets -- -D warnings
cargo clippy --locked --offline --target x86_64-unknown-uefi -p cmp40-efi -p cmp40-efi-boot-fixture --features cpu-rm,adapters -- -D warnings
cargo check --locked --offline -p cmp40-efi-core --lib --target x86_64-pc-windows-gnu
cargo build --locked --offline --release --target-dir target/memory-r1 --target x86_64-unknown-uefi -p cmp40-efi -p cmp40-efi-boot-fixture --features cpu-rm,adapters
python3 tests/qemu_smoke.py --cpu-rm --adapters --image-dir target/memory-r1/x86_64-unknown-uefi/release
python3 tests/package_cpu_rm.py --image-dir target/memory-r1/x86_64-unknown-uefi/release --output-dir target/memory-r1
```

Stored log excerpts omit obsolete boot-option policy diagnostics. File checksums
describe these excerpts; EFI image hashes and GPU observations remain historical.
