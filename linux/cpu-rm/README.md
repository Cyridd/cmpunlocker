# CMP 40HX proprietary Linux driver / CPU-RM unlock

This path unlocks compute on an **8 GiB CMP 40HX / TU106 (`10de:1f0b`)**
before the proprietary NVIDIA driver loads, with **GSP disabled**. It does not
modify or install NVIDIA's proprietary driver. Native initialization and CUDA
throughput were validated with **610.43.03 and 615.78.08**; see
[the research report](../../docs/research/CPU_RM_SMALL_WPR.md).

The existing [open-module installer](../../docs/linux-install.md) remains a
separate choice. Its PCIe/ReBAR/profiling patches are not applied by this tool.
GRID/vGPU, other CMP models, suspend/resume and other driver releases have not
been independently validated for this CPU-RM path.
The [GRID 19.6 community report](../../docs/research/GRID_CPU_RM_REPORT.md)
documents compute unlock on one 580.178.05 host, Linux guest CUDA/Tensor Core
benchmarks and Windows guest OpenCL benchmarks without additional guest-driver
changes. About half an hour of repeated clpeak runs reportedly produced no
noticed instability or driver errors. Gen2 remains unresolved there (Gen1 x8).

## Prepare and build

Requirements: x86-64 Linux, matching kernel headers and compiler, Make,
Python **3.11+**, `zstd`, and a separately installed matching proprietary NVIDIA
driver/userspace stack. Secure Boot requires signing `cmp40_preboot.ko` using
your distribution's trusted module-signing process, or disabling Secure Boot.
The module has been built on kernel `7.2.9-2-cachyos`; CI also builds against
Ubuntu's distribution headers. Rebuild and reinstall after kernel updates.

From the repository root, as a normal user:

```bash
python3 linux/cpu-rm/firmware/prepare.py
make -C linux/cpu-rm/kernel
make -C linux/cpu-rm offline-test
python3 -m unittest discover -s linux/cpu-rm/firmware -p 'test_*.py'
```

For a kernel built with Clang/LTO, such as CachyOS, use
`make -C linux/cpu-rm/kernel LLVM=1`. For a different kernel, specify
`KDIR=/lib/modules/<kernel-version>/build`.

Firmware preparation downloads the pinned NVIDIA 610.43.03 source archive and
a pinned firmware package into `build/inputs`, checks their SHA-256 values,
then extracts only the required firmware into `build/assets`. Nothing from
the package is installed or executed. The helper uses the same published MIT
selector payload as the Windows EFI; a ZERO control changes only its two
selector operands. NVIDIA firmware keeps its own terms; the extracted license
is included alongside the assets. The 610 Booter is an unlock input even when
the separately installed driver is 615; do not replace the driver's own
firmware or userspace with these assets.

An existing cache can be used with
`--cache /path/to/inputs --offline`; `--output /path/to/assets` selects the
output folder. Downloads and generated files are ignored by Git.

Install only the pre-driver module and its firmware:

```bash
sudo install -d /lib/firmware/cmp40-preboot/610.43.03
sudo cp linux/cpu-rm/build/assets/* /lib/firmware/cmp40-preboot/610.43.03/
sudo install -d /lib/modules/"$(uname -r)"/extra
sudo install -m 0644 linux/cpu-rm/kernel/cmp40_preboot.ko /lib/modules/"$(uname -r)"/extra/
sudo depmod -a
```

## Run before NVIDIA loads

Use a cold boot where the CMP is **unbound**, and use another GPU or a text
console for the host. A temporary boot entry can blacklist `nvidia`,
`nvidia_drm`, `nvidia_modeset`, `nvidia_uvm` and `nouveau` using your
distribution's module-blacklist mechanism. On GRID/vGPU hosts, also blacklist
`nvidia_vgpu_vfio`: its matching PCI alias and `softdep pre: nvidia` can load
NVIDIA despite a blacklist on `nvidia` alone. For dracut, both
`rd.driver.blacklist=...` and `modprobe.blacklist=...` may be needed. If NVIDIA
is explicitly included in your initramfs, adjust that image as well. Verify
the state before continuing; the helper will refuse a driver-owned device.

Find the full PCI address with `lspci -Dnn -d 10de:1f0b`, then substitute it
for the example below:

