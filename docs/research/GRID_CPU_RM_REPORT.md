# CMP40HX CPU-RM on GRID 19.6

The [host report in issue #8](https://github.com/Cyridd/cmpunlocker/issues/8#issuecomment-6555353992)
describes successful compute unlock with the existing pre-driver module at
commit `275bf98`, followed by proprietary GRID 19.6 / NVIDIA 580.178.05.
The [guest follow-up](https://github.com/Cyridd/cmpunlocker/issues/8#issuecomment-6570291262)
includes Linux CUDA and Windows OpenCL benchmarks, plus about half an hour
of repeated clpeak runs without noticed instability or driver errors.
These are community results supplied by @Scirese; they have not been
independently reproduced here.

## Reported setup and result

| Item | Reported value |
|---|---|
| Host | Proxmox VE 9.2.24, kernel `7.0.14-26-pve` |
| Card | CMP40HX/TU106, `10de:1f0b`, ASUS `1043:8804`, 8 GiB |
| VBIOS | `90.06.67.00.06` |
| Board | OEM X99/C612, electrically x8 slots |
| Upstream | `0000:00:01.0`, root port, maximum 8 GT/s x8 |
| Driver | GRID 19.6 / 580.178.05 with vgpu-unlock |
| Compute result | `PRE_DRIVER_UNLOCK_PASS`, `SS0=88888888`, `SS1=00000008` |
| Native result | GSP OFF, native driver and vGPU manager reported healthy |
| PCIe result | Gen1 x8; runtime Gen2 did not succeed |

This extends the compatibility evidence beyond the independently tested
610.43.03 and 615.78.08 drivers. Guest Tensor Core throughput is now documented,
while numerical correctness, guest restart, host driver reinitialization and
suspend/resume remain untested in this report.

## Guest benchmarks and stability

The follow-up reports that compute unlock works without additional changes
to the guest drivers. The Linux guest exposes `NVIDIA RTXA6000-1Q` with
1024 MB VRAM; the Windows guest exposes `NVIDIA RTXA6000-4Q` with 4095 MB.
These are virtual profile names on the physical TU106 card. The Linux
screenshot identifies `sm_75` and 34 SMs; the Windows screenshot lists
34 compute units at 1650 MHz.

### Linux guest: clpeak CUDA backend

clpeak displays `Driver Version: 13.0` and `Toolkit: 13.4` in its CUDA header.
The screenshot does not identify the guest's NVIDIA kernel-driver release.

| Measurement | Reported result |
|---|---:|
| FP32 loop | 7.79 TFLOPS |
| FP16, half / half2 | 16.4 / 16.4 TFLOPS |
| FP64 | 258 GFLOPS |
| Mixed FP16 × FP16 + FP32, shader cores | 8.18 TFLOPS |
| INT32 IMAD | 8.08 TOPS |
| INT8 `dp4a`, four variants | 32.1 / 32.1 / 32.2 / 32.0 TOPS |
| WMMA FP16 | 65.8 TFLOPS |
| WMMA INT8 | 131 TOPS |
| WMMA INT4 | 247 TOPS |
| Global memory, float / float2 / float4 | 372 / 391 / 404 GB/s |
| Local memory, float / float2 / float4 | 2.77 / 3.43 / 3.74 TB/s |

### Windows guest: OpenCL-Benchmark

The screenshot reports guest driver `582.78 (Windows)` and OpenCL 3.0.

| Measurement | Reported result |
|---|---:|
| FP64 | 0.258 TFLOPS |
| FP32 | 8.251 TFLOPS |
| FP16, half2 | 16.387 TFLOPS |
| INT64 / INT32 / INT16 | 1.965 / 8.122 / 7.131 TIOPS |
| INT8 `dp4a` | 29.730 TIOPS |
| Coalesced memory read / write | 399.30 / 409.18 GB/s |
| Misaligned memory read / write | 261.34 / 80.71 GB/s |
| PCIe send / receive / bidirectional | 1.40 / 1.43 / 1.41 GB/s |

OpenCL-Benchmark labels the PCIe link `Gen1 x16`, while the physical host
report establishes Gen1 x8. The guest label does not verify physical lane
width. The reporter suggests Gen1 x8 and VM overhead explain the lower
throughput relative to the examples in [results](../results.md), but these
runs do not isolate either cause.

The repeated clpeak runs provide a reported stability observation over about
half an hour. No paired stock/FULL guest runs, ZERO control, numerical check
or captured kernel-error audit was supplied. Guest lifecycle and longer
workloads still need separate validation.

## Integration fixes

Two issues were independent of the unlock payload:

1. `nvidia_vgpu_vfio` also matches the GPU PCI alias and declares
   `softdep pre: nvidia`. Blacklisting only `nvidia` allowed the vGPU module
   to pull it in before compute unlock. Blacklist both for the pre-driver
   boot, then explicitly load `nvidia_vgpu_vfio` after native health and
   GSP OFF are verified.
2. This GRID build has no `/sys/module/nvidia/parameters` directory. Its
   actual setting is `EnableGpuFirmware: 0` in `/proc/driver/nvidia/params`,
   with `GSP Firmware Version: N/A` in NVIDIA-SMI. Both CPU-RM helpers now
   accept the sysfs or /proc representation. An explicitly enabled setting
   is still rejected.

The [CPU-RM guide](../../linux/cpu-rm/README.md) includes this ordering.

## Gen2 observations

The stock runtime helper refused the endpoint's live PCIe capability v1 with
`error=-95` before any writes. The upstream capability was v2. A locally
relaxed endpoint check allowed the five PL0 writes, then failed before the
Retrain Link request, with `error=-5`, `device_ok=1`, link Gen1 x8.

| Register | Before policy | After policy |
|---|---|---|
| OVR `0x8872c` | `00000000` | `00000006` |
| CFG `0x8c040` | `804c5800` | `80485800` |
| PL `0x8c1c0` | `00310036` | `00350036` |
| MISC `0x8841c` | `e0b46500` | `e0b42d00` |
| CYA `0x8c2c0` | `068731b3` | `068731b3` |
| BAR0 CAP2 `0x880a4` | `00000002` | `00000002` |

The report also says the policy changed live `PCI_EXP_FLAGS` from `0001` to
`0002`. CFG bit 22 (`00400000`) differs from the tested B550 state. This is
a candidate for further investigation, not evidence that vGPU sets that
bit or that clearing it is correct. No new Xid/AER was reported.

### Linux's cached capability version

The TLS failure needs a separate check before treating it as hardware
clamping. In [Linux v7.0 PCI access.c](https://github.com/torvalds/linux/blob/v7.0/drivers/pci/access.c#L349),
`pcie_cap_version()` uses `pcie_caps_reg(dev)`, which returns the cached
`pdev->pcie_flags_reg`. `pcie_cap_has_lnkctl2()` requires cached version >1.
Consequently, when that cache is v1, standard `pcie_capability_read_*`
functions return zero for LNKCAP2/LNKCTL2 and the write helper returns success
without issuing the corresponding config write. The FLAGS register itself
is read live.

Thus a live transition to v2 does not refresh Linux's enumeration-time cache.
Removing just the live-v1 guard can still leave the TLS write suppressed by
the PCI accessor. The posted `CTL2=0000` from PCI access, alongside BAR0
`CTL2=00000001`, is consistent with this possibility. The reporter has not
yet supplied the cached flags or a raw PCI readback, so this is an identified
software boundary rather than a confirmed diagnosis of that host.

The BAR0 CAP2 observation remains separate: it still advertises Gen1 only.
Resolving accessor suppression alone would not prove usable Gen2.

## Next controlled checks

- Compare cached `pcie_flags_reg`, live FLAGS and raw PCI CAP2/CTL2 with
  their BAR0 mirrors, before and after policy application. Keep the device
  bound and perform these comparisons with reads only.
- Establish when v1 and CFG bit 22 first appear: enumeration, pre-driver
  compute cleanup, native NVIDIA load, vGPU-module load or manager startup.
- Compare separate cold boots with Gen2 off versus pre-driver `prepare`,
  keeping GRID, guests and the later helper identical.
- Independently reproduce the guest measurements with paired stock/FULL and
  numerical controls, then exercise guest/manager lifecycle transitions.

The public runtime v1 guard remains in place. This report does not justify
changing kernel PCI caches, blindly clearing CFG bit 22, or enabling Link
Disable on a live GRID host. See [the B550 Gen2 report](CPU_RM_PCIE_GEN2.md)
and [Link Disable audit](PCIE_LINK_DISABLE_AUDIT.md) for the tested boundaries.
