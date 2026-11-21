# Stage 4b: early carrier diagnostic

`cmp40-entrycheck` is a separate physical diagnostic. It prints `ENTERED`,
holds the screen for five seconds, and opens/verifies a numbered `RE` log
before load-option parsing, read-only GPU discovery and image loading.
It records boot-option decisions, target or discovery failure, firmware-path and raw SourceBuffer
LoadImage results for the exact original ZERO/FULL candidates, and Windows
preparation. Every accepted image is unloaded; it calls no StartImage, GPU
BAR-enable/MMIO method, transaction, reset or OS start. After sealing
the log it holds `FINISHED` for ten seconds and returns its diagnostic status.
The parent may then follow its usual fallback.

The diagnostic is built with `cpu-rm` to reuse the existing output adapter;
its entry point does not call CPU-RM execution or reference firmware/payload
assets. The only shared output change adds `entrycheck` -> `RE` filenames.
The GPU transaction and preparation code stay unchanged. The originally
provided GPU images retain their stage-4a hashes; those exact files were
copied into the isolated test directory for the LoadImage checks.

## Validation

All 39 core tests, EFI Clippy for CPU-RM/adapters, formatting and the isolated
release build pass. Five retained QEMU cases pass: valid original candidate
images,
a corrupt ZERO, persisted legacy logs, LOGCHECK and parked post-mutation
failure. The two candidate-image cases verify the persisted `RE` log.
No candidate entry or Windows fixture starts.
An additional mixed invocation of `raw-windows` and `entrycheck` passes,
checking that the runner selects each case's correct parent/checker image.

The serial logs and two persisted early logs are under [qemu/](qemu/), with
hashes in [summary.json](summary.json). QEMU has no NVIDIA passthrough and does
not establish physical acceptance or reproduce the reported machine's cause.
The checker cannot diagnose a real candidate StartImage failure directly,
because it deliberately stops at LoadImage/unload.

The packaged `target/entry-r1/cmp40-rust-entrycheck-r1-usb.zip` contains the
checker at `EFI/CMP40/ENTRYCHK.EFI` and USB fallback path, exact original
`ZEROCHK.EFI` / `FULLCHK.EFI`, the standalone guide and checksums. No QEMU
fixtures are included. Archive integrity/checksums and byte-for-byte
reproduction pass. The accepted memory-r1/read-only archives and original
carrier-r1 candidate archives retain their hashes.

## Reproduction and next physical check

From `windows/efi-rust/`:

```bash
cargo fmt --all -- --check
cargo test --locked --offline -p cmp40-efi-core
cargo clippy --locked --offline --target x86_64-unknown-uefi -p cmp40-efi -p cmp40-efi-boot-fixture --features cpu-rm,adapters -- -D warnings
cargo build --locked --offline --release --target-dir target/entry-r1 --target x86_64-unknown-uefi -p cmp40-efi -p cmp40-efi-boot-fixture --features cpu-rm,adapters
```

Before QEMU, copy the original stage-4a FULL/ZERO EFI files to the isolated
`target/entry-r1/x86_64-unknown-uefi/release/` directory, replacing its freshly
built GPU pair, to reproduce checks of the supplied images. Verify their
stage-4a hashes. Then run:

```bash
python3 tests/qemu_smoke.py --image-dir target/entry-r1/x86_64-unknown-uefi/release --case entrycheck --case entrycheck-invalid --case logging --case logcheck --case failed-parent
```

Stored log excerpts omit obsolete boot-option policy diagnostics. File checksums
describe these excerpts; EFI image hashes and GPU observations remain historical.
