# Physical diagnostics for the Rust compute unlock EFI

These experimental images target the existing CMP40HX TU106 `10de:1f0b`,
8 GiB, and the project's current board-specific firmware. The completion-gated
FULL/ZERO images now have [physical acceptance](evidence/stage3e/README.md) on
the tested board, including FULL compute unlock after OS handoff. The C EFI
remains the shipped installer image. An earlier ZERO run through Limine failed
at Booter; the
[follow-up physical trace](evidence/stage3d/README.md) records mailbox `0x91`,
WPR2 up and GSP still running before Booter preparation. The original log
was subsequently found, confirming that logging worked.

For future changed variants, use the accepted FULL/ZERO pair as the baseline.
Current builds write `EFI/40HX/LOGS/Lxxxxxxx.LOG` with the control in the header
and the `CMPUNLOCKER:` prefix. The root-level RF/RZ/RL filenames below belong
to the historical test images. Use the [logging guide](../LOGGING_POLICY.md) for current builds.

The accepted memory-r1 variant removes the separate zero-image allocation and
constructs its contents in the radix leaves. It saves 29,339,648 reserved bytes
(27.98 MiB); the GPU image, 960 KiB heap and firmware inputs keep their accepted
values. Successful cold FULL/ZERO runs and FULL compute unlock are confirmed; [stage 3f](evidence/stage3f/README.md) records the verified FULL log and
the ZERO report (its raw log was not copied). The provided pair is frozen in
`target/baseline-memory-e1422c1/`. Its logs identify the variant with
`MEMORY_LAYOUT zero_image=in_radix saved_reserved_bytes=29339648` and include
the image/table/radix sizes. `BUFFERS` no longer has a `dummy` allocation.
Radix, metadata and deferred FWSEC DMA addresses change; exact placement still
depends on which reserved-page requests succeed. Preserve the accepted images
and add a separate Limine EFI entry for this candidate, using the same path
setup and empty `cmdline` as the successful runs. Check the archive hash, then
run ZERO and FULL with a full power-off between attempts. Collect both new
logs, Windows driver status and the same FULL compute check used previously.

`FWSEC_COMPLETION` must show
GSP HALT (`CPUCTL` bit 4), no scratch0e FRTS error (`scratch0e >> 16 == 0`),
and WPR2 `01ffee00:01ffe000` before Booter preparation. A completion timeout
or error stops at `FwsecCompletion` and never prepares/starts Booter. Only
advance to FULL after ZERO reaches native completion and Windows initializes.

If log visibility is uncertain, run the LOGCHECK archive from the same Limine/firmware path used for ZERO.
It writes and rereads `RL000001.LOG` on the image's volume without PCI discovery,
MMIO or firmware execution. `LOG_CHECK_READY GPU_WRITES=0` confirms the firmware
readback check. Verify the file is also visible from the OS before a new ZERO
attempt. If it is missing, record `LOG_VOLUME`/`LOG_PARTITION` and stop at this
filesystem diagnostic until the target volume is identified.

Use the corrected ZERO only after log visibility is confirmed. It reports
`BOOTER_FAILURE check=... reg=... observed=... expected=...` if Booter fails.
The filename is `RZ000001.LOG`, incrementing without overwriting existing logs.
After failure it remains at `FAILURE_PARKED`; fully power off before booting
Linux to collect the log. Do not advance to FULL while ZERO still fails.

1. Use a separate writable FAT32 USB. Back up its existing `EFI/BOOT/BOOTX64.EFI`
   before replacing it. Extract the selected LOGCHECK/ZERO/FULL archive to the USB root.
   Each archive contains `EFI/BOOT/BOOTX64.EFI`, `SHA256SUMS.txt` and this guide.
   Verify the image hash before boot. Keep the C unlocker available for recovery.
2. Start from a full power-off and use the firmware's one-time USB boot menu.
   Use the same Secure Boot configuration as the previous unsigned diagnostic
   test, or sign the image with an already trusted certificate.
3. Run ZERO first as the control. It executes the same firmware sequence and
   expects selectors `00000000:00000000`. Copy the new
   `RZ000001.LOG` (or the next numbered log) from the image volume after Windows starts. Record whether
   the Windows NVIDIA driver initializes normally.
4. Fully power off before FULL. Boot the FULL archive on the same USB path.
   It expects selectors `88888888:00000008`. Copy the new
   `RF000001.LOG` (or the next numbered log), record driver initialization, and repeat the same
   compute check used for the working C version with the same driver/settings.

In both runs the log must reach `TRANSACTION Ready`, `NATIVE_STATE` and
`NATIVE_READY`. Native completion requires SEC2 HALT, mailbox 0 zero, GSP
halted/idle, stock FEAT PLM `ffffff8f`, and WPR2 HI/LO
`00000000:1ffffe00`. The trace must also show FWSEC establishing WPR2
`01ffee00:01ffe000` before Booter. `dropped=0` confirms the retained trace is
complete apart from the explicitly omitted firmware data ports.

An EFI success status alone does not establish working compute unlock. Compare
FULL with ZERO and the C reference in Windows. Record the image SHA-256,
driver version, results and logs together. The logs contain hashes and control
registers, not firmware data port contents.

On `ABORT`, a hang or a black screen, fully power off and restore the normal
boot path before trying again. No same-session retry or firmware fallback is
allowed after GPU mutation: `FAILURE_PARKED` prevents returning to Limine or the
firmware's next boot target. A hard stall may leave the log at `TRANSACTION_BEGIN` because the
transaction trace is kept in RAM until completion. Do not replace the system
Windows loader or use these images for routine boots before physical validation.

The default handoff preloads Windows from the image's volume or one uniquely
identified other volume. If there are multiple Windows candidates on other
volumes, it refuses to choose. The existing `--return-to-bootloader`,
`--return-to-grub` and `--return-to-limine` aliases are supported for parent
loaders. FLR and extra GSP/SEC2 cleanup are outside these test images.

Carrier-r1 changes OS preparation to BlockIo/FAT + SourceBuffer. Its [read-only carrier checks](CARRIER_HARDWARE.md)
have reported physical acceptance for Windows/NVIDIA, Limine return and subsequent
Linux boot. The carrier-r1 GPU pair also has
[Windows acceptance](evidence/stage4c/README.md), with both raw logs and
confirmation of FULL compute unlock. FULL parent/Linux handoff also has
[acceptance](evidence/stage4d/README.md) with proprietary driver 620.70.06,
GSP OFF and no independent Linux unlocker. Follow the standalone
[carrier-r1 GPU comparison guide](CARRIER_GPU_HARDWARE.md). Its Windows-preload log must include
`HANDOFF_PREPARED parent=false source=Some(RawBuffer)`; parent return records
`parent=true source=None`. The frozen memory-r1 logs do not contain this source field and
remain the earlier accepted GPU baseline. Production log retention is proposed
in [LOGGING_POLICY.md](../LOGGING_POLICY.md); this diagnostic stage preserves logs.
