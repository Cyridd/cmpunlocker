# First Rust ZERO hardware failure: manual report

Baseline image/source: the stage-3b ZERO release from `3cda611`.
The image SHA-256 was not reconfirmed during this run.

The reported run began with a cold boot and a dedicated Limine EFI entry:
`protocol: efi`, an existing Linux EFI partition containing ZERO, and an empty
`cmdline`. An initial check of that EFI partition found no log.
The exact Limine `path` was not supplied; no EFI volume handle was recorded.

These screen lines were transcribed from a phone video:

```text
TRANSACTION_BEGIN
TRANSACTION Failed failed_at=Some(Booter) result=Err(DEVICE_ERROR)
ABORT DEVICE_ERROR: no retry; power off before another attempt
```

The reported wait after transaction entry was approximately 5 to 6 seconds.
Windows then started immediately, stayed at a black screen for about 60
seconds and reported NVIDIA Code 43 / driver initialization failure.

Evidence limits: the video and raw log/register values are unavailable. The
phase establishes that the Rust flow reached its Booter stage and passed the
preceding FWSEC gate; it does not identify which Booter check failed. Timing
is compatible with the post-start sparse wait, but is not execution proof.
The report does not establish permanent damage or the cause of the missing log.

Confirmed implementation gap: returning an EFI error transfers control to the
parent/firmware, which may continue with another boot target despite an
incomplete GPU transaction. The corrected error tail parks after GPU mutation.
The specific parent/firmware selection of Windows is not reconstructed from
this report. Firmware parameters and payload bytes remain unchanged pending
an exact failure record.

The original log was subsequently found; it had been overlooked. The apparent missing-log condition was not a firmware
failure. A new ZERO trace is archived in [stage 3d](../stage3d/README.md).