```bash
readlink /sys/bus/pci/devices/0000:01:00.0/driver  # must have no driver link
sudo bash linux/cpu-rm/unlock.sh --gpu 0000:01:00.0 --load-nvidia
nvidia-smi
sudo dmesg | rg 'cmp40_preboot|NVRM'
```

The helper does not detach/reset an initialized GPU. It loads `cmp40_preboot`
in `mode=unlock`, explicitly selects the tested stock handoff and 1 MiB WPR
heap, checks `unlock_error=0` and `resources_retained=N`, then loads the
proprietary `nvidia` module with `NVreg_EnableGpuFirmware=0`. Omit
`--load-nvidia` to stop after the pre-driver unlock. If `cmp40_preboot` is
already loaded, inspect its parameters and logs before another operation.
The GSP check accepts `/sys/module/nvidia/parameters/NVreg_EnableGpuFirmware`
or `/proc/driver/nvidia/params`; some GRID builds expose only the latter.

For a GRID host, keep the vGPU manager and guests stopped through this sequence.
After native health and GSP OFF are confirmed, explicitly load the separate
vGPU module before starting the manager and guests:

```bash
sudo modprobe nvidia_vgpu_vfio
```

This ordering addresses the reported early-driver-load problem. GRID and
vgpu-unlock must be installed separately; the scope of the reported guest
benchmarks is recorded in [the GRID report](../../docs/research/GRID_CPU_RM_REPORT.md#guest-benchmarks-and-stability).

Expected pre-driver markers:

```text
CPU-RM handoff verified: FECS_PLM=0xffffff8f ... DMEM=zero WPR2=0:0x1ffffe00
PRE_DRIVER_UNLOCK_PASS
```

These markers establish firmware completion. Confirm native driver operation
with `nvidia-smi`, GSP Firmware `N/A`, and an independent CUDA benchmark such
as clpeak (WMMA FP16/INT8). Successful `insmod` alone is insufficient:
if firmware still owns DMA buffers, the module deliberately stays resident
while reporting a nonzero `unlock_error` and `resources_retained=Y`.
**In that state, power off before loading a GPU driver.** The helper checks
this condition and stops.

For a read-only inspection on an active GPU, with no existing preboot module:

```bash
sudo modprobe cmp40_preboot target=0000:01:00.0 mode=inspect
sudo dmesg | rg 'cmp40_preboot'
sudo modprobe -r cmp40_preboot
```

`inspect` never performs the unlock or wakes a suspended GPU. Experimental
SEC2/loader controls remain in the source for reproducing the research;
they are not part of the installation procedure.

## Automatic unlock with mkinitcpio

The optional [cmp40-unlock install hook](mkinitcpio/cmp40-unlock) runs compute
unlock inside a systemd-based initramfs, before module loading and udev. It
selects exactly one unbound `10de:1f0b`, uses the stock handoff and 1 MiB WPR
heap, and leaves `gen2=off`. NVIDIA loads later through the normal boot path.

I tested this hook on my CachyOS system with proprietary NVIDIA 615.78.08
and GSP OFF. Automatic compute unlock worked after boot.

Install the module and prepared firmware as described above for every kernel
you boot. Keep `base`, `systemd` and `modconf` in `HOOKS`, and place
`cmp40-unlock` immediately after `modconf`, after `autodetect` when present.
Do not put `cmp40_preboot` in `MODULES`: the service must load it with explicit
unlock parameters. Keep NVIDIA modules out of this initramfs as well.

Persist GSP OFF before enabling automatic unlock. For example, create
`/etc/modprobe.d/cmp40-cpu-rm.conf` with:

```text
options nvidia NVreg_EnableGpuFirmware=0
```

From the repository root:

```bash
sudo install -Dm644 linux/cpu-rm/mkinitcpio/cmp40-unlock /etc/initcpio/install/cmp40-unlock
echo 'HOOKS=(${HOOKS[@]/modconf/modconf cmp40-unlock})' | sudo tee /etc/mkinitcpio.conf.d/20-cmp40-unlock.conf
sudo mkinitcpio -P
```

The drop-in inserts the hook after an existing `modconf` entry. If you already
added it directly to `HOOKS`, use that entry instead of the drop-in. Preserve
any hooks your storage or encryption setup needs.

Inspect each generated image before rebooting. Use the image path printed by
mkinitcpio or configured by your bootloader, rather than assuming
`/boot/initramfs-*.img`. A Limine setup can use
`/boot/<machine-id>/<kernel-name>/initramfs`.
If a bootloader tool maintains a separate copy, use its normal update command
and inspect the image selected by the boot entry.

```bash
lsinitcpio /path/to/initramfs | rg 'cmp40|nvidia'
```

The output should include `cmp40_preboot.ko` (possibly compressed), the
`cmp40-preboot/610.43.03/` firmware files, `cmp40-initrd-unlock` and
`cmp40-unlock.service`. It should contain no `nvidia*.ko` files. If NVIDIA
modules appear, remove their entries from `MODULES` or custom hooks and
check whether `kms` includes them, then rebuild and inspect the image again.
The install hook includes `cmp40_preboot` and its module dependencies without
adding it to the automatic module-load list.

After a cold boot:

```bash
journalctl -k -b | rg 'cmp40|NVRM'
cat /sys/module/cmp40_preboot/parameters/{unlock_error,resources_retained}
nvidia-smi
```

The log should show `PRE_DRIVER_UNLOCK_PASS` and
`pre-driver unlock completed on <GPU>; verify with nvidia-smi after boot`
before NVIDIA's module-load message. The two parameter files must print `0`
and `N`. Confirm GSP Firmware `N/A` with `nvidia-smi -q` and run a CUDA workload
to check native operation; the firmware completion markers alone do not prove
workload stability.

### Skips, failures and removal

A missing per-kernel module or a missing or empty firmware file produces a
build warning and omits the unlock from that image. Rebuild and reinstall the
module after kernel updates, then regenerate the initramfs. At boot, the
wrapper skips an absent or ambiguous CMP, a driver-owned card, or an already
loaded NVIDIA/preboot module without attempting unlock.

Before an attempt, the wrapper creates
`/run/modprobe.d/cmp40-block-nvidia.conf` to block normal modprobe loads of
NVIDIA, the vGPU module and nouveau. It removes that guard only after reading
`unlock_error=0` and `resources_retained=N`. A failed or interrupted attempt
leaves the guard installed. Power off completely before retrying; do not
remove the guard or force a GPU driver to load during that boot.

For a one-boot bypass, add `cmp40.skip` to the kernel command line. To remove
the integration, remove the `cmp40-unlock` HOOKS entry or its
`20-cmp40-unlock.conf` drop-in and `/etc/initcpio/install/cmp40-unlock`, then
regenerate the initramfs. Keep GSP OFF if you still use the manual CPU-RM path.

## PCIe Gen2 with CPU-RM

Gen2 can be applied entirely **after native NVIDIA initialization**. A cold-boot
control with no preliminary Gen2 writes reached Gen2 x16 using the CPU-RM helper.
This was measured on CMP40HX VBIOS `90.06.67.00.06`, MSI B550 and proprietary
NVIDIA 615.78.08; see [the hardware report](../../docs/research/CPU_RM_PCIE_GEN2.md).

After installing the rebuilt module and firmware, start from an unbound cold
boot and run this order, replacing the example PCI address:

```bash
sudo bash linux/cpu-rm/unlock.sh --gpu 0000:01:00.0 --load-nvidia
sudo modprobe -r cmp40_preboot
sudo bash linux/cpu-rm/gen2.sh --gpu 0000:01:00.0
```

After a successful mkinitcpio unlock, verify `unlock_error=0` and
`resources_retained=N`, then start at `sudo modprobe -r cmp40_preboot` in this
sequence. The compute unlock has already run; do not invoke `unlock.sh` again.

The first helper verifies compute unlock and firmware/DMA cleanup. Unload its
completed one-shot module before using it again for runtime Gen2. The Gen2
helper verifies proprietary GSP OFF, native health and absence of open NVIDIA
clients, then holds a temporary NVML client while the module sets the GPU-side
policy and requests ordinary upstream retraining. It additionally requires
full compute selectors, halted GSP and an active PCIe link. Its runtime mode
uses existing PCI decoding and leaves the native driver's PCI enable count
unchanged.

`--module /path/cmp40_preboot.ko` on the Gen2 helper selects a local matching
build instead of the installed module. An already active Gen2 link completes
with reads only. Check live `LnkCap/LnkCap2/LnkSta`, native health and a DMA or
compute workload afterward. Maximum-speed reports can remain stale after the
capability changes: the test retained a sysfs `max_link_speed` of 2.5 GT/s even
while actual transfers used Gen2 x16 at about 6.8 GB/s.

The helper is explicit and one-shot; no automatic service is installed or
enabled. Other cards, driver versions, NVIDIA reinitialization and
suspend/resume need separate validation. Automatic integration must apply
Gen2 after the first native NVIDIA initialization, before GPU clients start.

### Pre-driver research controls

`--gen2 prepare` and `--gen2 retrain` on `unlock.sh` remain available for
controlled cold-boot comparisons. Their default policy changes only the known
rate, MISC and `DIS_G2` fields and sets XVE override `6`; the explicit
`--gen2-policy windows` variant writes the Windows full MISC/CYA values.
Pre-driver training reached Gen2, but native CPU-RM reset the policy to Gen1.
The subsequent cold control established that late Gen2 works from the stock
Gen1 capability state without preliminary PCIe preparation.

Early preparation remains an optional comparison for other platforms,
including the Gen2 training reports in
[discussion #10](https://github.com/Cyridd/cmpunlocker/discussions/10) and the
[Alder Lake audit](../../docs/research/PCIE_LINK_DISABLE_AUDIT.md). An early
policy setup could affect native initialization or capability sampling;
neither report has yet been fixed by this experiment. The
[hardware report](../../docs/research/CPU_RM_PCIE_GEN2.md#early-preparation-on-other-platforms)
describes the cold-boot controls. The reported open/GSP path is a separate
comparison from this proprietary GSP-OFF helper.

Gen2 outcomes are recorded separately in `gen2_error`, `gen2_device_ok` and
`gen2_link_status`; compute unlock has its own `unlock_error`. A Gen2 timeout
never enters the Booter FLR error path. Failed training restores the original
TLS fields on both ends; GPU-side policy remains available for diagnosis.
All Gen2 modes use the actual upstream port and ordinary Retrain Link, without
Link Disable or driver reprobe. The existing
`tools/cmp40hx-driver-reprobe-gen2.sh` depends on the patched open driver's
Gen2 setup running again at probe time, so use the CPU-RM helper for this path.

## Boot ordering and removal

Persist CPU-RM selection in a modprobe configuration if desired:

```text
options nvidia NVreg_EnableGpuFirmware=0
```

Automatic unlock must run **before the first NVIDIA probe**, with this module,
its firmware and any SHA-256 crypto dependency available. A
`softdep nvidia pre: cmp40_preboot` alone does not check `unlock_error` and
does not guarantee ordering when NVIDIA is loaded from an initramfs. Use
the [mkinitcpio hook](#automatic-unlock-with-mkinitcpio) for the supported
systemd initramfs setup. Other initramfs implementations need their own guarded
early-boot integration, including the shell, modprobe/modinfo, firmware and
crypto dependencies. No automatic setup is enabled by default.

The Windows EFI's `--return-to-bootloader` option is retained as another
pre-OS route, but native Linux acceptance here was measured with this Linux
module. Do not apply both paths in the same boot.

To stop using the unlock, boot without the helper, remove its module from
`/lib/modules/<kernel-version>/extra/`, remove only
`/lib/firmware/cmp40-preboot/610.43.03/`, undo your boot/modprobe configuration,
run `depmod -a`, and rebuild any affected initramfs. A power cycle clears
the hardware unlock. Leave the installed NVIDIA driver files intact.

## Attribution

The GPL-2.0-only pre-driver module and firmware tooling derive from
[nlzy/turing-unlock](https://github.com/nlzy/turing-unlock), commit
`417f333263ff169e8f449c931b70db9dfd7b8ef4`, with CMP40HX-specific parsing,
asset verification, handoff and small-WPR changes by Cyridd. The selector
payload is MIT; NVIDIA source assets and binary firmware keep their respective
terms. License copies are in [licenses/](licenses/), and the extracted NVIDIA
firmware notice accompanies the prepared assets.
