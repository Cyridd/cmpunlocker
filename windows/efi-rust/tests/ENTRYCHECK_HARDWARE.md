# Early carrier-r1 diagnostic

Run only `ENTRYCHK.EFI` from this archive. It makes no GPU config/MMIO writes
and calls no `StartImage`, firmware transaction or Windows handoff. The archive
also contains the exact previously provided carrier-r1 ZERO/FULL images as
`EFI/CMP40/ZEROCHK.EFI` and `FULLCHK.EFI`. They are only loaded and unloaded
to report firmware acceptance; this does not execute their entry points.

Previous attempts replaced the images in already working Limine
entries, with empty `cmdline` and matching SHA-256. Both appeared to proceed
immediately to Windows without visible output or new logs. This diagnostic
separates a rejected image from policy/discovery/preload problems. It has
physical acceptance on that machine. The original GPU images subsequently
passed the Windows comparison with verified logs and confirmed FULL compute
unlock; see
[stage 4c](evidence/stage4c/README.md). FULL parent/Linux then passed with
driver 620.70.06, GSP OFF and no other unlocker; see
[stage 4d](evidence/stage4d/README.md).

## Setup

Extract to a temporary directory and verify `SHA256SUMS.txt`. Copy the complete
`EFI/CMP40/` directory to the same writable Linux EFI volume used for the
previous tests. In an existing working Limine entry, temporarily point `path`
at `EFI/CMP40/ENTRYCHK.EFI`, using the same working path syntax, `protocol: efi`
and empty `cmdline`. Preserve the old entry/file for later use. Alternatively,
copy ENTRYCHK's bytes to the exact existing tested filename; the two candidate
files still need to be at the fixed `EFI/CMP40/ZEROCHK.EFI` / `FULLCHK.EFI` paths
on that volume. Do not start those two candidates for this diagnostic.

The archive's `EFI/BOOT/BOOTX64.EFI` is another copy of ENTRYCHK for a separate
test USB. Preserve its existing fallback loader before copying. Keep the same
Secure Boot setup. Windows' system loader is not an installation target.

## Expected observations

The first message is:

```text
CMP40-ENTRYCHECK: ENTERED GPU_WRITES=0 CHILD_STARTS=0
```

It remains visible for five seconds before filesystem access. Then the checker
creates and verifies the next `RE000001.LOG` (or next unused number) in the
root of its own EFI volume, before reading policy or discovering the GPU.
The log records:

- `POLICY_OK parent=...` / `POLICY_REFUSED` reports load-option parsing.
- `TARGET` or `PCI_RESULT`; these use the shared read-only discovery routes.
- `LOAD_OK` / `LOAD_REFUSED` for each candidate using Firmware and RawBuffer.
  `FILE ... bytes=...` identifies bytes read through the raw FAT route.
- `WINDOWS_PREPARED` / `WINDOWS_RESULT`; a prepared Windows image is unloaded
  without starting it. `READY` means diagnostic inspection completed, even if
  a subcheck reported an error; it does not mean GPU unlock worked.

After closing/verifying the log, it displays `FINISHED ... return in 10s`,
waits ten seconds and returns to Limine. Limine may then select Windows as
its normal fallback; this diagnostic has not changed or unlocked the GPU.
If any firmware operation stalls, the last verified checkpoint identifies
the operation about to run. A failure to create the log reports
`LOG_OPEN_FAILED` on screen, followed by the ten-second delay.

Record the first and final messages and collect `RE*.LOG`. If neither the
five-second first message nor a log appears, report that result. Investigate
checker loading, entry and console visibility before drawing conclusions
about policy or GPU execution.

This diagnostic cannot validate `StartImage` or the real GPU transaction.
Successful candidate `LoadImage` only establishes that the firmware accepted
their PE images through the tested loading routes.
