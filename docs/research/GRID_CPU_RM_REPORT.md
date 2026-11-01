# CMP40HX CPU-RM on GRID 19.6

The [2026-11-01 report in issue #8](https://github.com/Cyridd/cmpunlocker/issues/8#issuecomment-6555353992)
describes successful compute unlock with the existing pre-driver module at
commit `275bf98`, followed by proprietary GRID 19.6 / NVIDIA 580.178.05.
The report was prepared with another language model and supplied by @Scirese;
its logs are useful evidence, but I have not reproduced this host myself.

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
610.43.03 and 615.78.08 drivers. It does not yet establish Tensor Core
throughput inside a guest, numerical correctness, sustained vGPU workloads,
guest restart, host driver reinitialization or suspend/resume.

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
- Confirm guest Tensor Core throughput and numerical results independently
  of PCIe speed, then exercise guest/manager lifecycle transitions.

The public runtime v1 guard remains in place. This report does not justify
changing kernel PCI caches, blindly clearing CFG bit 22, or enabling Link
Disable on a live GRID host. See [the B550 Gen2 report](CPU_RM_PCIE_GEN2.md)
and [Link Disable audit](PCIE_LINK_DISABLE_AUDIT.md) for the tested boundaries.
