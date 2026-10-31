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
distribution's module-blacklist mechanism. For dracut, both
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

## Boot ordering and removal

Persist CPU-RM selection in a modprobe configuration if desired:

```text
options nvidia NVreg_EnableGpuFirmware=0
```

Automatic unlock must run **before the first NVIDIA probe**, with this module,
its firmware and any SHA-256 crypto dependency available. A
`softdep nvidia pre: cmp40_preboot` alone does not check `unlock_error` and
does not guarantee ordering when NVIDIA is loaded from an initramfs. Use
the guarded helper in your early-boot configuration, and include its shell,
modprobe/modinfo, firmware and crypto dependencies in the initramfs if needed.
Distribution-specific automatic setup is not installed by this repository.

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
