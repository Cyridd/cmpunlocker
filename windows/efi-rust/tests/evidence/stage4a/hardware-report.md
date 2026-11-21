# Physical carrier-r1 acceptance report

Reported on 2026-11-18 on the same CMP40HX TU106 system as the accepted
memory-r1 tests. The observations have no raw serial capture.

Following `tests/CARRIER_HARDWARE.md`, a separate Limine entry started Windows
and NVIDIA normally. Running with `cmdline: --return-to-limine` returned to
Limine. Visible markers in both checks matched the guide. Linux could then
be selected and started.

This accepts the no-CPU-RM carrier's Windows preload, return to Limine and
subsequent Linux boot on that configuration. The guide requested full power-off
between checks; following the guide is reported, but exact power events were
not separately reconfirmed. Driver versions, a raw console/video capture and
an independent checksum of the executed image were not supplied.

The provided default-build `CARRIER1.EFI` has SHA-256
`10fd039eda1ba4815b4dbf8bf514e9b6003ce51c3d9cf568ff646430fc9f53be`.
The unchanged archive is frozen under
`target/baseline-carrier-fe02bf7/cmp40-rust-carrier-r1-usb.zip`; its archive and
image hashes match `summary.json`. These identify the supplied artifact,
without a separately reported checksum verification.

This image performs no GPU transaction. The combined carrier-r1 ZERO/FULL
Windows comparison and FULL-to-Limine/Linux compute handoff remain pending;
memory-r1 stays the accepted GPU baseline. Instructions for those images are
in [CARRIER_GPU_HARDWARE.md](../../CARRIER_GPU_HARDWARE.md).
