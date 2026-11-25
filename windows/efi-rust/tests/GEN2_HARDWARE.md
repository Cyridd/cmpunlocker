# Rust EFI Gen2 experiment

This package tests Gen2 preparation before the Windows NVIDIA driver starts.
It targets the existing 8 GiB CMP40HX TU106 profile. The accepted FULL compute
sequence runs first; Gen2 starts only after `NATIVE_READY Full`. The r2
controls reached Gen2 through the final EFI observation, but loaded Windows
remained at Gen1 with NVIDIA 621.14.

Earlier Linux CPU-RM testing reached Gen2 before driver initialization, then
returned to Gen1 when NVIDIA rewrote the GPU policy. Check the live Windows
link under load even when the EFI log reports Gen2. Capability and TLS=2 alone
do not prove that the active link is Gen2.

## Images and controls

| r1 image | r2 image | Operation after FULL compute completion |
|---|---|---|
| `EFI/CMP40/BASEFULL.EFI` | Same frozen image | Accepted ReBAR-r1 FULL; no Gen2 writes |
| `EFI/CMP40/G2PREP.EFI` | `EFI/CMP40/G2PREP2.EFI` | Masked policy and capability adoption; no TLS or retrain writes |
| `EFI/CMP40/G2TLS.EFI` | `EFI/CMP40/G2TLS2.EFI` | Same policy, then TLS=2 on GPU and immediate upstream port |
| `EFI/CMP40/G2TRAIN.EFI` | `EFI/CMP40/G2TRAIN2.EFI` | Same policy and targets, then at most one upstream retrain |
| `EFI/BOOT/BOOTX64.EFI` | Same fallback path | Copy of the package's Prepare image |

`EFI/40HX/REBAR.CFG` contains `CMP40HX_REBAR_V1=0` followed by LF. This
disables the policy-file opt-in to isolate Gen2. Keep the image volume's
policy disabled and use an empty command line initially. In particular,
`--experimental-rebar` would override the disabled file. `norebar` can force
ReBAR off. After the separate controls work, `--experimental-rebar` can test
Gen2 together with the previously accepted 8 GiB BAR1 relocation.

All three Gen2 images accept `--no-gen2` to skip the experiment while keeping
FULL compute. Existing parent-return options still apply when needed. Normal
FULL/ZERO images never select Gen2, even when built with the experiment feature.
The EFI r1/r2 packages contain no installer, Windows helper, vulnerable driver
or QEMU fixture. The separate registry trial package includes the optional
PowerShell script described below.

## r1 and r2 results

The r1 Retrain log reached physical Gen2 x16 on GPU and upstream port in
10 ms. Prepare and Targets completed with Gen2 capability and a Gen1 x16
link, as their modes permit. The Gen2 sample preceded ReBAR in each log.
All four logs show active ReBAR. With NVIDIA 621.14 and all Gen2 helpers and
ASPM disabled, I observed Gen1 under games and GPU-Z Render Test in all three
Gen2 modes. I confirmed compute unlock in Base and every Gen2 mode.

The r2 images keep the r1 policy and retrain sequence and add an observation
after ReBAR, before `HANDOFF_READY`. The observer reads policy, capabilities,
controls and status on both ends and rechecks native FULL state. It cannot
write registers or delay/retrain. It does not run after Windows starts.

The two r2 Retrain controls completed on 2026-11-25:

| Command line | ReBAR log result | Final EFI link, GPU and port | Loaded Windows link |
|---|---|---|---|
| `norebar` | Not requested; skipped | Gen2 x16 | Gen1 x16 |
| `--experimental-rebar` | Active | Gen2 x16 | Gen1 x16 |

Both logs show one retrain completing in 10 ms, `dropped=0`, matching final
policy and `HANDOFF_READY Full`. The final GPU status is `1102`, the port
status is `f102`, and both TLS fields are 2. Final MISC/CYA/CFG/PL/OVR values
are `20342d00 / 068711b3 / 80085800 / 00240036 / 00000006` in both controls.
With the Windows Gen2 helpers disabled, I observed Gen1 x16 under load in
both runs, retained compute in both, and 8 GiB BAR1 only with ReBAR enabled.
GPU-Z continued to advertise PCIe 2.0 x16 as the maximum.

