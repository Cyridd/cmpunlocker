# Physical comparison: carrier-r1 ZERO/FULL

These packages contain the CPU-RM images built at `fe02bf7`, with the new
BlockIo/FAT Windows preload path. The no-CPU-RM carrier has
reported physical acceptance on the project's CMP40HX TU106 `10de:1f0b`, 8 GiB:
Windows/NVIDIA starts, return to Limine works, and Linux then starts.
The combined carrier-r1 GPU Windows path has verified FULL/ZERO logs and
confirmed post-handoff behavior; see [stage 4c](evidence/stage4c/README.md). The procedure remains
available for repetition. FULL parent/Linux acceptance is recorded in
[stage 4d](evidence/stage4d/README.md) for proprietary driver 620.70.06 with
GSP OFF and no independent Linux unlocker.

The GPU transaction, FWSEC completion gate, board-specific inputs, payloads,
radix construction and DMA allocation code match the accepted memory-r1
runtime. Preparing Windows through the changed carrier can affect the firmware
environment and memory placement, so it requires a physical comparison.
Keep the frozen memory-r1 pair (`target/baseline-memory-e1422c1/`) and the
working C EFI available for comparison/recovery. The accepted carrier-r1 GPU
pair is frozen separately at `target/baseline-carrier-gpu-fe02bf7/`.

## Images and setup

| Control | EFI SHA-256 |
| --- | --- |
| ZERO | `fab513e7116f86c2de333f7b6050e5083d5e2671347b981ca1935416a0362978` |
| FULL | `8cbc06100323032dd75654f70103c925ae886a863876cdd2df0f595b29176735` |

Each archive contains one image at `EFI/BOOT/BOOTX64.EFI`, this standalone
guide as `TEST_GUIDE.md`, and `SHA256SUMS.txt`. Extract the ZERO and FULL
archives into separate temporary directories and verify their checksums.
Copy the two EFI files to new names on the same writable Linux EFI volume
used for the accepted carrier, for example `EFI/CMP40/ZEROC1.EFI` and
`EFI/CMP40/FULLC1.EFI`. Add separate Limine entries with `protocol: efi` and
the same working `path` syntax. The different filenames are only suggested
destinations; they are not extra files inside the archives.

Use an empty `cmdline` for both controls.
Use the same Secure Boot setup as prior tests.
For a separate test USB, preserve its existing fallback loader before copying
the archive's `EFI/BOOT/BOOTX64.EFI` there.

## 1. Cold ZERO to Windows

Fully power off before the attempt. Start the new ZERO entry and confirm
Windows boots and NVIDIA initializes normally without Code 43. Copy the next
`RZ*.LOG` from the root of the image's EFI volume. The log must contain:

```text
HANDOFF_PREPARED parent=false source=Some(RawBuffer)
MEMORY_LAYOUT zero_image=in_radix saved_reserved_bytes=29339648
FWSEC_COMPLETION
TRANSACTION Ready
NATIVE_READY Zero
```

These are marker fragments; some lines have a `CMP40-CPU-RM:` prefix
or additional fields. `FWSEC_COMPLETION` must show GSP HALT
(`CPUCTL` bit 4), zero scratch0e FRTS error in the high half and WPR2
`01ffee00:01ffe000`. Native completion expects ZERO selectors
`00000000:00000000`, Booter mailbox0 zero, halted SEC2 and idle/halted GSP,
stock FEAT PLM `ffffff8f`, and WPR2 HI/LO `00000000:1ffffe00`.
The trace must report `dropped=0`; firmware data ports remain omitted/counted.
Advance to FULL only after ZERO reaches native completion and Windows/NVIDIA
works normally. If the handoff source differs, preserve the log so the actual
loading route can be assessed.

## 2. Cold FULL to Windows

Fully power off again, then start the new FULL entry with the same handoff
settings. Collect the next `RF*.LOG` and verify the same markers, with
`NATIVE_READY Full` and selectors `88888888:00000008`. Repeat the same compute
test as for accepted memory-r1, with the same driver/settings. Record the
driver version and test result with the image checksum and both logs.
Native register checks and an EFI success status do not alone establish
post-handoff compute unlock.

## 3. FULL to Limine and Linux

This check passes on the tested configuration. Repeat it when comparing
changed variants.

After the ZERO/FULL Windows comparison passes, fully power off and run the
same FULL image with `cmdline: --return-to-limine`.
The log should identify `HANDOFF_PREPARED parent=true source=None`, then
`NATIVE_READY Full`. Confirm the Limine menu returns, select Linux and check
driver initialization and compute behavior. Collect that run's separate
`RF*.LOG`, Linux kernel/driver version and compute result.

Record any driver-side unlock service/module or reset behavior. A Linux driver
that performs its own unlock does not establish that EFI unlock survived.
The accepted read-only return/Linux result does not cover this GPU handoff.

## Failure and logging

On `ABORT`, a stall or black screen, fully power off before another attempt
or collecting the log through a normal boot. After possible GPU mutation,
failure stays at `FAILURE_PARKED`: no same-session retry, reset or fallback
to Windows/parent is permitted. A hard stall may leave only the pre-transaction
checkpoint because the control trace remains in preallocated RAM until return.

Diagnostic logs still preserve earlier numbered files and retain the full
control trace. Bounded production logging is planned before making Rust the
installer default. Use these separately named images for the comparison;
the system Windows loader is not an installation target.
