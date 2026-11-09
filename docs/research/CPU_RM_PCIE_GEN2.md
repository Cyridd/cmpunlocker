# CMP40HX PCIe Gen2 with proprietary Linux CPU-RM

On 2026-11-01 I tested a pre-driver Gen2 setup followed by the unmodified
proprietary NVIDIA 615.78.08 driver with GSP disabled. Preparing and training
the link before NVIDIA worked, but native initialization changed the GPU-side
policy and returned the link to Gen1. A normal post-driver retrain alone failed.
Reapplying the same masked policy after CPU-RM initialization reached a usable
Gen2 x16 link without Link Disable, FLR or NVIDIA unbind/bind.

A second cold boot then established that the preliminary Gen2 stage is
unnecessary on this configuration: compute unlock left stock Gen1 capability,
and the post-driver helper alone exposed and trained Gen2 x16.

## Shared Gen2 engine (0.17.0)

**Verified by source inspection:** the Gen2 update on 2026-11-08 uses one implementation in
[`gen2_core.h`](../../linux/cpu-rm/kernel/gen2_core.h) for CPU-RM and all four
open-driver patch sets. The hardware measurements elsewhere in this report
used the previous implementation.

**Verified:** I tested 0.17.0 on hardware and confirmed successful operation.

The policy follows the current
[CMP50HX source](https://github.com/xrip/cmp50hx-unlock/tree/b630a493b7877fbd4472113dcb960a93749c0a78)
and incorporates the policy-restoration and bounded-retry ideas from
[issue #68](https://github.com/xrip/cmp50hx-unlock/issues/68) and
[issue #51](https://github.com/xrip/cmp50hx-unlock/issues/51). These reports
involve X99 and TU102; they do not establish the cause of the CMP40HX failure.
This update combines selected changes; their individual effects on CMP40HX
have not been isolated.

The masked write sequence is now:

```text
MISC 0x8841c = (old | bit11 | bit13) & ~(bit12 | bit14)
VAL0 0x8e120 = 0          # only with XP3G PLM == ffffffff
OVR0 0x8e110 = 1          # only with XP3G PLM == ffffffff
CYA  0x8c2c0 = old & ~bit2
CFG  0x8c040 = (old & ~0x000c0000) | 0x00080000
PL   0x8c1c0 = (old & ~0x00060000) | 0x00040000
OVR  0x8872c = 6          # last, only if live Gen2 capability is absent
```

BOOT0, full compute selectors and every register to be written must be
readable before the first policy write. XP3G registers are written only when
their PLM is `ffffffff` and their values are valid. Other PLM values retain
the previous CMP40 subset. The optional CPU-RM `windows` policy still uses
full MISC/CYA constants. A failed write/readback stops the sequence before
retraining; partial policy remains visible for diagnosis.

Live FLAGS, LnkCap, LnkCap2 and LnkCtl2 are read through the discovered PCIe
capability offset. Linux's cached version and speed fields are not changed.
Initially live v1 permits policy application, but v2-only registers are not
accessed until the live version reaches 2. Both ends must advertise Gen2
before any TLS writes. A real v1 capability that survives adoption therefore
still stops target programming; the cached-v1 workaround does not bypass it.

A retained Gen2 capability no longer skips policy restoration. Actual speed
is checked separately. Retraining uses only the upstream port's RL bit;
CCC, ASPM, HASD/HAWD, CFG bit 22 and the other unmasked fields are preserved.
The update does not write `0x88610`, XP3G VAL3/OVR3, PL bits 16/20, or the
endpoint's RL bit. My repeated `0x88610` tests on CMP40HX, including `0x1001`,
produced no Gen2 improvement. This register is a Gen3 control.

The open adapter calls the engine after the successful stock-Booter guard,
at GSP-ready, and after `rm_init_adapter`. It uses the existing full BAR0
mapping and matching `void *, NvU32` callback declarations in both OS
interface headers. `cmp40_gen2_early=1` is the default: apply policy and seed
both TLS fields after Booter. Value `0` applies policy only; value `2` adds
one early retrain with a 200 ms poll. RM callbacks use bounded busy waits,
including 50 ms for policy settling, and never sleep or allocate memory.
A healthy early Gen1 timeout preserves TLS=2 for the later stages. It does
not mean the early retrain succeeded. GSP-ready performs policy restoration
only; the post-init stage performs ordinary retraining.

CPU-RM retains `gen2=off` by default. Its pre-driver `prepare`, `targets` and
`retrain` controls run after compute cleanup. The explicit runtime helper
still requires an idle native proprietary driver with GSP halted. The late
engine allows two retrain requests, each with a two-second observation
window. It retries only on a healthy Gen1 link with unchanged width and live
Gen2 capability, restoring policy without another `0x8872c` write first.
Both TLS fields are checked and reapplied before each retrain request.
All failures after target programming restore both saved TLS fields, except
the healthy early timeout described above. Both ends must report the same
speed and original width, with an accessible GPU and full selectors, for a
successful final result.

The open patch files embed the canonical core and adapter. Regenerate them
from the pinned, unmodified NVIDIA archive with:

```bash
python3 tools/gen2/generate.py --version 620.70.06 --source open-gpu-kernel-modules-620.70.06.tar.gz
```

Repeat for another supported version and its matching archive. `--check`
compares the generated patch without writing it. The patch checker compares
the embedded files byte for byte and checks all three callback stages and
both declarations, along with compute, ReBAR and optional profiling patches.

### Verification of the shared engine

**Verified:** the final source passed the following commands on x86-64 CachyOS,
with Python 3.14.9 and Clang/LLD 23.1.4:

```bash
make -C linux/cpu-rm offline-test
python3 -m unittest discover -s linux/cpu-rm/firmware -p 'test_*.py'
make -C linux/cpu-rm/kernel KDIR=/usr/lib/modules/7.3.3-1-cachyos/build LLVM=1
bash -n linux/cpu-rm/unlock.sh tools/check-linux-patches.sh
shellcheck -x -S warning linux/cpu-rm/unlock.sh tools/check-linux-patches.sh
```

All exited 0. The offline suite passed 44 production-code PCI/MMIO fault
cases, the actual open adapter in normal and diagnostic modes, six helper
cases, two mkinitcpio tests and the firmware/layout checks. The separate
firmware unittest run passed eight tests. Fault cases include cached v1 versus
live v2, initially live v1, failed adoption, protected XP3G, partial writes,
policy restoration without another adoption write, TLS reset between attempts,
width loss, disagreeing link ends, inaccessible devices and target restoration.

The final open builds produced `nvidia.ko` with exit 0:

| Driver | Kernel headers | PCIe variant |
|---|---|---|
| 580.173.02 | 6.12.69-2-cachyos-lts-lto | Normal |
| 610.57.04 | 6.12.69-2-cachyos-lts-lto | Normal |
| 615.78.08 | 7.3.3-1-cachyos | Normal |
| 620.70.06 | 7.3.3-1-cachyos | Normal and diagnostic |

Each source tree had compute, Gen2 and ReBAR applied. The command for each
row used its source directory as `tree` and `/usr/lib/modules/<kernel>/build`
as `kdir`:

```bash
make -C "$tree" -j2 modules SYSSRC="$kdir" SYSOUT="$kdir" CC=clang LD=ld.lld LLVM=1 NV_KERNEL_MODULES=nvidia
```

The LTS headers were built with Clang 21.1.6; compilation used 23.1.4 and
printed a compiler mismatch warning. The open builds also emitted upstream
compiler/objtool warnings. The changed Gen2 code compiled without diagnostics.
Only `nvidia.ko` was requested; the other open kernel modules and full
615/610 diagnostic builds were not repeated because their sources were
unchanged or their Gen2 variant was covered by the shared-core comparison
and the 620 diagnostic build.

`tools/check-linux-patches.sh --version VER --source ARCHIVE` and
`tools/gen2/generate.py --version VER --source ARCHIVE --check` passed for
all four pinned archives. The checker applied all seven normal/diagnostic
combinations at fuzz 0, including ReBAR and optional profiling, and verified
that each embedded core and adapter matched the canonical files.

The complete resolved build/patch commands, exit codes, module hashes and
logs are saved locally in `artifacts/gen2-refresh-2026-11-08/`, including
`build-validation.json` and `patch-validation.json`. The build record's core
hash matches the final `gen2_core.h`. `git diff --check` also passed.

**Verified limitation:** a full CPU-RM LTS build exited 2 at the existing
`timeline.c:35` sysfs callback: these headers require `struct bin_attribute *`,
while the current callback takes `const struct bin_attribute *`. The separate
final `gen2.o` build passed with:

```bash
make -C /usr/lib/modules/6.12.69-2-cachyos-lts-lto/build M=/home/cyrid/Documents/cmpunlocker/artifacts/gen2-refresh-2026-11-08/cpu-lts LLVM=1 gen2.o
```

That object result does not establish a working full LTS module. The public
timeline compatibility code and the isolated GRID boot installation were
not changed by this Gen2 update.

## Test setup

- CMP40HX / TU106, PCI ID `10de:1f0b`, GPU `0000:10:00.0`.
- Physical upstream AMD port `0000:00:01.1`.
- MSI MPG B550 GAMING PLUS (MS-7C56), BIOS `1.M2`; VBIOS `90.06.67.00.06`.
- Linux `7.2.9-2-cachyos`, proprietary NVIDIA 615.78.08, GSP Firmware `N/A`.
- Isolated cold boot with the GPU unbound before the first operation.
- Stock-handoff compute unlock with the 1 MiB WPR heap; firmware/DMA cleanup
  completed before the pre-driver PCIe writes.
- CUDA clpeak 2.0.19, with a 150 W limit for the compute comparisons.

The pre-driver experiment used module 0.15.0; the runtime diagnostic used
0.16.0. The installed open NVIDIA driver was not replaced.

## Ordered hardware result

| Phase | GPU LnkCap2 | GPU/upstream live speed | Result |
|---|---|---|---|
| Cold, unbound | `00000002` | Gen1 x16 | Stock capability |
| Compute unlock and cleanup | `00000002` | Gen1 x16 | Full selectors, no retained DMA |
| Masked Gen2 policy | `00000006` | Gen1 x16 | Gen2 capability materialized |
| Pre-driver normal retrain | `00000006` | Gen2 x16 | GPU `STA=1102`, upstream `STA=3102` |
| Native CPU-RM initialization | `00000006` | Gen1 x16 | Capability and TLS=2 retained; policy rewritten |
| Compute and pinned DMA | `00000006` | Gen1 x16 | Healthy unlock, real Gen1 transfers |
| Normal post-driver retrain alone | `00000006` | Gen1 x16 | No promotion within bounded wait |
| Masked policy reapplied + normal retrain | `00000006` | Gen2 x16 | Native health and full selectors retained |
| DMA, Tensor Core workload, client close | `00000006` | Gen2 x16 | Link remained usable |

The relevant GPU-side values were:

| Register | After compute cleanup | Pre-driver policy | After native initialization | Runtime policy |
|---|---|---|---|---|
| `OVR 0x8872c` | `00000000` | `00000006` | `00000006` | `00000006` |
| `CFG 0x8c040` | `80045800` | `80085800` | `800c5800` | `80085800` |
| `PL 0x8c1c0` | `00120036` | `00140036` | `00220036` | `00240036` |
| `MISC 0x8841c` | `20342500` | `20342d00` | `e0b40d00` | `e0b42d00` |
| `CYA 0x8c2c0` | `068711b3` | `068711b3` | `068731b3` | `068731b3` |

Pre-driver training itself changed additional status fields in PL, producing
`00240036`. CPU-RM then restored the rate field to 1 and cleared MISC bit 13,
among other changes. These observations explain why retained PCI capability
and TLS values alone did not establish a working Gen2 policy. The experiment
tested the whole masked sequence; it does not isolate which individual writes
are necessary.

The masked sequence preserves unrelated fields:

```text
OVR  = 6
CFG  = (old & ~0x000c0000) | 0x00080000
PL   = (old & ~0x00060000) | 0x00040000
MISC = (old | bit11 | bit13) & ~(bit12 | bit14)
CYA  = old & ~bit2
```

It then sets TLS=2 on the endpoint and its actual upstream port and asserts
only the upstream Retrain Link bit. The Windows full MISC/CYA constants were
not needed for this Linux result.

## Workload verification

Pinned CUDA transfers confirmed the actual bandwidth change:

| Transfer | Before runtime policy, Gen1 x16 | After runtime policy, Gen2 x16 |
|---|---:|---:|
| Host to device | 3.4017 GB/s | 6.8037 GB/s |
| Device to host | 3.3517 GB/s | 6.7028 GB/s |

Live PCIe sampling at 100 ms intervals observed Gen1 during all 34 samples of
the first DMA test and Gen2 during all 29 samples of the second. Separate
compute comparisons produced:

| Workload | Before runtime policy | After runtime policy |
|---|---:|---:|
| FP32 | 7.8474 TFLOPS | 7.8804 TFLOPS |
| WMMA FP16 | 67.0009 TFLOPS | 67.2274 TFLOPS |
| WMMA INT8 | 132.0638 TOPS | 132.7539 TOPS |

All 399 live samples in the second compute run remained Gen2 x16. Full selectors
remained `SS0=88888888`, `SS1=00000008`; native NVIDIA queries continued to
work with GSP disabled. No new Xid or PCIe AER errors were recorded. The
clpeak `int8_k32` compile/load failure was also present in the previous
compute baseline; it is not a regression from this Gen2 change.

Linux sysfs `max_link_speed` still reported 2.5 GT/s, and NVIDIA reported
`Device Max: 1`, despite live Gen2 and the doubled DMA bandwidth. Maximum
speed reports can therefore be stale or inconsistent with the changed
hardware capability. This experiment used live `LnkCap/LnkCap2/LnkSta` and
actual transfers for acceptance, without modifying Linux's PCI cache.

## Cold control with runtime-only Gen2

I repeated the test after a full cold power-off with `gen2=off` during compute
unlock. Both the post-unlock and post-NVIDIA PCI snapshots still reported
`CAP2=00000002`, Gen1 x16, GPU TLS=1. Before the runtime helper, the GPU-side
state was:

```text
OVR=00000000 CFG=800c5800 PL=00120036 MISC=e0b40500 CYA=068731b3
CAP=00453d01 CAP2=00000002 CTL2=00000001
```

The public `linux/cpu-rm/gen2.sh` helper, using module 0.16.0, applied the masked
policy after native initialization. Its register writes were all logged under
the NVIDIA-owned device; no Gen2 writes preceded native initialization. The
result was:

```text
OVR=00000006 CFG=80085800 PL=00240036 MISC=e0b42d00 CYA=068731b3
CAP=00453d02 CAP2=00000006 GPU_STA=1102 UPSTREAM_STA=3102
GEN2_RUNTIME_RESULT error=0 device_ok=1 status=1102
```

| Cold runtime-only verification | Result |
|---|---:|
| Pinned host to device | 6.8025 GB/s |
| Pinned device to host | 6.7035 GB/s |
| FP32 | 7.7284 TFLOPS |
| WMMA FP16 | 66.5044 TFLOPS |
| WMMA INT8 | 132.1448 TOPS |
| Live link during compute | 409/409 samples Gen2 x16 |
| Live link during DMA | 29/29 samples Gen2 x16 |

Full selectors and native GSP-OFF operation remained healthy, with no new Xid
or AER errors. After both workloads and client close, the live link still
reported 5 GT/s x16. This control validates using ordinary pre-driver compute
unlock, then native CPU-RM initialization, then a single runtime Gen2 policy
application; preliminary PCIe preparation is not needed on the tested setup.

## NVIDIA 620.70.06 follow-up

Verified on 2026-11-08: the public runtime helper also worked with proprietary
NVIDIA 620.70.06 and GSP OFF on `7.3.3-1-cachyos`, using the same GPU and
physical upstream port. The compute unlock used `gen2=off`; no pre-driver
Gen2 writes preceded native initialization. NVIDIA DRM and modeset were
unloaded and `fuser` found no clients before running:

```bash
sudo bash linux/cpu-rm/gen2.sh --gpu 0000:10:00.0 \
  --module linux/cpu-rm/kernel/cmp40_preboot.ko
```

The helper exited 0 with `GEN2_RUNTIME_RESULT error=0 device_ok=1
status=1102`, then unloaded the diagnostic module and stopped its temporary
NVML monitor. The endpoint changed from `CAP2=00000002`, `STA=1101` to
`CAP2=00000006`, `STA=1102`; the upstream status changed from `3101` to
`3102`. OVR was 6, CFG was `80085800`, PL was `00240036`, MISC was
`e0b42d00`, and CYA was `068731b3` after training.

| 620.70.06 verification at 150 W | Before helper | After helper |
|---|---:|---:|
| Pinned H2D, GB/s | 3.40 | 6.80 |
| Pinned D2H, GB/s | 3.35 | 6.70 |
| FP16 WMMA, TFLOPS | 66.77 | 66.80 |
| INT8 WMMA, TOPS | 132.08 | 132.04 |

All 427 endpoint/upstream samples, taken at 100 ms intervals across the
combined compute, DMA and numerical workload, reported `1102` / `3102`.
Numerical WMMA matched 163,840 outputs with maximum absolute error 0.
Post-workload inspection retained full selectors; no new Xid, AER or
RmInitAdapter errors were found. The original 184 W limit was restored.
The known clpeak `int8_k32` compile/load failure remained present.

These tests followed driver unload after a cold power-on. NVIDIA and
persistenced had already run earlier in that boot, so the 620 result does
not establish operation before the first NVIDIA initialization. The isolated
615 cold-boot controls above remain separate evidence.

### Cold boot with the mkinitcpio hook

Verified on 2026-11-08: a separate run after a confirmed physical PSU
power-off reached Gen2 x16 with the installed mkinitcpio hook and proprietary
620.70.06 on `7.3.3-1-cachyos`. The card was the same ASUS CMP40HX with
VBIOS `90.06.67.00.06`, on MSI B550. The active NVIDIA `RegistryDwords`
setting was empty; the experimental `RMPcieLinkSpeed=4` option was disabled
for this boot.

The boot log records `PRE_DRIVER_UNLOCK_PASS` at 3.455 seconds and hook
completion at 3.456 seconds, before the NVIDIA module-load message at
9.358 seconds. The resident preboot module reported `mode=unlock`,
`unlock_error=0`, `resources_retained=N` and `gen2=off`. The initial snapshot
had no earlier Gen2 result. The initramfs module matched the tested build,
SHA256 `5175f5c2f5291f3e87f5f75cae4d2ea0ac88e046efa5d82b806da8889ee873c1`.

The completed preboot module was removed before the runtime test.
`nvidia_drm`, `nvidia_modeset` and `nvidia-persistenced` were stopped
temporarily; the core `nvidia` module stayed loaded and the GPU stayed bound
to it. The public helper ran once, with its existing client checks and NVML
monitor, using this command from the repository root:

```bash
bash /home/cyrid/Documents/cmpunlocker/linux/cpu-rm/gen2.sh \
  --gpu 0000:10:00.0 \
  --module /home/cyrid/Documents/cmpunlocker/linux/cpu-rm/kernel/cmp40_preboot.ko
```

It ran as root and exited 0 with `GEN2_RUNTIME_RESULT error=0 device_ok=1
status=1102`. The display modules and persistence service were then restored
successfully. Readbacks before preparation and immediately before the helper
matched; restoring the services retained Gen2.

| Register | Before helper | After policy, before retrain | After retrain |
|---|---|---|---|
| OVR | `00000000` | `00000006` | `00000006` |
| CFG | `800c5800` | `80085800` | `80085800` |
| PL | `00120036` | `00140036` | `00240036` |
| MISC | `e0b40500` | `e0b42d00` | `e0b42d00` |
| CAP2 | `00000002` | `00000006` | `00000006` |
| Endpoint Link Status | `1101` | `1101` | `1102` |
| Upstream Link Status | `3101` | `3101` | `3102` |

Gen2 capability was already present in the after-policy sample while the
physical link remained Gen1. The upper PL nibble changed from 1 to 2 between
that sample and the final retrain sample. Comparing the final `00240036`
directly with the GRID report's failed preparation state would mix phases;
these samples do not establish the meaning of each PL field.

Verified workload commands, run as root with the existing 150 W power limit:

```bash
/home/cyrid/Загрузки/clpeak-2.0.19-linux-x86_64-cuda/bin/clpeak \
  --cuda --cuda-device 0 --single-precision-compute --transfer-bandwidth \
  --json-file /home/cyrid/Documents/cmpunlocker/artifacts/issue8/cold-620-2026-11-08/validation-ab30ca98/clpeak.json \
  --csv-file /home/cyrid/Documents/cmpunlocker/artifacts/issue8/cold-620-2026-11-08/validation-ab30ca98/clpeak.csv
/home/cyrid/Документы/artifacts_backup_20260511/tu106-preboot/verify-wmma
```

Both exited 0. With CUDA toolkit 13.2 and driver API 13.5, clpeak reported
7737.49 GFLOPS FP32, pinned H2D 6.80 GB/s and pinned D2H 6.70 GB/s. Numerical
WMMA verified 163,840 outputs across 20 launches with maximum absolute error
0. All 26 link samples over about 2.6 seconds reported `1102` / `3102`.
No new Xid, AER or RmInitAdapter errors appeared in the kernel log. The power
limit remained 150 W; temperature changed from 36.19 C to 40.12 C.

Raw logs, command records and snapshots are retained locally under
`artifacts/issue8/cold-620-2026-11-08/`, for boot ID
`ab30ca98-4a20-4320-9b4c-3b736281375f`. The baseline collector used PCI and
MMIO reads without querying NVIDIA; boot services had initialized the driver.
This verifies one cold boot and a short workload on the native 620 driver.
GRID 580.178.05, vGPU guests and VFIO were not exercised. Builds and offline
module tests were not repeated because the module and helper were unchanged.

## Integration

The opt-in pre-driver modes are `gen2=prepare` and `gen2=retrain`. Gen2 runs
after compute unlock, Falcon cleanup and DMA release, with an independent
`gen2_error`; a link timeout cannot enter the Booter FLR error handler.

The explicit runtime mode is `mode=gen2_runtime gen2=retrain`. It does not
execute firmware, allocate firmware DMA, detach NVIDIA, change PCI enable
counts or perform a reset. In addition to exact CMP40HX identity and an
active link, it requires the NVIDIA driver, full selectors and halted GSP.
The [CPU-RM helper](../../linux/cpu-rm/gen2.sh) verifies proprietary GSP OFF,
native health and absence of open GPU clients, then temporarily holds an
NVML client while the module reapplies the masked policy and retrains.

The existing driver-reprobe helper remains specific to the patched open
driver's repeated Gen2 setup. Enabling it for proprietary CPU-RM would not
reproduce the policy reapplication tested here.

## Remaining controls

These are two cold-boot sequences on one platform and driver version. The
runtime-only cold control passed; the pre-driver Gen2 modes can remain research
controls while normal integration applies Gen2 after native initialization.
Other cards, 610.43.03, repeated runtime-only cold boots, NVIDIA reinitialization
and suspend/resume remain unvalidated. No automatic service is installed or
enabled by this change.

## Early preparation on other platforms

The runtime-only success on MSI B550 does not establish that pre-driver Gen2
policy is unnecessary on every platform. The `--gen2 prepare` and
`--gen2 retrain` modes are retained for that comparison.

[Discussion #10](https://github.com/Cyridd/cmpunlocker/discussions/10) reports
ASUS `1043:8804`, VBIOS `90.06.67.00.06`, CachyOS `7.2.7-1-cachyos` and NVIDIA
open modules 610.57.04. The endpoint already advertises Gen2 with TLS=2,
CFG/PL rate fields=2 and `DIS_G2=0`, yet ordinary retraining and a repeated
NVIDIA probe leave Gen1 x16. The posted excerpt does not include the full
OVR/MISC state or a pre-driver snapshot. Its `EqualizationComplete-` flag
alone does not diagnose a Gen2 failure; that status concerns Gen3 and higher
link equalization.

The [Alder Lake audit](PCIE_LINK_DISABLE_AUDIT.md) separately establishes that
its physical link can train Gen2, but the tested Link Disable cycle loses
GSP/RM. These observations support testing whether earlier policy setup
changes the first initialization or capability-sampling boundary. They do
not yet prove that early preparation fixes either report.

For a proprietary CPU-RM comparison on an affected platform, use separate
cold boots with the same driver and workload:

1. Compute unlock with Gen2 off, native NVIDIA initialization, then the late
   Gen2 helper.
2. Compute unlock with `--gen2 prepare`, native NVIDIA initialization, then
   the same late helper. This changes policy timing without a pre-driver
   retrain.
3. If the first two results justify it, compare `--gen2 retrain` as a third
   cold-boot variant.

Collect OVR, CFG, PL, MISC, CYA and live endpoint/upstream capabilities before
and after each boundary, then verify native health, full selectors and DMA
bandwidth. Keep Link Disable out of these comparisons. The two community
reports use open/GSP drivers; switching them to proprietary CPU-RM is itself
a driver-path change and cannot isolate the effect of earlier preparation
in the open driver. The CPU-RM runtime helper requires GSP OFF.

## VFIO passthrough: host retrain in issue #20

[Issue #20](https://github.com/Cyridd/cmpunlocker/issues/20#issuecomment-6497769200)
uses full VFIO passthrough on Proxmox 9.2, kernel `7.0.14-24-pve`, QEMU
`11.0.3-4` and q35/OVMF. The guest runs patched open NVIDIA 610.57.04 with
GSP ON. Its ASUS CMP40HX (`1043:8804`, VBIOS `90.06.67.00.04`) is directly
behind a physical AMD `1022:14db` root port. This differs from the proprietary
CPU-RM tests above and the GRID/vGPU host in [issue #8](GRID_CPU_RM_REPORT.md).

The [host experiment result](https://github.com/Cyridd/cmpunlocker/issues/20#issuecomment-6569682975)
records a masked Gen2 target write and an ordinary Retrain Link request on
the physical root port, with the VM running and its GPU declared idle. The
test left endpoint LnkCtl2 and GPU MMIO untouched. The bounded poll did not
confirm Gen2:

| Physical config field | Before request | After request, before rollback |
|---|---|---|
| GPU FLAGS | `0001` | `0001` |
| GPU LnkCap | `00453d01` | `00453d01` |
| GPU LnkSta | `1101` | `1101` |
| Root FLAGS | `0142` | `0142` |
| Root LnkCap | `00737105` | `00737105` |
| Root LnkCap2 | `0180003e` | `0180003e` |
| Root LnkCtl2 | `0041` | `0042` |
| Root LnkSta | `3101` | `3101` |

The physical root port accepted TLS=2. Both snapshots show Gen1 x16, and the
five-second poll did not confirm Gen2. The endpoint reports capability v1
and separately advertises a maximum speed of Gen1; capability version and
negotiated speed are different fields. The root port supports Gen2 and up
to 32 GT/s. Setting the physical root-port target and requesting retrain on
the host was insufficient; the endpoint's Gen1 capability remains unresolved.
The output does not capture a transient training sequence or establish the
cause of that capability state.

The script reports restoring the original root-port target after
`GEN2_NOT_CONFIRMED`; no post-rollback readback was supplied. Guest
`nvidia-smi` still works and shows P8 with no active processes, but no post-test
CUDA workload, physical link sampling under load or kernel-error audit was
supplied. BAR1 remains 64 MiB, BAR3
32 MiB and the bridge's prefetchable window 96 MiB; ReBAR stays disabled.

Earlier host snapshots show endpoint capability v2 with the VM stopped and
v1 after guest initialization. A useful next control is to collect physical
host snapshots with the VM stopped, with it running before guest NVIDIA
initializes, and after `CMP40_GSP_READY`. This can separate assignment/reset
effects from guest initialization as candidate causes of the capability
change. The existing [diagnostic patch](PCIE_GEN2_DIAGNOSTIC.md) can then
compare the guest's BAR0 policy mirrors with host config-space reads.
Its PCI accessor output alone does not establish physical CAP2/CTL2 when
the cached capability version is v1.

This result does not establish a Gen2 fix or justify Link Disable, reset-policy
changes or undocumented GPU register writes on the running VFIO device.
