# Carrier-r1 GPU images: no visible entry

Reported on 2026-11-18 after the `76c008b` packages were provided.
Both ZERO and FULL were tested; neither showed visible application messages, both
appeared to skip immediately into Windows, and no new RF/RZ log was found on
the image's EFI volume. A phone recording did not capture application output.

The report confirms `protocol: efi`, empty `cmdline`, and replacement of EFI
files in existing, previously working Limine entries. No new entries were added.
The filenames/path matched and the deployed image SHA-256 values matched the
provided ones. Exact expanded Limine path text and the actual numeric hashes
were not supplied; checksum equality is a reported observation.

Neither GPU execution nor a specific failing early operation is established
by this report. Current CPU-RM code can return before opening its RF/RZ log
on load-option refusal or failed PCI discovery. Those messages may be
transient when Limine resumes. An image loading/entry problem also remains
possible. There is no basis yet to select one cause or claim a GPU firmware
failure, Code 43, or successful unlock.

At the time of this report, read-only carrier-r1 had confirmed Windows/NVIDIA,
Limine return and Linux boot. The changed combined GPU images were still
unaccepted; memory-r1 and read-only archives remained frozen.

Subsequently supplied `RZ000003.LOG` and `RF000004.LOG` accompany confirmation
of Windows/NVIDIA success in both modes and FULL compute unlock. Both raw logs
verify RawBuffer Windows preparation and native GPU completion. See
[stage 4c](../stage4c/README.md) for the accepted Windows baseline and frozen
original archives. GPU parent/Linux compute handoff remains pending.
