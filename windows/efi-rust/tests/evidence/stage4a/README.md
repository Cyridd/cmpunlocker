# Stage 4a: carrier-r1 raw loading and boot options

This stage follows `965bf6d` and the frozen accepted memory-r1 GPU runtime
`e1422c1`. Host and emulated checks pass. Reported physical acceptance
covers the no-CPU-RM carrier: Windows/NVIDIA, return to Limine and
subsequent Linux boot. The combined ZERO/FULL Windows GPU checks are now
accepted in [stage 4c](../stage4c/README.md); FULL parent/Linux is accepted
in [stage 4d](../stage4d/README.md) for driver 620.70.06, proprietary, GSP OFF.
See [hardware-report.md](hardware-report.md). Source and image hashes are
recorded in [summary.json](summary.json). The previous FULL/ZERO binaries and
physical evidence remain unchanged.

## Implementation

`prepare_and_execute` is byte-identical to the accepted memory-r1 function.
The complete transaction, metadata, radix, allocation, DMA ownership, asset
definitions and pinned preparation script are unchanged from `e1422c1`.
No new firmware/payload is introduced. Changed OS loading still needs physical
comparison; source equivalence is not driver/DMA acceptance. Logging continues
through SimpleFileSystem outside `Transaction::run`; this port does not provide
raw filesystem writes or change diagnostic trace retention.

## Validation and artifacts

All 39 core tests pass, including eleven new sparse-disk checks. GCC/Clang C
comparisons, both asset tests, core and default/CPU-RM EFI Clippy, formatting,
Windows GNU core compilation and isolated release builds pass.

The serial logs are under [qemu/](qemu/) and [qemu-readonly/](qemu-readonly/);
the reports and hashes are in `summary.json`. QEMU has no NVIDIA passthrough
and does not enter a real GPU transaction. The fixture images are test-only.

The provided `target/carrier-r1/cmp40-rust-carrier-r1-usb.zip` contains only
the no-CPU-RM carrier (at `EFI/CMP40/CARRIER1.EFI` and USB fallback path),
the [physical guide](../../CARRIER_HARDWARE.md) and checksums. The accepted
read-only archive is frozen unchanged under `target/baseline-carrier-fe02bf7/`.
The QEMU-tested CPU-RM builds under
`target/carrier-r1/x86_64-unknown-uefi/release/` are now packaged separately as
`target/carrier-r1/cmp40-rust-cpu-rm-{zero,full}-usb.zip`, with the standalone
[GPU comparison guide](../../CARRIER_GPU_HARDWARE.md) and checksums. The EFI
bytes match the recorded QEMU hashes; no rebuild was needed. These are not
shipped installer replacements. Their Windows path now has physical acceptance,
and the original pair is frozen under `target/baseline-carrier-gpu-fe02bf7/`.
The earlier GPU baseline remains under `target/baseline-memory-e1422c1/`.

All recorded source/image hashes and the 25 + 4 saved QEMU serial-log hashes
were rechecked. Both GPU archives pass EFI header and
content/checksum checks and reproduce byte for byte in a temporary directory.
The packager's default three-image behavior also passes with the default guide.
Accepted memory-r1 and read-only carrier archives retain their original hashes.
No runtime source changed, and the earlier firmware tests were not rerun for
this documentation/packaging change.

## Next validation and logging

Read-only Windows preload, `--return-to-limine` and subsequent Linux selection
pass according to the physical report. ZERO/FULL Windows preload also passes
with verified raw logs and reported FULL compute unlock. FULL-to-Limine/
Linux compute also passes for driver 620.70.06, proprietary, GSP OFF, without
another unlocker; see [stage 4d](../stage4d/README.md). The
[GPU guide](../../CARRIER_GPU_HARDWARE.md) remains for future comparisons.

The [logging proposal](../../../LOGGING_POLICY.md) limits log storage on small
ESPs: compact normal-success summaries, failure/opt-in traces and a
proposed 8 MiB retention budget with up to four traces, subject to that byte
limit. Old logs stay intact now. Bounded logging and Go checker/uninstaller
support are required before making Rust the installer default; Go is retained.

## Reproduction

From `windows/efi-rust/`:

```bash
cargo fmt --all -- --check
cargo test --locked --offline -p cmp40-efi-core
CC=clang cargo test --locked --offline -p cmp40-efi-core --test c_parity --test transaction
python3 tests/test_assets.py
cargo clippy --locked --offline -p cmp40-efi-core --all-targets -- -D warnings
cargo clippy --locked --offline --target x86_64-unknown-uefi -p cmp40-efi -p cmp40-efi-boot-fixture -- -D warnings
cargo clippy --locked --offline --target x86_64-unknown-uefi -p cmp40-efi -p cmp40-efi-boot-fixture --features cpu-rm,adapters -- -D warnings
cargo check --locked --offline -p cmp40-efi-core --lib --target x86_64-pc-windows-gnu
cargo build --locked --offline --release --target-dir target/carrier-r1 --target x86_64-unknown-uefi -p cmp40-efi -p cmp40-efi-boot-fixture --features cpu-rm,adapters
python3 tests/qemu_smoke.py --cpu-rm --adapters --raw --image-dir target/carrier-r1/x86_64-unknown-uefi/release
cargo build --locked --offline --release --target-dir target/carrier-r1/readonly --target x86_64-unknown-uefi -p cmp40-efi -p cmp40-efi-boot-fixture
python3 tests/qemu_smoke.py --image-dir target/carrier-r1/readonly/x86_64-unknown-uefi/release --case windows --case unique-windows --case raw-windows --case raw-parent
python3 tests/package_carrier.py
python3 tests/package_cpu_rm.py --image-dir target/carrier-r1/x86_64-unknown-uefi/release --output-dir target/carrier-r1 --guide tests/CARRIER_GPU_HARDWARE.md --control zero --control full
```

Stored log excerpts omit obsolete boot-option policy diagnostics. File checksums
describe these excerpts; EFI image hashes and GPU observations remain historical.
