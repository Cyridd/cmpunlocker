# CMP40HX CPU-RM on GRID 19.6

The [host report in issue #8](https://github.com/Cyridd/cmpunlocker/issues/8#issuecomment-6555353992)
describes successful compute unlock with the existing pre-driver module at
commit `275bf98`, followed by proprietary GRID 19.6 / NVIDIA 580.178.05.
The [guest follow-up](https://github.com/Cyridd/cmpunlocker/issues/8#issuecomment-6570291262)
includes Linux CUDA and Windows OpenCL benchmarks, plus about half an hour
of repeated clpeak runs without noticed instability or driver errors.
These guest results were supplied by @Scirese and have not been independently
reproduced here. Local controls confirm pre-driver completion, NVIDIA-SMI
initialization and [runtime Gen2 x16](#third-cold-boot-native-grid-gen2)
on 580.178.05 without `vgpu-unlock`.

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

## Local reproduction preparation

**Verified, 2026-11-08:** a separate CachyOS boot environment was prepared
for native GRID initialization on the local CMP40HX. The second cold boot
subsequently passed native initialization, as recorded below. The preceding
cold control with NVIDIA 620.70.06 passed;
its results are in [the Gen2 report](CPU_RM_PCIE_GEN2.md#cold-boot-with-the-mkinitcpio-hook).

The supplied package contains host driver `580.178.05` and Linux guest driver
`580.178.04`. The native test uses
`Host_Drivers/NVIDIA-Linux-x86_64-580.178.05-vgpu-kvm.run`, whose SHA-256 is
`c084ebcb98b2d166309da3b59c64ab0db5df0418eb602e4b51c65939276e526d`.
The archive's integrity check passed during extraction. This records the
supplied file's identity; an independent comparison with an NVIDIA-published
checksum was not performed.

The test root is a writable Btrfs snapshot, subvolume ID `649`, under
`artifacts/issue8/grid-580-2026-11-08/rootfs`. Its `fstab` mounts only that
root and a temporary `/tmp`. Its home directory and logs are separate from
the ordinary system, and the ESP is not mounted there. Limine entry
`CachyOS / cmp40-grid-580` selects kernel `6.12.69-2-cachyos-lts-lto` and a
dedicated initramfs. The ordinary boot entries, kernel images and initramfs
files passed unchanged-content checks after deployment.

### Build and image checks

From the repository root, the successful builds were:

```bash
make -C artifacts/issue8/grid-580-2026-11-08/driver/kernel -j4 \
  SYSSRC=/usr/lib/modules/6.12.69-2-cachyos-lts-lto/build \
  SYSOUT=/usr/lib/modules/6.12.69-2-cachyos-lts-lto/build \
  KERNEL_UNAME=6.12.69-2-cachyos-lts-lto LLVM=1 CC=clang LD=ld.lld modules
make -C artifacts/issue8/grid-580-2026-11-08/cmp40-kernel -j4 \
  KDIR=/usr/lib/modules/6.12.69-2-cachyos-lts-lto/build LLVM=1
make -C /usr/lib/modules/6.12.69-2-cachyos-lts-lto/build \
  M=/home/cyrid/Documents/cmpunlocker/artifacts/issue8/grid-580-2026-11-08/pci-cache \
  LLVM=1 -j4 modules
```

All three commands exited `0`. The proprietary GRID sources were unchanged.
The first `cmp40_preboot` build failed because Linux 6.12 expects a non-const
`struct bin_attribute *` in the sysfs read callback. Removing `const` from
`timeline_read` in the separate LTS build copy fixed that error. The
repository's module sources and working 7.3.3 module were left unchanged.
The diagnostic module reads Linux's cached `pcie_flags_reg` and raw PCI
FLAGS/CAP2/CTL2/status for the GPU and its physical upstream port. It does
not update the cache, enable the device, map BAR0 or request a retrain.

The compiler was Clang/LLD `23.1.4`; the LTS kernel was built with Clang
`21.1.6`. Kbuild reported that mismatch. GRID also produced source and
objtool warnings, including naked returns in a `MITIGATION_RETHUNK` build.
Successful linking and matching vermagic do not establish runtime
compatibility. The first cold boot stopped during the pre-driver module
load; the revised test module and native GRID loaded in the second boot.
This kernel also differs from the reporter's
`7.0.14-26-pve`.

The initramfs was built inside the snapshot using `systemd-nspawn` and:

```bash
depmod -a 6.12.69-2-cachyos-lts-lto
mkinitcpio -k 6.12.69-2-cachyos-lts-lto \
  -c /etc/cmp40-grid-mkinitcpio.conf --nopost --no-cmdline \
  -g /opt/cmp40-grid/initramfs
```

The build exited `0`. `lsinitcpio` listing and extraction checks confirmed
the unlock service, the matching LTS module, the same pinned firmware as
the working host, stock handoff, the small WPR heap and `gen2=off`. No
NVIDIA, nouveau or nova kernel module is present in the image. The initial image's SHA-256 was
`9762c46fb47d67d19c3cfcdef1363d6253130f7b76b135d6b2e082648fc562b1`.

The load plan was inspected inside the snapshot with
`modprobe -S 6.12.69-2-cachyos-lts-lto --show-depends --ignore-install nvidia`.
It contains only `nvidia.ko`. This dry run inherits the running host's
`/proc/cmdline`; substituting the test command line through an nspawn bind
mount failed with `ENOENT`. The staged modprobe settings and test boot
command line were therefore checked separately. Both select GSP OFF and
omit the ordinary system's PCIe and license options.

`verify-root.py` and `deploy-boot.py --apply` both exited `0` after their
checks passed. The latter used `limine-entry-tool --add-kernel` and
`limine-enroll-config`; copied-image checksums, the resolved command line
and preservation of the ordinary boot configuration passed. Full commands
and check results are saved in `root-verification.json` and
`boot-verification.json` beside those scripts. Python syntax checks passed,
and `native-test.py --check` refused the ordinary 620 boot with exit `1`,
as intended.

### Native test procedure

After a full power-off, select `CachyOS / cmp40-grid-580`, log in as
`cyrid` with the existing password, and run:

```bash
sudo cmp40-grid-test native
```

The script requires the prepared root, LTS kernel, PCI identities, module
hashes and successful initramfs unlock. It saves three stages: after
compute unlock, after loading native GRID, and after the first matching
580.178.05 `nvidia-smi -q`. It compares the cached and raw PCI capabilities
and reads the previously audited BAR0 offsets only when the card is already
active in D0 with memory decoding enabled. It stops before Gen2, the vGPU
module, manager or any guest. Logs stay in
`/var/lib/cmp40-grid-test/native-<boot-id>/` inside the test root and can be
read from the ordinary system after returning to it.

Only the host module and matching host utilities are staged for this first
run. No guest CUDA stack or `vgpu-unlock` modification is installed.
**Hypothesis:** the first native GRID initialization may reproduce the
capability or CFG differences. A successful stock native test would still
leave the effects of `vgpu-unlock`, vGPU startup and the reporter's platform
to investigate.

### First cold boot: pre-driver power check

**Verified:** boot `6d944f64ba8546dba4fe08df93dce713` reached the test console
on 2026-11-08. I reported a cold start and a refusal from
`sudo cmp40-grid-test native`. Inspection of the snapshot's persistent
journal found:

```text
cmp40-unlock: unlocking 0000:10:00.0 (CPU-RM, stock handoff, small WPR heap)
cmp40-unlock: FAILED: modprobe cmp40_preboot failed for 0000:10:00.0
modprobe: ERROR: could not insert 'cmp40_preboot': Host is down
```

The initramfs failure guard remained installed. No `PRE_DRIVER_UNLOCK_PASS`,
native NVIDIA load or Gen2 result was recorded. The native test stopped at
the guard, before its first capture. The saved journal and parsed findings
are `first-boot-journal.txt` and `first-boot-result.json` in the preparation
directory. The journal was read with:

```bash
sudo -n journalctl --directory=/home/cyrid/Documents/cmpunlocker/artifacts/issue8/grid-580-2026-11-08/rootfs/var/log/journal --list-boots --no-pager
sudo -n journalctl --directory=/home/cyrid/Documents/cmpunlocker/artifacts/issue8/grid-580-2026-11-08/rootfs/var/log/journal -b 0 --no-pager -o short-monotonic
```

**Hypothesis:** the pre-driver check rejected cached `PCI_UNKNOWN` while
the physical card was already in D0. The initial module did not log either
the cached state or PMCSR, so the first boot cannot establish that pair.
The [upstream 6.12.69 PCI code](https://github.com/gregkh/linux/blob/v6.12.69/drivers/pci/pci.c)
sets runtime PM active in `pci_pm_init()`, while
`pci_enable_device_flags()` separately refreshes the cached PCI power state.
The [runtime PM code](https://github.com/gregkh/linux/blob/v6.12.69/drivers/base/power/runtime.c)
returns without invoking a resume callback when the device is already
runtime-active. A successful runtime-PM reference therefore does not by
itself prove that `pdev->current_state` has been refreshed to D0.

The separate LTS build now logs the PM-reference result and cached state.
Its experimental power check accepts `PCI_UNKNOWN` only for an explicit
unlock on an unbound device, while holding the existing PCI lock and active
PM reference, and only if a successful PMCSR read confirms D0. Missing PM
capabilities, failed reads, `ffff`, lower power states and unknown state
during inspection remain rejected. The fallback does not write PMCSR or
update either PCI cache. The repository module and installed ordinary-boot
module remain unchanged.

**Verified:** the following tests and rebuild exited `0`:

```bash
cc -std=c11 -Wall -Wextra -Werror -O2 artifacts/issue8/grid-580-2026-11-08/test-power-guard.c -o artifacts/issue8/grid-580-2026-11-08/test-power-guard
artifacts/issue8/grid-580-2026-11-08/test-power-guard
make -C artifacts/issue8/grid-580-2026-11-08/cmp40-kernel -j4 KDIR=/usr/lib/modules/6.12.69-2-cachyos-lts-lto/build LLVM=1
sudo -n python3 /home/cyrid/Documents/cmpunlocker/artifacts/issue8/grid-580-2026-11-08/refresh-power-guard.py --apply
```

The 16 unit cases exercise the actual fallback helper, including refusals
and preservation of the cached state. Python syntax and the refusal outside
the test boot also passed. The refreshed initramfs, installed hashes,
Limine checksums and config enrollment passed verification; ordinary boot
images and entries were unchanged. The compiler mismatch warning remains.
These were offline checks. The second boot below subsequently exercised the
fallback on the GPU.

The `power-guard-1` initramfs SHA-256 is
`7ae3266b9cfc37dc188c3db593ddb94c40cf3a27ab78f5fb7b95d9fe1239e015`.
The previous image, module and preparation records are preserved in
`power-guard-backup/`. The command remains `sudo cmp40-grid-test native`.
If its preconditions fail, it now saves a read-only report under
`/var/lib/cmp40-grid-test/refused-<boot-id>/` without removing the guard,
retrying unlock or loading a GPU module.

### Second cold boot: native GRID initialization

**Verified:** boot `9b6384aa-cb13-4d0a-8a64-dae61f94c668` passed the revised
pre-driver check, compute-unlock completion and native GRID initialization.
I confirmed that the PSU was disconnected for about 30 seconds before this
boot and ran `sudo cmp40-grid-test native`. The recorded commands
`modprobe --ignore-install nvidia NVreg_EnableGpuFirmware=0` and
`/usr/bin/nvidia-smi -q` both exited `0`.

The journal directly recorded the previously suspected state pair:

```text
POWER_REFERENCE ret=0 unlock=1 cached=5 runtime_active=1
POWER_D0_CONFIRMED cached=5 pmcsr=0008 (read-only fallback; cache unchanged)
PRE_DRIVER_UNLOCK_PASS
```

Cached state `5` is `PCI_UNKNOWN`. PMCSR `0008` has power-state bits `0`,
meaning D0. Thus runtime PM was active while the cached PCI power state was
unknown. The old guard would reject this combination. The first boot did
not log its state pair, but the second boot confirms this failure condition
and exercises the fallback successfully.

NVIDIA-SMI identifies driver `580.178.05`, CMP40HX, VBIOS
`90.06.67.00.06`, GSP firmware `N/A`, and virtualization mode `None`.
`nvidia_vgpu_vfio` was not loaded. The full saved boot journal contains no
Xid, AER error, RmInitAdapter failure, BUG or Oops. This is initialization
evidence; no CUDA workload, DMA check or guest was run.

The endpoint's cached and raw PCIe flags were both `0002` in all three
captures: after compute unlock, after module loading and after NVIDIA-SMI.
The upstream flags were `0142`. The first BAR0 capture was skipped because
PCI memory decoding was disabled (`COMMAND=0000`); the collector did not
enable it. Native module loading changed COMMAND to `0007`.

After the first NVIDIA-SMI query, all 13 collected BAR0 values matched the
620.70.06 cold control. Selected values show the differences from the
reporter's pre-policy state:

| Field | Local GRID 580.178.05 | Local 620.70.06 | Reported GRID host |
|---|---|---|---|
| Live endpoint PCIe capability version | 2 | 2 | 1 |
| OVR | `00000000` | `00000000` | `00000000` |
| CFG | `800c5800` | `800c5800` | `804c5800` |
| PL | `00120036` | `00120036` | `00310036` |
| MISC | `e0b40500` | `e0b40500` | `e0b46500` |
| CYA | `068731b3` | `068731b3` | `068731b3` |
| CAP2 | `00000002` | `00000002` | `00000002` |

The intermediate capture after module loading had MISC `20342500`,
CFG `80045800`, CYA `068711b3` and HIER `0`. After the first NVML query these
became `e0b40500`, `800c5800`, `068731b3` and `1`. Module binding and the
first native initialization therefore need separate comparison points.

The link remained Gen1 x16. CAP2 `2` is the expected pre-policy state in the
successful 620 control as well; it does not establish a Gen2 failure by
itself. **Hypothesis:** the reporter's differences arise from platform or
enumeration state, `vgpu-unlock`, or later vGPU initialization. Clean native
GRID did not produce them on this B550 host. This second boot stopped before
Gen2; the third boot below tested it.

Evidence is saved under
`rootfs/var/lib/cmp40-grid-test/native-9b6384aa-cb13-4d0a-8a64-dae61f94c668/`,
with a normalized comparison in `second-boot-result.json`. The full journal
was read with:

```bash
sudo -n journalctl --directory=/home/cyrid/Documents/cmpunlocker/artifacts/issue8/grid-580-2026-11-08/rootfs/var/log/journal -b 9b6384aacb134d0a8a64dae61f94c668 --no-pager -o short-monotonic
```

### Prepared native Gen2 control

The same test boot now provides `sudo cmp40-grid-gen2`. It runs or validates
the native control from that boot, checks the completed unlock and idle
driver, removes only `cmp40_preboot`, then invokes the unchanged public
`gen2.sh` once. The core NVIDIA module stays bound. The helper retains its
v1 guard, client checks, NVML monitor and post-retrain health query.
The wrapper saves results under `/var/lib/cmp40-grid-test/gen2-<boot-id>/`
and stops before workloads or vGPU startup. An existing attempt directory
prevents a second invocation in the same boot.

**Verified:** `python3 artifacts/issue8/grid-580-2026-11-08/test-gen2-runner.py`
passed 7 native-record cases, 10 link-result cases and rejection outside the
test boot. `bash -n linux/cpu-rm/gen2.sh linux/cpu-rm/common.sh` exited `0`.
`sudo -n python3 /home/cyrid/Documents/cmpunlocker/artifacts/issue8/grid-580-2026-11-08/install-gen2-runner.py --apply`
exited `0`; installed file hashes matched, and the helper scripts, test
initramfs and ordinary boot images were unchanged. This validates preparation
and those guard/result checks. The third boot below exercised the hardware
sequence.

### Third cold boot: native GRID Gen2

**Verified, 2026-11-08:** boot `76208954-bcc4-4572-af31-354626316a27`
reached Gen2 x16 with the same GRID 580.178.05 and LTS kernel. I confirmed
disconnecting the PSU for about 30 seconds before this boot and ran
`sudo cmp40-grid-gen2`. The command completed native initialization, removed
the completed unlock module and invoked the public helper once:

```bash
bash /opt/cmp40-grid/tools/cpu-rm/gen2.sh --gpu 0000:10:00.0 \
  --module /usr/lib/modules/6.12.69-2-cachyos-lts-lto/extra/cmp40_preboot.ko
```

The helper exited `0`, including its post-retrain NVIDIA-SMI health check.
The kernel recorded `GEN2_RUNTIME_RESULT error=0 device_ok=1 status=1102`.
Both ends reported `5.0 GT/s PCIe`, width `16`; endpoint status was `1102`
and upstream status was `f102`. Their different upper status bits do not
change the matching speed and width. Native NVIDIA stayed bound throughout
the captured sequence; no core-driver unbind or reload was requested.
GSP was off, `vgpu-unlock` was absent, and `nvidia_vgpu_vfio` was not loaded.

| Register | Before policy | After policy, before retrain | After retrain |
|---|---|---|---|
| OVR | `00000000` | `00000006` | `00000006` |
| CFG | `800c5800` | `80085800` | `80085800` |
| PL | `00120036` | `00140036` | `00240036` |
| MISC | `e0b40500` | `e0b42d00` | `e0b42d00` |
| CAP2 | `00000002` | `00000006` | `00000006` |
| Endpoint Link Status | `1101` | `1101` | `1102` |
| Upstream Link Status | `7101` | `7101` | `f102` |

All 13 captured final BAR0 values match the working 620.70.06 cold control.
The endpoint's cached and live PCIe flags remained `0002`; the upstream
flags remained `0142`. Both Link Control words also stayed unchanged:
endpoint `0140`, upstream `0040`. Thus Common Clock Configuration (CCC,
bit 6) was `1` on both ends before and after success. ASPM was already off.
HASD in Link Control 2 and upstream HAWD in Link Control were both clear;
neither was needed for this run.

The full journal through shutdown contains no Xid, AER/PCIe Bus Error,
RmInitAdapter failure, BUG or Oops. The test verifies a single retrain and
the helper's native health check. It does not verify DMA throughput, CUDA
correctness, guest operation or continuous link stability until shutdown.
Those workloads were deliberately outside this test's scope.

**Verified:** these commands exited `0`:

```bash
sudo -n journalctl --directory=/home/cyrid/Documents/cmpunlocker/artifacts/issue8/grid-580-2026-11-08/rootfs/var/log/journal -b 76208954bcc44572af31354626316a27 --no-pager -o short-monotonic > /home/cyrid/Documents/cmpunlocker/artifacts/issue8/grid-580-2026-11-08/third-boot-journal.txt
python3 artifacts/issue8/grid-580-2026-11-08/review-third-boot.py
```

The review script passed 16 evidence checks and saved the PCI decoding,
620 comparison and evidence hashes in `third-boot-result.json`. The
experimental `gen2.c`/`gen2.h` and installed `gen2.sh`/`common.sh` match
the public files byte for byte. The experimental power-state fallback
remains confined to the LTS preparation copy. No GPU operations were run
while reviewing the saved evidence, and unchanged code was not rebuilt.

## Comparison with the UEFI Common Clock experiment

**Verified by source inspection:**
[CaiNiaoLaiLe/40HX-Unlock-Pve-Uefi-Grub](https://github.com/CaiNiaoLaiLe/40HX-Unlock-Pve-Uefi-Grub/tree/a11ae44ba030c21a95780fecfa9d3d1f860cb219)
at `a11ae44ba030c21a95780fecfa9d3d1f860cb219` has a different retrain
sequence in `src/main.c:apply_gen2()`. Its five BAR0 transformations are
equivalent to this project's `gen2_policy=rmw`:

- OVR becomes `6`.
- CFG changes only bits 19:18 to `2`; bit 22 is preserved.
- PL changes only bits 18:17 to `2`.
- MISC clears bits 12 and 14 and sets bits 11 and 13. Its expression
  `(value & ~0x5000) | 0x2800` equals our masked replacement of `0x7800`
  with `0x2800`.
- CYA clears bit 2.

Applying those transformations to Scirese's input still produces
CFG `80485800` and PL `00350036`. Different complete CFG/PL values in the
UEFI report therefore do not identify an extra register fix to transplant.
Its direct firmware PCI accesses also run before Linux caches the capability
version, so the Linux v1 accessor boundary does not apply there.

There is a separate register difference in `src/core_40hx.c:write_full_set()`:
the compute stage writes BAR0 `0x88610 = 0x1001` before SBR. Its source
comment describes matching an RTX 2070 value. **Verified by my prior tests:**
I repeatedly tested this Gen3-enabling register, including `0x1001`, without
an effect on CMP40HX. I confirmed these prior results on 2026-11-08; their
raw logs were not reviewed in this session.
The current collector does sample `0x88610` under the name `HIER`: it was
`00000001` before and after the successful GRID Gen2 run. This corrects the
earlier statement that the offset was absent from the capture. Repeating
the `0x1001` experiment is not part of the issue #8 plan.

The UEFI sequence waits 200 ms after policy application, sets TLS=2 and
HASD on both ends, clears ASPM, and first clears CCC on both ends. It sets
upstream HAWD and Retrain Link, clears bandwidth-status flags, and polls
both link speeds. On failure, its second round uses the upstream's original
CCC bit on both ends. If that bit was already zero, both rounds use zero.
The current CPU-RM helper waits 50 ms after policy and preserves CCC, ASPM,
HASD and HAWD while setting TLS and requesting ordinary Retrain Link.

The external repository reports an Intel Xeon E7 v4 IIO / C224 platform,
PVE 8.4 with kernel `6.8.12-43-pve`, and open NVIDIA 615.78.08 with GSP ON.
Its Gen2 work runs in UEFI after a compute/SBR sequence. This differs from
the local native GRID runtime control and from Scirese's X99/C612 host.

| Observation | Local GRID control | External UEFI report | Scirese's GRID report |
|---|---|---|---|
| Before training | CAP2 becomes `6`, TLS accepts `2` | CAP2 is `6`, TLS accepts `2` | CAP2 stays `2`, TLS check fails |
| CCC evidence | `1/1` before and after successful retrain | Initial GPU/root `0/1`; reports failure at `1/1`, success at `0/0` | No paired Link Control values supplied |
| Failure stage | No failure in this run | Retrain completes with link still Gen1 | Stops before Retrain Link |

**Hypothesis:** an explicit paired CCC test could help a platform where
CAP2 and raw TLS already confirm Gen2 but retraining leaves the link at
Gen1. The external report's successful `0/0` run supports that experiment;
it has not been independently reproduced here. Its failed `1/1` run also
means the explanation cannot be reduced to mismatched CCC bits alone.
The report does not measure reference-clock skew or establish that CCC=0
is required on other platforms. The local `1/1` success rules out that
requirement on this B550 control.

For issue #8, first resolve the earlier capability/TLS observation with
cached and raw reads. A CCC change cannot bypass the existing v1 guard or
Linux's cached-v1 accessor suppression. Whether a separate CCC problem
would appear after that boundary remains unknown. The UEFI report does not
justify clearing CFG bit 22 or copying its full reset/clock sequence into
the runtime GRID path.

Only its source and documentation were reviewed. `sha256sum -c CHECKSUMS.sha256`
in the clone exited `0` for all three EFI files; that checks them against
the repository's manifest, not against a fresh build or hardware execution.
The clone, installed boot configuration and GPU state were not changed.

## Related CMP50HX reports on Intel X99

[Issue #68](https://github.com/xrip/cmp50hx-unlock/issues/68) and
[issue #51](https://github.com/xrip/cmp50hx-unlock/issues/51) report a closer
match to Scirese's capability failure than the UEFI clock experiment.
Both use Intel X99/IIO platforms and CMP50HX with patched NVIDIA 610.43.03.
They observe Gen2 capability after Booter, followed by Gen1 capability after
GSP initialization. Scirese's GRID path has GSP disabled, and CMP40HX uses
TU106 rather than the CMP50HX's TU102. Similar register values support a
comparison of initialization stages, not a shared-cause conclusion.

### Issue #68: early targets/retrain, then policy restoration

The ASUS X99-DELUXE / i7-5930K report shows CAP2 `6` and PL `00340036`
after Booter, then CAP2 `2` and PL `00350036` at GSP-ready. The latter PL
value matches Scirese's failed post-policy sample. The proposed changes are:

1. Set TLS=2 on both ends and request an upstream retrain immediately after
   the post-Booter policy, before GSP-RM starts.
2. Once initialized, restore MISC, XP3G VAL0/OVR0, CYA, CFG and PL before
   the service retrains. This restoration deliberately omits another write
   of `6` to `0x8872c`, which that project calls the LTSSM adoption kick.

The early attempt itself logged `EARLY_RETRAIN_FAIL` and stayed at Gen1.
Gen2 capability nevertheless survived the following initialization. After
late policy restoration, the second late retrain reached Gen2 x16. Reported
pinned transfers were H2D 6.26 and D2H 6.71 GB/s, with no AER errors and
the link retaining Gen2 through a 45-second P8 idle interval and wake.

These are reported results from one cold boot with the early change plus
the live late-policy experiment. The report does not isolate the early TLS
writes from the early retrain event. Both visible and hidden BIOS speed
settings selected Gen2; their necessity was not tested. The early-patch
comment says the link must already run at 5 GT/s, but its recorded failure
at Gen1 shows that actual early Gen2 was not required in that run.

The late policy restoration has a local precedent: the 615.78.08 CPU-RM
test retained Gen2 capability through native initialization, but lost the
working MISC/CFG/PL settings and actual Gen2 speed. Restoring the policy
then retraining succeeded; see [the ordered result](CPU_RM_PCIE_GEN2.md#ordered-hardware-result).
The current CPU-RM helper already reapplies policy when the link is Gen1,
even if CAP2 or TLS advertises Gen2.

### Issue #51: hybrid code and a second initialization

The Machinist X99-MR9A Pro Max / Xeon E5-2678 v3 report uses three 20 GiB
CMP50HX cards. Its working combination restores older Gen2 patches and
performs driver unload, FLR, reload and retraining. It reports Gen2
x16/x16/x4 after the second initialization, with 32 GiB BAR1 on each card.
The attachment also changes ReBAR handling and retrain timing, so the
result does not isolate removal of the `ALREADY_GEN2` branch.

**Verified by source inspection:** the branch removed from
`s_cmp50ApplyGen2Policy()` returns from capability/policy setup when CAP
already advertises Gen2. The later driver function and boot service have
their own retrain paths. Thus the log text `ALREADY_GEN2` does not by itself
show that all retraining was skipped. The late service does skip policy
application when TLS is already at least 2, even while the actual link
remains Gen1; issue #68 restores that missing policy step separately.

The hybrid patch preserves ASPM, requests retraining on the endpoint and
upstream port, then retries once after a two-second observation window and
a 500 ms delay. Its comment reports worse results when ASPM was cleared.
This differs from the UEFI report's ASPM-off recipe. Neither report supports
a universal ASPM/CCC setting, and an endpoint Retrain Link write should not
be assumed to have the same meaning as the downstream-port request.

### Differences worth isolating on CMP40HX

**Verified by source inspection:** the reviewed CMP50HX sources at
`b630a493b7877fbd4472113dcb960a93749c0a78` set MISC, XP3G overrides, CYA,
CFG and PL before writing `6` to `0x8872c`. The CMP40HX 0.16.0 runtime
code used in the cold-boot comparison writes `0x8872c` first, followed by CFG, PL, MISC and CYA. It does not
write the CMP50HX helper's XP3G VAL0 `0x8e120=0` / OVR0 `0x8e110=1` pair.
The earlier UEFI clone also writes `0x8872c` first, so this ordering
difference comes from the CMP50HX sources.

**Hypothesis:** on an affected platform, capability adoption may depend on
the state at the `0x8872c` write or on policy seeded before native
initialization. A separate experiment with the same five writes and
`0x8872c` last would isolate ordering; additional XP3G writes would be a
different experiment, preceded by register inspection. Successful B550
runs do not establish whether either change is necessary on X99. The
CMP50HX comments' stronger claim of one adoption opportunity per power
cycle is not established for CMP40HX.

The numeric PL comparison also needs care:

| PL | Rate bits 18:17 | Bit 16 | Bit 20 | Bit 21 |
|---|---:|---:|---:|---:|
| `00340036` | 2 | 0 | 1 | 1 |
| `00350036` | 2 | 1 | 1 | 1 |
| `00240036` | 2 | 0 | 0 | 1 |
| `00140036` | 2 | 0 | 1 | 0 |

The reported `00340036` to `00350036` transition changes bit 16, not bit 20.
Both retain a rate field of 2 despite different capability outcomes. Bit 20
also remains set in the successful local sample after policy and before
retrain (`00140036`). These observations do not justify forcing either
bit or assigning it a lock meaning without further evidence.

For issue #8, raw PCI CAP2/CTL2 and the cached capability version still
need to be separated before interpreting a failed TLS write. Early policy,
early TLS and early retrain are distinct controls. The existing
`gen2=prepare` mode only applies policy; it does not explicitly program
both targets and therefore does not reproduce the early change in #68.
An eventual comparison should keep CCC/ASPM unchanged and avoid combining
ordering, XP3G writes and driver reload into one proposed fix.

**Verified:** both `gh issue view` requests returned the issue bodies with
empty comment lists. The attachment downloaded from #51 has SHA-256
`7e77addbf5131b68052921a030b28ecc0470b6934665764b7753a35dab4b8943`, matching
the report. The read commands were:

```bash
gh issue view 68 --repo xrip/cmp50hx-unlock --json title,body,comments,url,state
gh issue view 51 --repo xrip/cmp50hx-unlock --json title,body,comments,url,state
sha256sum artifacts/issue8/cmp50-x99-2026-11-08/cmp50hx-hybrid-minimal.patch
```

All exited `0`. The saved issues, attachment, three upstream source files
and their hashes are under `artifacts/issue8/cmp50-x99-2026-11-08/`.
`sources.json` records the source revision and the PL bit decoding. These
patches were read without applying, building or running them; no CMP50HX
hardware test was performed. The register-order and early-initialization
hypotheses concern the affected CMP40HX host.

## Next controlled checks

- Validate native GRID Gen2 with pinned transfers and a numerical workload,
  then compare separate vGPU-module and manager stages.
- Compare cached `pcie_flags_reg`, live FLAGS and raw PCI CAP2/CTL2 with
  their BAR0 mirrors, before and after policy application. Keep the device
  bound and perform these comparisons with reads only.
- Establish when v1 and CFG bit 22 first appear: enumeration, pre-driver
  compute cleanup, native NVIDIA load, vGPU-module load or manager startup.
- On an affected host, isolate policy ordering with the same five writes
  and `0x8872c` last. Keep added XP3G writes and early TLS/retrain in separate
  controls; compare raw capability and TLS before requesting retrain.
- Compare separate cold boots with Gen2 off versus pre-driver `prepare`,
  keeping GRID, guests and the later helper identical. Distinguish this
  policy-only mode from the early target/retrain sequence in CMP50HX #68.
- Decode CCC and ASPM on both ends in the same samples. Consider a paired
  CCC experiment only after raw capability/TLS checks pass and an ordinary
  retrain still leaves Gen1; preserve the original values and separate it
  from HASD/HAWD changes.
- Independently reproduce the guest measurements with paired stock/FULL and
  numerical controls, then exercise guest/manager lifecycle transitions.

CPU-RM 0.17.0 and the refreshed open patches now use a
[shared Gen2 engine](CPU_RM_PCIE_GEN2.md#shared-gen2-engine-0170). It reads
live capability independently of Linux's cached version, permits policy
application from live v1, and requires live v2 before TLS writes. It adds
`gen2=targets` for the early TLS comparison. I tested the new engine on
hardware and confirmed successful operation.
Kernel PCI caches, CFG bit 22 and Link Disable remain unchanged. See [the B550 Gen2 report](CPU_RM_PCIE_GEN2.md)
and [Link Disable audit](PCIE_LINK_DISABLE_AUDIT.md) for the tested boundaries.
