# Physical checks for Rust carrier-r1

Current builds log `CMPUNLOCKER-READONLY:`. The examples from the earlier images use `CMP40-READONLY:`.

The `CARRIER1.EFI` image is the default build without the `unlock` feature.
It inventories PCI devices and discovers the CMP40HX through the same
RootBridgeIo Standard/Compact and CF8 paths as FULL/ZERO. It does not enable
BARs or run a GPU transaction. Source-buffer loading and boot-option parsing
have host and QEMU coverage. The [physical report](evidence/stage4a/hardware-report.md)
confirms Windows/NVIDIA boot, return to Limine and subsequent Linux boot on the
tested configuration. Use this procedure to repeat the checks.

Keep the accepted FULL/ZERO files in their existing locations. Extract this
archive into a temporary directory, copy `EFI/CMP40/CARRIER1.EFI` to a new
location on the same Linux EFI volume, and create a separate Limine entry using
the same `protocol: efi` and path syntax as the successful tests. The archive
also contains `EFI/BOOT/BOOTX64.EFI` for a separate test USB; both names contain
the same carrier. `SHA256SUMS.txt` identifies each image. Preserve an existing
USB fallback loader before using that path. The system Windows loader is not
an installation target for this diagnostic.

## 1. Windows preload

Start from full power-off and use the carrier entry with an empty `cmdline`.
Its own volume is preferred for Windows. If that volume has no Windows loader,
the application accepts one uniquely identified loader on another volume;
multiple candidates, a corrupt FAT/GPT or rejected image stop the handoff.

The expected console observations include:

```text
CMP40-READONLY: TARGET_ROUTE 0000:10:00.0 Standard GPU_WRITES=0
CMP40: RAW_FILE_READ ... FAT=Fat32 block_bytes=512 SFS_CALLS=0
CMP40: SOURCE_VOLUME_VERIFIED
CMP40: WINDOWS_FROM_BUFFER ... SFS_CALLS=0
CMP40-READONLY: WINDOWS_PREPARED unique-volume
CMP40-READONLY: START_WINDOWS GPU_WRITES=0
```

The address/encoding, FAT kind, sector size and `own-volume`/`unique-volume`
depend on the configuration. Verify that Windows starts and NVIDIA initializes
normally. The carrier prints to the console and does not create an `RF`/`RZ`
log; report the visible markers or use the same phone-video approach as before.
Compute unlock is not a success criterion for this read-only image.

## 2. Return to Limine and start Linux

Fully power off, then run the same carrier with:

```text
cmdline: --return-to-limine
```

It should report `RETURN_PARENT GPU_WRITES=0` and return control to Limine.
Record whether its menu returns, Linux can be selected and starts, and whether
any firmware POST/reset or unexpected Windows fallback occurs. This checks
physical parent return without a GPU transaction; it is separate from the
already accepted Windows preload path.

## 3. Later GPU acceptance

After the two carrier checks, the carrier-r1 FULL/ZERO cold Windows comparison
also passed on the tested configuration; [stage 4c](evidence/stage4c/README.md)
records both raw GPU logs and confirmed FULL compute unlock.
The separately packaged pair now has a standalone
[GPU comparison guide](CARRIER_GPU_HARDWARE.md).
The accepted carrier-r1 pair is frozen at `target/baseline-carrier-gpu-fe02bf7/`.
Its Windows logs identify
`HANDOFF_PREPARED parent=false source=Some(RawBuffer)`. Use the
[compute unlock procedure](UNLOCK_HARDWARE.md) and compare the normal native state and compute
behavior. FWSEC/Booter, image geometry and DMA allocation code are unchanged.

Physical FULL-to-Limine-to-Linux also passes on the tested configuration
with proprietary driver 620.70.06, GSP OFF and no independent Linux unlocker;
[stage 4d](evidence/stage4d/README.md) records the raw GPU log and reported OS behavior.
For future comparisons, use a separately identified image
with `--return-to-limine`. Require the `parent=true` checkpoint followed
by `NATIVE_READY Full`. Record the Linux driver/version, any driver-side unlock
or reset behavior, and a compute result. A Linux driver that independently
unlocks the GPU does not by itself prove that EFI unlock survived handoff.
Use full power-off between GPU attempts; the accepted parked-failure policy
still applies after any possible GPU mutation.

## Logging

The accepted GPU builds retain their existing diagnostic logging. Production
log size/retention is a later milestone documented in
[LOGGING_POLICY.md](../LOGGING_POLICY.md); no old logs are removed by this stage.