These controls place the loss after the final EFI sample and show that it
also occurs without ReBAR. They do not identify the component that changes
the link or prove that the policy values remain unchanged in Windows.
Repeating the same r2 controls is unnecessary unless the driver, firmware,
boot path or another relevant input changes. Base, Prepare and Targets remain
available as additional controls; use the r2 names if repeating the full
four-mode sequence below.

Look for these records before `HANDOFF_READY Full`:

```text
CMP40-GEN2: FINAL_BEGIN phase=after-rebar-before-handoff OBSERVER_WRITES=0
GEN2_FINAL_LINK GPU ... speed=2 width=16
GEN2_FINAL_LINK PORT ... speed=2 width=16
GEN2_FINAL_POLICY VALUES=... matches=true
CMP40-GEN2: FINAL_RESULT Ok(Gen2) OBSERVER_WRITES=0 OS_RETENTION=unmeasured
```

If the earlier retrain reports Gen2 but the final observer reports Gen1,
the loss occurred before this final EFI sample. If both report Gen2 while
loaded Windows remains Gen1, the loss occurred after the sample, during the
remaining handoff or OS initialization. This narrows the boundary; it does
not identify the writer of the changed GPU policy. `matches=false` records
policy changes without repairing them. An unhealthy link, unreadable state
or failed native FULL check still takes the existing failure/park path.

