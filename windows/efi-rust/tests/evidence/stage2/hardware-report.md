# Physical read-only boot report

Reported on 2026-11-17, following the read-only carrier commit `0a82aa4`.
The observations have no raw serial capture or independently repeated
hardware test.

- CMP40HX discovered at `0000:10:00.0`, identity `10de:1f0b`.
- `PCI_HANDLES=34`, `TARGETS=1`, zero reported read errors.
- Class transcribed as `0300200`; no raw log was supplied to
  resolve the formatting of this field.
- The Rust EFI ran from disk 3's Linux EFI partition. Windows Boot Manager
  existed only on disk 1's EFI partition; disk 2 contained another Windows
  NTFS data partition. Windows started successfully from disk 1.

This accepts physical PCI discovery and cross-volume Windows handoff on the
reported machine. It does not test GPU firmware execution, compute unlock,
FULL/ZERO controls, or the other boot-target ambiguity/failure cases.
The prepared image hash is recorded in [summary.json](summary.json).
A separate checksum of the image used on the machine was not supplied.
