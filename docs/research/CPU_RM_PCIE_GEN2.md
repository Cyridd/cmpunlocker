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