The r2 package is a diagnostic. It has no demonstrated fix for Windows Gen2
retention. The [alternative fork's helper](https://github.com/BardKing-CN/CMP40HX-Unlock-OnlyEFI/blob/f12f8bb92abb0f0a36da08bd1c9a8bcd858687c7/CMP40HX-Unlock-OnlyEFI-v0.1.1-GitHub-Repo/source/windows/CMP40HXGen2_prod.c)
waits for the post-NVIDIA state before restoring two policy registers.
Its source was inspected locally on 2026-11-25. Early EFI preparation alone
was insufficient in the fork's reported validation.

## Optional stock-driver registry trial

The `gen2-registry-r1` package reuses the frozen r2 EFI binaries and includes
`WINDOWS/gen2_registry.ps1`. Its fallback is Retrain (`G2TRAIN2.EFI`); the
original r1/r2 packages keep Prepare as their fallback. This experiment uses
NVIDIA 621.14 and requires no third-party kernel driver or scheduled task.
The tested settings did not preserve Gen2 in Windows with NVIDIA 621.14.
Registry readback confirms stored values, not that NVIDIA consumed them or
that Gen2 survived initialization.

On 2026-11-25, I tested `Policy` and `PolicyPlatform`, then manually tested
`RMPcieLinkSpeed=0x1` with the other `PolicyPlatform` values. Gen2 did not
appear under load in any tested scenario, with either `norebar` or
`--experimental-rebar`. I observed equivalent EFI logs with no errors; those
new logs were not supplied for independent inspection. These are negative
Windows results for the tested settings, not evidence that every undocumented
RM setting is ineffective. No working registry configuration has been found.

The commands below remain a reproduction and restoration procedure. Repeating
these settings with the same inputs is unnecessary. The next useful evidence
is live GPU policy after NVIDIA initialization, to distinguish ignored
preferences from later policy changes. The current EFI observer ends before
Windows starts and cannot provide that sample. There is no demonstrated
EFI-only retention fix.

The script defaults to read-only `Inspect`. `Apply` and `Restore` require
64-bit Windows PowerShell in an administrator terminal. It selects exactly
one present CMP40HX by PCI vendor/device ID and follows its Enum `Driver`
value to the display software key. It refuses ambiguous devices, a service
other than `nvlddmkm`, a different driver version or non-DWORD types for the
values it would change. It does not edit the global NVIDIA service key,
GSP settings, tasks, startup entries or EFI files, and never restarts devices
or Windows.

| Trial | DWORD values written to the CMP40HX software key |
|---|---|
| `Policy` | `RMPcieLinkSpeed=9`, `RMNoGen2=0`, `RmDisableGen2LinkRetraining=0` |
| `PolicyPlatform` | The same values, plus `RmForceEnableGen2=1` |

`RMPcieLinkSpeed` is a bitfield: 9 combines the Gen2 enable field with the
Gen3 disable field. It is not a numeric target speed. Static inspection of
stock `nvlddmkm.sys` 621.14 on 2026-11-25 found the bitfield reader and the
`RmForceEnableGen2` platform flag. That flag can affect shared RM platform
policy; its name does not establish an unconditional Gen2 unlock. The driver
also has a policy branch that clears MISC bit 13 and writes CFG bits 18:19
from the negotiated link speed. These trials might still return to Gen1.
The inspected SYS SHA-256 is
`447c10f368d19db5f9dae9cceb690918d761f0d361d98017b7a68a54d99c6ba5`.

`Policy` preserves any existing `RmForceEnableGen2` value. Record it with
Inspect; if it is already 1, `PolicyPlatform` does not isolate a new platform
flag and should not be treated as a separate comparison.

Keep the Gen2 autostart sources disabled. In the extracted archive directory,
run the first trial from an administrator PowerShell terminal:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\WINDOWS\gen2_registry.ps1 -Action Inspect
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\WINDOWS\gen2_registry.ps1 -Action Apply -Trial Policy -BackupPath .\POLICY.json
```

The execution-policy option applies only to that process. Before any registry
write, the script saves and reads back an exclusive backup of the original
DWORD values and their absence. An existing backup is never overwritten.
Keep each backup private and retain it until the original state is restored.
After `Apply` succeeds, fully power off and cold-boot `G2TRAIN2.EFI` with
`norebar`. Check the loaded link using the same GPU-Z workload, compute and
BAR1 checks as the completed r2 controls, then save the new EFI log.

Restore the first trial before applying the second:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\WINDOWS\gen2_registry.ps1 -Action Restore -BackupPath .\POLICY.json
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\WINDOWS\gen2_registry.ps1 -Action Apply -Trial PolicyPlatform -BackupPath .\PLATFORM.json
```

Fully power off and cold-boot the same image with `norebar`, then repeat the
loaded checks. If either trial preserves Gen2, test that successful trial
with `--experimental-rebar` in a separate cold boot. Restore after testing:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\WINDOWS\gen2_registry.ps1 -Action Restore -BackupPath .\PLATFORM.json
```

If only Policy succeeded and the second trial was skipped, use `POLICY.json`
for restoration. Restore removes only values originally absent and restores
existing DWORDs exactly. It refuses values changed outside the trial instead
of overwriting them. A failed apply attempts restoration and keeps the backup;
an incomplete rollback requires resolving the reported value before continuing.
Registry operations cannot atomically exclude concurrent writers, so keep
other tuning tools stopped. After restoration, cold-boot to restore the driver
policy as well. If initialization fails or the screen goes black, use the
accepted boot path or Windows Safe Mode to run Restore before another trial.

The manual `RMPcieLinkSpeed=1` variant is outside the script's recorded plan
of 9. If that manual value remains and differs from the saved original,
Restore will refuse it. After checking
the saved backup and confirming that 1 came from this manual trial, return
only `RMPcieLinkSpeed` to the recorded trial value 9 in the same CMP40HX
software key, then run Restore with the corresponding backup. It will restore
the original value or absence. Other unexpected changes still require review;
the restoration guard is unchanged.

## Disable every Windows Gen2 autostart source first

An existing helper would confound the test: it could retrain the link after
EFI, or overwrite an otherwise useful observation. These steps are reversible
and are to be performed locally in Windows. Keep the installed components
and your original settings available for restoration.

1. In Task Scheduler, record the existing state of `40HX PCIe Gen2 Bring-up`
   and `40HXGen2Retry`, if present. Disable both and end any running instance.
   The primary task can run a continuous guard. Do not run the installer
   during the experiment; it can recreate the task and Run entry.
2. Check the `40HXGen2` value under
   `HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Run`.
   The current installer writes this HKCU entry. Also check the same value
   under HKLM for leftovers from other versions or manual setup. In Registry
   Editor, save the value's exact name, type and data locally, then remove
   only that named value from each Run key where it exists. Keep the backup
   private; it can contain an executable path. Restore only those saved
   values after testing if the Windows helper is still needed.
3. Check other accounts and any custom startup entry you configured to run
   `40HXInstaller.exe -gen2`. Ensure no Gen2 helper remains running. Do not
   start WinRing0, ThrottleStop or a manual Gen2 helper for these tests.
4. Use a full shutdown with Fast Startup disabled, then physically power off
   before each trial. Disabling future startup entries alone does not undo
   policy already written in the current session.

Task state and the two Run values can be inspected with these commands:

```powershell
schtasks /Query /TN "40HX PCIe Gen2 Bring-up" /V /FO LIST
schtasks /Query /TN "40HXGen2Retry" /V /FO LIST
reg query HKCU\Software\Microsoft\Windows\CurrentVersion\Run /v 40HXGen2
reg query HKLM\Software\Microsoft\Windows\CurrentVersion\Run /v 40HXGen2
```

A missing task or value means that particular channel is absent. After the
cold boot, confirm both tasks stayed disabled and the helper did not run.
Do not use the helper's cached status as evidence for the EFI result.

## Cold-boot test order

Use a separate writable FAT32 USB and preserve any existing files before
extracting the archive. Verify `SHA256SUMS.txt`. Keep the accepted boot path
available for recovery and use the same Secure Boot setup as the accepted
unsigned EFI tests. Do not replace the system Windows loader or install
these images on the system ESP.

Select each named image through a separate bootloader entry or EFI shell.
For firmware fallback testing, copy the selected image to
`EFI/BOOT/BOOTX64.EFI` after backing up that file. The default fallback runs
Prepare, so select Base explicitly for the first control. Use the same
handoff path for every trial and avoid launching a second unlock EFI afterward.

1. **Base:** cold-boot `BASEFULL.EFI`. Confirm normal NVIDIA initialization,
   FULL compute unlock and the live PCIe link under a GPU workload with all
   Windows Gen2 autostart sources disabled.
2. **Prepare:** cold-boot `G2PREP.EFI`. Expect `Ok(Prepared)` without any PCI
   control writes. Record whether Gen2 capability appears and what speed
   Windows actually uses under the same workload.
3. **Targets:** cold-boot `G2TLS.EFI`. Expect `Ok(TargetsSeeded)` with both
   TLS fields set to 2. An EFI Gen1 link is possible because this mode does
   not request retraining. Check the Windows result separately.
4. **Retrain:** cold-boot `G2TRAIN.EFI`. `Ok(Gen2)` means both ends reported
   Gen2 at their original width in EFI. `Ok(Gen1Timeout)` means one request
   and a two-second wait ended on a healthy Gen1 link; TLS=2 is retained for
   OS initialization. It is not a Gen2 success verdict. A link already faster
   than Gen2 is preserved and reported as `Ok(AlreadyFast)`.

For each trial, copy the next numbered `RF*.LOG` from the image volume and
record the selected image hash, driver version, live speed and width in GPU-Z
under load, FULL compute result and BAR1 size from `nvidia-smi -q`. Capture
the link again after several minutes of load. The initial isolated runs keep
the existing BAR assignment; compare it with Base. An idle downshift is
insufficient evidence of a failed Gen2 policy, so use the same loaded check.

The Gen2 log records before/after live capability, control and status fields,
policy values, the selected port, the result, requested retrains and elapsed
wait. `H` trace records are true 16-bit config writes; `E` records their
backend result. Both the CPU-RM and Gen2 traces should report `dropped=0`.
Successful handoff reaches `HANDOFF_READY Full` after the Gen2 result. A log
ending at `NATIVE_READY` or `CMP40-GEN2: BEGIN` does not establish completion.

## Scope and failure handling

The first experiment uses masked MISC/CYA/CFG/PL writes and writes OVR=6
last only when live Gen2 capability is absent. XP3G policy extensions are
excluded. The immediate port must be a root/downstream port with a GPU-only
leaf bus. Both ends must advertise Gen2 before target writes. True WORD
stores preserve the adjacent write-one-to-clear status fields.

There is no Link Disable, FLR, reset, PnP restart or retrain retry. Invalid
read-only topology/capability checks skip Gen2. Errors after policy mutation
use the existing terminal failure path, attempting only restoration of owned
TLS fields when they were programmed. Partial MMIO policy is retained for
diagnosis. On `FAILURE_PARKED`, a hang or black screen, physically power off
and collect the log through the accepted recovery path. Do not retry in the
same powered session or advance to another mode while the previous one fails.

EFI success does not yet justify removing the installed Windows mechanism.
The experiment can replace it only if loaded Windows operation preserves Gen2
and compute with every post-boot Gen2 helper disabled.
