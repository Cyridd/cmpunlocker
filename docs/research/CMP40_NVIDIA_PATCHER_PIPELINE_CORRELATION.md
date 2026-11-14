# NVIDIA-patcher Device ID table and the Vulkan pipeline throttle

Verified: [NVIDIA-patcher](https://github.com/dartraiden/NVIDIA-patcher)'s CMP
Device ID replacement changes the input to the RM response that enables
glcore's Vulkan pipeline-bind delay. Static inspection found the same path in
stock 610.43.03, 615.78.08 and 620.70.06. On proprietary 615.78.08 with GSP
OFF, clearing the response's second byte removed the measured delay. The
620.70.06 kernel comparison returned `01 01` on stock RM and `00 00` after
restoring the NVIDIA-patcher kernel, with Device ID `1f0b` throughout.
The hardware selector changed physical identity from `1f0b` to `1f4b`.
Stock 620.70.06 initialized with that identity and returned `00 00`. The
subsequent A1/B/A2 comparison removed the pipeline-bind delay, completed
offscreen graphics workloads and restored the original identity. CUDA device
discovery failed under `1f4b` and worked again after restoration. This
selector does not produce RTX 2060 SUPER ID `1f06`.

## What the script changes

The inspected [Linux script](https://github.com/dartraiden/NVIDIA-patcher/blob/master/linux.sh)
(accessed 2026-11-14) extracts `NVIDIA-Linux*.run` and patches
`kernel/nvidia/nv-kernel.o_binary` after making a backup. Its CMP pattern
changes the little-endian CMP 40HX entry from `0x00071f0b` to `0x0007ffff`.
It also replaces other CMP, P1xx and several engineering SKU IDs. The glcore
library is unchanged.

Static inspection found one CMP pattern match in each stock kernel object:

| Driver | CMP pattern file offset | CMP40 entry file offset |
| --- | ---: | ---: |
| 610.43.03 | `0x6560b6c` | `0x6560b7c` |
| 615.78.08 | `0x5fe26f0` | `0x5fe2700` |
| 620.70.06 | `0x54a54f0` | `0x54a5500` |

The affected reader walks four-byte entries, compares their low 16-bit Device
IDs, and sets a GPU-object Boolean on a match. Applying the script's exact
CMP replacement to a copy of the table in memory removed its sole
`0x1f0b` match in all three releases.

The upper 16-bit entry fields and the Boolean's proprietary name remain
unidentified.

## RM response and glcore emission

| Stage | 610.43.03 | 615.78.08 | 620.70.06 |
| --- | --- | --- | --- |
| Table reader | `_nv030186rm` | `_nv031055rm` | `_nv026762rm` |
| `.rodata` table range, end exclusive | `0x55f10a0–0x55f1588` | `0x5011a20–0x5011f0c` | `0x44941c0` to `0x44946ac` |
| GPU-object match Boolean | `+0x9be` | `+0xa34` | `+0xaf3` |
| GPU-object inversion Boolean | `+0x9bf` | `+0xa35` | `+0xaf4` |
| RM response handler | `_nv017683rm` | `_nv018387rm` | `_nv013961rm` |
| Response registration in `.rodata` | `+0x56b0160` | `+0x50dae40` | `+0x4556b10` |
| glcore query instruction | `0xfc0e2d` | `0xfcb5f5` | `0xfdd00a` |
| glcore response-to-flag store | `0xfc0e67` | `0xfcb62f` | `0xfdd044` |
| glcore policy byte | `+0x213` | `+0x213` | `+0x21b` |
| Classic bind emitter's flag check | `0xb2084b` | `0xb2279b` | `0xb27ceb` |
| Slot-52 constant file offsets | `0xb2085d`, `0xdcbb70` | `0xb227ad`, `0xdae680` | `0xb27cfd`, `0xdbf780` |

The RM export table registers `0x20810107` with a two-byte response and the
handler listed above. All three handlers implement:

```text
response[0] = device_table_match
response[1] = device_table_match
if inversion_boolean != 0:
    response[1] = !device_table_match
```

All three glcore releases issue that command and, on success, set the internal
policy byte listed above from whether `response[1]` is nonzero. The classic bind
emitter checks this byte and skips its `CALL_MME_MACRO(52), argument 240`
command pair when it is zero.

The result depends on the inversion Boolean. Every original RM response in the
615 response-byte experiment was `01 01`, which means the Device ID matched
and inversion was inactive. In that state, the patcher's table change
predicts `00 00` and disables emission. The inversion state in the earlier
610 installation was not measured.

## Response-byte experiment

Verified: the experiment used CMP 40HX with proprietary 615.78.08, GSP OFF,
the pre-boot compute unlock and Gen2 x16. The benchmark was
`vk_shader_bind_matrix` with stock glcore. A local `LD_PRELOAD` diagnostic
called the real ioctl and logged its successful two-byte `0x20810107`
response, then changed selected response bytes in the benchmark process.
It forwarded the original requests to RM.

Each main result is the median of three process medians, each from seven GPU
timestamp submissions. Both cases bind pipelines without dispatch:

| Response returned to glcore | Same pipeline, 1000 binds, GPU ms | Alternating two pipelines, 1000 binds, GPU ms |
| --- | ---: | ---: |
| Original `01 01` | 138.633408 | 138.654720 |
| First byte cleared: `00 01` | 138.643840 | 138.667264 |
| Second byte cleared: `01 00` | 0.000032 | 0.000032 |
| Both cleared: `00 00`, one process per case | 0.000032 | 0.000032 |

Each of the twenty processes logged one successful matching query with an
original response of `01 01`. Clearing the second byte removed the delay;
clearing the first byte left it intact. This isolates the second response byte
as the input that enables the measured throttle.

The 32 ns result is the timestamp floor of these bind-only command buffers.
Disabling this policy can let the driver defer pipeline programming until
work needs it. Pipeline execution, game FPS and modified pushbuffers were
not measured or captured in this experiment.

## Verification limits

Verified: GSP remained N/A before and after the runs, the link remained Gen2
x16, and the kernel journal gained no new lines. Hash checks confirmed
that the stock kernel/glcore artifacts, benchmark and system glcore hashes
were unchanged. The preload was limited to the benchmark processes.

The response-byte experiment covers 615.78.08; the equivalent 610.43.03 and
620.70.06 paths were inspected statically. The 620 stock/restored kernel
comparison and the `1f4b` native run below confirm the RM response
transitions. A kernel comparison on the earlier releases remains untested.
Game performance was not measured in these experiments.

## Pre-driver integration

Verified: the table is in the NVIDIA kernel object's `.rodata`, and
`_nv031055rm` initializes the GPU-object byte at `+0xa34` to zero, then sets it
from the Device ID lookup. The corresponding 610 fields are listed above.
This byte is driver memory, not an MMIO register that the Booter payload can
set before the driver loads. The current pre-driver unlock changes compute
selectors, FECS permissions and WPR state; it does not change this RM table.

Verified, static inspection: the 615 ID readers `_nv030398rm`, `_nv030396rm`
and `_nv030397rm` read configuration offsets `0x08`, `0x2c` and `0x00` through
GPU callbacks, then store the offset-zero result at GPU-object `+0xd94`.
The throttle lookup reads its upper 16 bits at `+0xd96`. Which callback path
the current board selects was not traced at runtime. The Linux probe also
copies `pci_dev->device` into `nv->pci_info.device_id` in `nv-pci.c`.

The published Turing `dev_nv_xve.h` describes `NV_XVE_ID` as read-only
(`R--4R`). The driver helper `_nv052343rm` sets the inversion byte at `+0xa35`
with a software constant among other object properties. Its selecting caller
and a firmware override for this property remain unresolved.

Verified, historical capture inspection: application initialization programs
MME slot 52 with instruction address `0x128` and uploads the twelve DWORDs of
the known delay program. Assumed: rewriting only that macro before the driver
loads would not persist through this later upload.

Assumed: with the same Device ID, inversion state and unmodified RM table,
the present GPU-only pre-driver payload cannot disable this software policy.
This conclusion covers the traced path; it does not rule out an undiscovered
firmware input to Device ID or property selection.

| Approach | Status | Integration requirement |
| --- | --- | --- |
| Local glcore argument-0 patch or process-local RM response override | Verified | The preceding experiments remove the bind-only delay; the override runs with the application. |
| Patch CMP40's RM table in the driver | Verified for the 620 response | Stock RM returned `01 01`; the restored NVIDIA-patcher kernel returned `00 00`. The table must be patched before RM's lookup. GPU timing in this kernel comparison was not measured. |
| Override the response inside the loaded driver | Hypothesis | A separate hook must target CMP40 and successful two-byte `0x20810107` responses before glcore caches the result. It has not been implemented. |
| Change Device ID through the XVE selector | Verified for 620 initialization, graphics timing and restoration | Selector one produced `1f4b`, survived stock-RM initialization and Vulkan enumeration, and returned `00 00`. The OS cache stayed `1f0b`; separate runtime-PM recovery restored all identities to `1f0b`. The A/B/A comparison removed the bind delay and completed offscreen rendering; CUDA discovery failed under `1f4b`. Pre-enumeration application and Windows remain untested. |

A driver response hook could be a separate step after `--load-nvidia` and
before applications start. It would intervene in the running driver while
leaving the installed NVIDIA files intact. The Linux ioctl frontend forwards
requests to `rm_ioctl`, but a filter there would also need to resolve the
request's GPU handle, preserve error responses and reject unsupported driver
layouts. The tested userspace interposer establishes the effect of changing
the byte; it does not establish a working kernel hook.

The table edit changes both response bytes when inversion is inactive.
Verified: clearing only the second byte was sufficient in the process-local
experiment. Hypothesis: a CMP40-specific response override could narrow the
bypass to this query while preserving the first byte. Other effects of the
Device ID table edit and other consumers of either response byte were not
tested.

The glcore helper works with the original identity. The measured selector
alternative removes the policy with stock RM but loses CUDA discovery in
620.70.06. The compute-unlock payload and pipeline policy remain separate
mechanisms.

The earlier pre-driver feasibility assessment used static disassembly and
historical MME-upload captures. The later selector experiment below supplies
hardware evidence for a temporary Device ID change. Windows driver behavior
and a kernel response hook remain untested.

## 620.70.06 Device ID override investigation

The proposed identity is CMP40 `10de:1f0b` replaced by RTX 2060 SUPER
`10de:1f06`, following the [bootloader suggestion in NVIDIA-patcher issue
#618](https://github.com/dartraiden/NVIDIA-patcher/issues/618#issuecomment-6729179022)
(accessed 2026-11-14).
The primary objective is the Linux pipeline policy and, eventually, Windows
3D rendering. The extended Linux protocol also checks graphics execution,
compute correctness, shader throughput and memory transfers. These are
controls for other effects of the identity change; no compute, RT or PCIe
unlock from Device ID alone has been established.

Verified, static inspection on 2026-11-14: the 620 table contains 315
four-byte entries. The reader preloads the first ID, `0x01f8`, then reads
entries from `.rodata+0x44941c4` up to the exclusive boundary `0x44946ac`.
There is one `0x1f0b` entry, at `+0x4494680`, and no `0x1f06` or `0x1f4b` entry.
Replacing the CMP entry in a memory copy leaves zero `0x1f0b` matches.

Verified for `0x1f4b`: the native run below retained that ID and returned
`00 00`. The inspected lookup predicts the same response for `0x1f06` with
inversion inactive, but initialization with `0x1f06` has not run.
The A/B/A timing comparison below confirms removal of the delay under
`0x1f4b`. CUDA discovery failed in that state. Other Device ID consumers
and whole-driver compatibility remain untested.

### The identity source includes BAR0

Verified, source inspection: 620's generated TU106 HAL selects
`gpuGetIdInfo_GM107`, `gpuReadBusConfigReg_GM107` and
`gpuReadBusConfigRegEx_GM107`. In
`src/nvidia/src/kernel/gpu/arch/maxwell/kern_gpu_gm107.c`, ID initialization
reads configuration offsets `0x08`, `0x2c` and `0x00` through
`GPU_BUS_CFG_RD32`. Its register reader performs
`GPU_REG_RD32_EX(pGpu, DEVICE_BASE(NV_PCFG) + index, ...)`.
The published TU102 `dev_nv_xve.h` defines `NV_PCFG` at BAR0 offsets
`0x88000` through `0x88fff` and `NV_XVE_ID` at offset zero as `R--4R`.

Verified, proprietary binary inspection: `_nv025993rm` stores the
offset-zero DWORD at GPU-object `+0xe64`. `_nv026762rm` compares its upper
word at `+0xe66` against the Device ID table. The callback chain through
`_nv026629rm` and `_nv026627rm` reads BAR0 at `0x88000 + index`.
The constructor installs those callbacks, but their selection on the live
board has not been instrumented.

The Linux wrapper also caches `pci_dev->device` in `nv->pci_info.device_id`
at probe. Its slot-reset recovery compares the PCI configuration identity
with the kernel's cached vendor/device pair and rejects a mismatch.

Verified, static inspection: the generic PCI mirror writer `_nv026818rm`
accepts DWORD offsets through `0xffc`, adds `0x88000`, calls the register-write
helper `_nv049512rm`, and returns zero without reading the register back.
The function-specific writer `_nv026819rm` selects `0x88000` for function
zero and `0x8a000` for function one. The open-source
`gpuWriteBusConfigReg_GM107` also writes `NV_PCFG + index` and returns
`NV_OK` without readback. These helpers support writes to configuration
registers; their success status does not establish that the read-only
identity DWORD accepts writes.

Verified, source inspection: `gpumgrSetAttachInfo` in
`src/nvidia/src/kernel/gpu_mgr/gpu_mgr.c` assigns `PCIDeviceID` from `socId`
inside the `bIsSOC` branch, described in the source as the ARCH MODS iGPU
verification path. This is a software-object assignment in a separate
attachment path. It provides no demonstrated hardware override for CMP40.

Verified, binary and source inspection: the store at `.text+0x2355d0` in
`_nv044869rm` targets offset `+0xe64` in a different object. Its receiver
comes from `_nv005817rm` using class descriptor `_nv002994rm`, which
contains object size `0x16f8` and NVOC class ID `0x272cdd`.
`_nv005817rm` reads the descriptor's class ID at offset four and calls the
dynamic-cast helper. The generated source gives `OBJGPU` class ID
`0x7ef3cb`. The matching field offset therefore does not identify a GPU
Device ID assignment. The proprietary class name remains unresolved;
this store is excluded from the identity-override candidates.

Assumed: changing only an OS-visible ID or the Linux `pci_dev` cache would
leave the traced BAR0 identity source intact. A hardware override needs to
reach the mirror before RM caches it and survive native initialization. For
a consistent OS identity, it also needs to precede PCI enumeration or be
followed by a controlled enumeration step. Persistence across reset and
resume would need separate tests.

### Read-only diagnostics

[`tools/devid/read_identity.py`](../../tools/devid/read_identity.py) reads the
kernel's cached PCI identity, the real PCI configuration DWORD and the BAR0
mirror. It accepts only NVIDIA `1f0b`/`1f4b`/`1f06` with BOOT0 `166000a1`, requires
active runtime power and PCI memory decoding, and repeats identity reads to
reject a changing snapshot. It performs no register writes, wakeups or
driver operations. A mismatch is reported in JSON, not treated as a
successful override.

[`tools/devid/observe_rm_policy.c`](../../tools/devid/observe_rm_policy.c) is
an x86-64 Linux `LD_PRELOAD` observer for Vulkan/RM clients. It forwards each
ioctl before logging successful, direct `NV_ESC_RM_CONTROL` requests for
`0x20810107` with a two-byte response. It preserves errno and the response.
Extended ioctl wrappers are not decoded. The log does not resolve GPU
handles, so multiple NVIDIA GPUs require separate attribution. Use it for
policy observation; its logging overhead is unsuitable for CPU timing.
The interposer expects a third ioctl argument. Its verified client is
`vulkaninfo --summary`.

Build from the repository root against a fresh NVIDIA 620.70.06 open source
tree. Set `cmp_open_source` to its location; that external path is redacted.
The `artifacts/` paths in the reproduction commands are generated local
builds and outputs, not links to published evidence. Downloaded driver
packages and raw logs are not distributed with the repository:

```bash
cmp_open_source='<open-source-tree>'
mkdir -p artifacts/devid620
gcc -shared -fPIC -O2 -Wall -Wextra -Werror \
  -I"$cmp_open_source/src/common/sdk/nvidia/inc" \
  -I"$cmp_open_source/src/nvidia/arch/nvalloc/unix/include" \
  tools/devid/observe_rm_policy.c -ldl \
  -o artifacts/devid620/observe_rm_policy.so
python3 -m py_compile tools/devid/read_identity.py
```

Verified: the GCC build and Python syntax check exited zero with GCC 16.2.1
and Python 3.14.10.
An ignored C smoke harness issued four failing ioctls with an inaccessible
argument pointer. The observer preserved `-1`/`EBADF` without dereferencing
the pointer or logging a policy response. Its build and run commands exited
zero. The harness was a local control and is not published.

Verified on kernel `7.3.4-1-cachyos`, proprietary NVIDIA `620.70.06`, GSP OFF:
read-only identity inspection returned `1f0b10de` for the kernel cache, PCI
configuration and BAR0 mirror, BOOT0 `166000a1`, and command `0407`. With
the installed NVIDIA-patcher module, the observer logged `00 00`; Vulkan
reported CMP 40HX and Device ID `1f0b`. The installed module file contained
the CMP replacement and no original CMP pattern. Loaded module memory was
not compared with the disk file. Duplicate NVIDIA layer warnings and
duplicate CMP enumeration were present.

### Stock module prepared for comparison

Verified: an isolated copy of the supplied proprietary kernel wrapper built
`nvidia.ko` without changing its RM table. The initial build used GNU ld and
failed with `unrecognised emulation mode: llvm`. The successful command,
from the isolated proprietary kernel-wrapper copy, was:

```bash
make -j8 modules NV_KERNEL_MODULES=nvidia LLVM=1 CC=clang LD=ld.lld IGNORE_CC_MISMATCH=1
```

It exited zero with Clang and LLD `23.1.5`. The installed kernel compiler
also reported `23.1.5`; the wrapper's compiler-version check had warned
despite those matching versions. Numerous objtool warnings remained in the
stock binary, so this was a successful build with warnings.
`modinfo -F version` returned `620.70.06` and `modinfo -F vermagic` returned
`7.3.4-1-cachyos SMP preempt mod_unload`. The built module retains one stock
CMP pattern. It was subsequently loaded temporarily for the comparison
below; it was not installed over the system module.

Verified: the installed modeset and DRM modules both report version
`620.70.06` and vermagic `7.3.4-1-cachyos SMP preempt mod_unload`.
`modprobe -n -r nvidia_drm nvidia_modeset nvidia_uvm nvidia_peermem nvidia`
exited one with `Module nvidia_drm is in use`. This was a dry run; the stack
remained loaded at that point.

### Stock/restored kernel comparison

Verified on 2026-11-14: I ran the temporary kernel comparison from KDE on the
integrated GPU. I closed the application using NVIDIA and stopped
`nvidia-persistenced`; KDE remained running. A TTY and display-manager stop
were unnecessary in this setup. Normal module unloading succeeded after
the NVIDIA clients released their device handles.

The prerequisite is that NVIDIA clients have exited. A graphical session
can remain running if it uses the integrated GPU and holds no NVIDIA
devices. Check NVIDIA and DRM ownership before unloading. If a module is
busy, stop without forcing an unload.

The comparison unloaded NVIDIA normally, loaded the isolated stock core
with `NVreg_EnableGpuFirmware=0`, queried identity and policy, then unloaded
it and restored the installed NVIDIA-patcher core. Both DRM/modeset
companions matched 620.70.06.

| Stack | Successful `0x20810107` response | Vulkan Device ID | Vulkan driver |
| --- | --- | --- | --- |
| Prepared stock core | `01 01` | `1f0b` | `620.70.06` |
| Restored NVIDIA-patcher core | `00 00` | `1f0b` | `620.70.06` |

The stock identity snapshot had consistent `1f0b10de` identities, BOOT0
`166000a1` and command `0407`. No restored BAR0 snapshot was captured. Both
Vulkan summaries were byte-identical; the observer logs had the same
duplicate-layer warnings alongside their different policy responses.

Verified: the comparison reproduces the predicted RM response change while
Device ID stays `1f0b`. It supports the table's role in Linux policy, but does
not isolate every difference between the modules. GPU timestamps were not
measured in this module comparison; the later stock identity comparison
covers them. Game FPS, reset/resume and Windows 3D remain untested.

### Published XVE selector and read-only observation

Verified, register-document inspection: NVIDIA's TU102 and TU104 function-zero
XVE maps define `NV_XVE_CYA_3` at offset `0x708`, with
`STRAP_DEVID_SEL_OVERRIDE_EN` at bit 19 and
`STRAP_DEVID_SEL_OVERRIDE_VAL` at bit 20. Both fields are marked `RWCVF`.
The references are pinned to open-gpu-doc commit
`9fdf5c4062007929d9f4e6cbad9c9771fe61b880` (accessed 2026-11-14):

- [TU102 XVE definitions](https://github.com/NVIDIA/open-gpu-doc/blob/9fdf5c4062007929d9f4e6cbad9c9771fe61b880/manuals/turing/tu102/dev_nv_xve3g_fn0.ref.txt#L1314-L1321).
- [TU104 XVE definitions](https://github.com/NVIDIA/open-gpu-doc/blob/9fdf5c4062007929d9f4e6cbad9c9771fe61b880/manuals/turing/tu104/dev_nv_xve3g_fn0.ref.txt#L1314-L1321).

Verified by the experiment below: writes to bits 19 and 20 at TU106 BAR0
offset `0x88708` read back as requested, and enabled selector one changed
both physical identity readings. This confirms the measured behavior on
this CMP40; other boards and reset persistence remain untested.

The published value field selects one bit. It does not accept a 16-bit
Device ID. The measured settings produced `1f0b` and `1f4b`, respectively.
Their XOR is `0x0040`, so this experiment changed Device ID bit six.
It did not produce the proposed `1f06`, and it does not establish a general
formula for other boards.

Verified on 2026-11-14: the default mode of
[`tools/devid/probe_selector.py`](../../tools/devid/probe_selector.py)
read `CYA_3 = 0987e0ed`, with enable clear and selector value zero.
The cache, PCI configuration and BAR0 mirror all returned `1f0b10de`,
BOOT0 was `166000a1`, and the PCI command was `0407`.
The following command exited zero on x86-64 Linux kernel
`7.3.4-1-cachyos`, Python `3.14.10`, with proprietary `nvidia` 620.70.06
bound. The device-directory argument is redacted. GSP mode was not queried
again for this read:

```bash
sudo -n python3 tools/devid/probe_selector.py <pci-device-directory>
```

Verified: the following command exited one with
`the experiment requires an unbound PCI device`. Its guard rejected the
bound device before opening BAR0 for writing:

```bash
python3 tools/devid/probe_selector.py <pci-device-directory> --experiment
```

### Selector experiment helper

`probe_selector.py --experiment` requires an unbound, powered CMP40 with
PCI memory decoding enabled, consistent identities and no existing
selector override. It tries selector values zero and one by changing only
bits 19 and 20 of the saved DWORD. Each case records the requested value,
readback and identity snapshot, then restores the original DWORD and checks
the identity again. A rejected control write or a change in unrelated bits
stops the sequence after restoration. Handled errors and Ctrl+C retain the
attempt record and trigger a restoration attempt.

The helper uses an aligned native `ctypes.c_uint32` store on x86-64 Linux.
It does not change PCI command, power state, driver binding or enumeration.
If unloading the driver disables memory decoding, it refuses the test.
The OS cache is expected to retain its old ID during a temporary selector
change; a changed physical ID is recorded even when `identities_agree` is
false. The experiment attempts to restore the original state and leaves
driver reload to the operator.

Verified: nine local offline tests passed for both selector cases, write
rejection, restoration failure, observation failure, Ctrl+C, preflight
guards, unrelated-bit changes and four-byte store width. That harness is
not published. The following syntax/help checks also exited zero:

```bash
python3 -m py_compile tools/devid/read_identity.py tools/devid/probe_selector.py
python3 tools/devid/probe_selector.py --help
```

The tests use simulated registers and anonymous memory. They establish
software control flow and store width; the hardware result is recorded
separately below. Restoration cannot be guaranteed after power loss, device
disappearance, a fatal MMIO fault or process termination.

Verified on 2026-11-14: after NVIDIA was unloaded, the unbound function
reported runtime status `suspended`, PCI power state `D0`, enable count
zero and PCI command `0003`. Memory decoding was still enabled. The
runtime-status check rejected the experiment before any selector write;
`suspended` alone did not establish that this GPU was in D3.

A temporary `power/control=on` changed runtime status to `active` while
PCI power state remained `D0`. The default read-only probe then exited
zero with consistent `1f0b10de` identities, BOOT0 `166000a1` and unchanged
`CYA_3 = 0987e0ed`. An EXIT trap restored the original `auto` policy, and
subsequent inspection confirmed `auto`, `suspended`, `D0`. No selector
write or driver load ran. The runtime-PM verification command was the
following; the external device-directory value is redacted:

```bash
sudo -n bash -c 'set -eu
pci_selector_device="<pci-device-directory>"
test ! -e "$pci_selector_device/driver"
pci_selector_control=$(cat "$pci_selector_device/power/control")
trap '\''printf "%s\n" "$pci_selector_control" > "$pci_selector_device/power/control"'\'' EXIT
printf "%s\n" on > "$pci_selector_device/power/control"
cat "$pci_selector_device/power/control" "$pci_selector_device/power/runtime_status" "$pci_selector_device/power_state"
python3 tools/devid/probe_selector.py "$pci_selector_device"
'
```

### Measured alternate identity

Verified on 2026-11-14: I unloaded NVIDIA, held runtime PM active, ran both
selector cases and restored the original `auto` policy after the experiment.
The commands were run from the repository root; the external device-directory
value is redacted:

```bash
pci_selector_device='<pci-device-directory>'
pci_selector_pm_saved=$(cat "$pci_selector_device/power/control")
printf '%s\n' on | sudo tee "$pci_selector_device/power/control"
sudo python3 tools/devid/probe_selector.py "$pci_selector_device" --experiment \
  > artifacts/devid620/selector-experiment.json
cat artifacts/devid620/selector-experiment.json
printf '%s\n' "$pci_selector_pm_saved" | sudo tee "$pci_selector_device/power/control"
```

The selector experiment recorded:

| State | CYA3 requested/read back | PCI config identity | BAR0 identity | Cached identity |
| --- | --- | --- | --- | --- |
| Baseline | `0987e0ed` | `1f0b10de` | `1f0b10de` | `1f0b10de` |
| Enabled, selector zero | `098fe0ed` | `1f0b10de` | `1f0b10de` | `1f0b10de` |
| Enabled, selector one | `099fe0ed` | `1f4b10de` | `1f4b10de` | `1f0b10de` |
| Restored | `0987e0ed` | `1f0b10de` | `1f0b10de` | `1f0b10de` |

Both writes matched their requested DWORDs and preserved unrelated bits.
BOOT0 remained `166000a1` and PCI command remained `0003`. Both per-case
restoration checks and the final `restoration_confirmed` were true.
The physical readings changed while the OS cache retained its enumeration
value. No driver was loaded while the override was active.

### Stock RM checks for 1f4b

Verified, offline inspection: the 315-entry restricted-SKU table contains
one `1f0b` entry and zero `1f4b` entries. `_nv025993rm` stores the raw
offset-zero callback identity at GPU-object `+0xe64`; this function has no
Device ID bit-six normalization. Other identity consumers remain untraced.

Verified, catalog inspection: the supplied 620.70.06 package's
`supported-gpus/supported-gpus.json` and
`html/supportedchips.html` list CMP40 `1f0b` and RTX 2060 SUPER `1f06`, but
contain no `1f4b` entry. These package catalogs were inspected on 2026-11-14.
There is no supported product name for `1f4b` in these catalogs.

Verified: the proprietary kernel wrapper's `nvidia/nv-pci-table.c` matches NVIDIA
vendor with any Device ID for VGA and 3D display classes. The original
`rm_is_supported_pci_device` instructions were run in Unicorn with synthetic
arguments and relocated read-only data:

| Input to the early PCI filter | Return value |
| --- | ---: |
| NVIDIA, 3D display class, `1f0b` | `1` |
| NVIDIA, 3D display class, `1f4b` | `1` |
| NVIDIA, 3D display class, `1f06` | `1` |
| Wrong vendor | `0` |
| Wrong base class | `0` |
| Invalid low Device ID | `0` |

The filter's legacy table has 1665 entries of 24 bytes at
`.rodata+0x45b1620`. Its `1f0b` entry has flags `0x440`, with the rejecting
legacy bits clear (`flags & 0x1f == 0`); neither `1f4b` nor `1f06` appears.
Any reached logging call was replaced with an invalid instruction so it
would fail the emulation. The selected cases returned without reaching one.
The emulation opened no GPU device and called no live RM entry point.
It exited zero with Python `3.14.10`, Unicorn `2.1.4` and pyelftools `0.33`.
This establishes acceptance by the early PCI filter. Full initialization
also runs `rm_is_supported_device`, whose inspected path reads BOOT0, BOOT1
and BOOT42 and selects a HAL, followed by further initialization. The native
experiment subsequently initialized RM with `1f4b` and enumerated it through
Vulkan. Reset/resume remains untested.

### Native initialization and policy under 1f4b

Verified on 2026-11-14: I followed the two-terminal protocol with the
prepared stock `nvidia.ko`, GSP OFF, and the selector held at value one.
Inspection of the six saved outputs confirmed:

| Observation | After stock initialization | After Vulkan enumeration |
| --- | --- | --- |
| CYA3 | `099fe0ed` | `099fe0ed` |
| Override enable/value | `true` / `1` | `true` / `1` |
| Cached PCI identity | `1f0b10de` | `1f0b10de` |
| PCI config identity | `1f4b10de` | `1f4b10de` |
| BAR0 identity | `1f4b10de` | `1f4b10de` |
| BOOT0 | `166000a1` | `166000a1` |
| PCI command | `0407` | `0407` |

The two selector snapshots are byte-identical. NVML reports KMD
`620.70.06`, Device ID `0x1F4B10DE`, GSP firmware `N/A`, product brand
`GeForce` and product name `Unknown Error`. Vulkan reports NVIDIA
proprietary `620.70.06`, Device ID `0x1f4b` and name `unknown device`.
It has two NVIDIA enumeration entries, as in the earlier baseline.
These naming results do not identify the card as an RTX 2060 SUPER.

The observer recorded one successful, unmodified policy response:

```text
RM_POLICY command=20810107 status=00000000 response=00 00
```

Together with the earlier stock `1f0b` response of `01 01`, this supports
the Device ID lookup's role in the Linux policy. The prepared module's
SHA-256 still matches the stock-core hash listed below. Loading that core
is confirmed by the reported command sequence; loaded module memory was
not compared with the file. GPU timestamps, game FPS and graphics execution
were not measured in this run. The GSP OFF result does not establish
acceptance by a future GSP ON initialization.

Cleanup failed before its first restoration MMIO read/write. The saved
hold result contains `restoration_confirmed: false` and:

```text
experiment failed; GPU runtime PM is not active; set power/control to on before the experiment
```

The helper had already waited for unbinding, then its active-power guard
rejected cleanup. The log does not establish what changed runtime PM.
Before manual recovery, a read-only inspection found NVIDIA bound again
with selector one still enabled and physical identity `1f4b`. The failed
hold record was preserved. Separate recovery successfully
reasserted runtime PM, restored CYA3 `0987e0ed` and all three identities to
`1f0b10de`, and preserved unrelated fields. I restored the `auto` power
policy, reloaded NVIDIA with GSP OFF and checked the original ID in
`nvidia-smi -q`. The post-reload result was reported directly; no saved NVML
output was supplied for that check.

### Holding the selector during manual stock initialization

[`tools/devid/hold_selector.py`](../../tools/devid/hold_selector.py) prepares
that experiment without loading a driver itself. With `--experiment` and
interactive terminal input, it requires an unbound, active CMP40 with
consistent original identities and override disabled. It selects only
value one and requires the measured physical `1f4b` readback before
announcing `SELECTOR_HELD`.

Keep its terminal open while a second terminal loads and checks the prepared
stock RM. After Enter, Ctrl+C or EOF, the helper waits while a driver is
bound and attempts restoration only after unbinding. It then reasserts
`power/control=on` and waits up to five seconds for active runtime PM before
any restoration MMIO access. It restores only the
saved selector bits, preserving other CYA3 fields that native initialization
may have changed. It checks selector readback and the cached/config/BAR0
identities afterward. PCI command may change during a module swap and is
reported separately from identity restoration.

Verified: 20 offline tests cover validation before writable BAR access,
hold/release ordering, bound-device and
alternate-ID write guards, rejected writes, unexpected IDs, Ctrl+C/EOF,
restoration failures, preservation of unrelated fields, a selector already
restored by driver reset, PCI command changes and read-only acceptance of
cached `1f4b`. Additional checks cover runtime-PM activation after unloading,
refusal to change power policy while bound, activation timeout, saved-state
validation, recovery from the measured alternate ID and rejection of
unknown or inconsistent live states. These commands exited zero on Python `3.14.10`:

```bash
python3 -m unittest discover -s tools/devid -p 'test_*.py' -v
python3 -m py_compile tools/devid/read_identity.py tools/devid/probe_selector.py \
  tools/devid/hold_selector.py tools/devid/test_hold_selector.py
python3 tools/devid/hold_selector.py --help
```

The original hold mode ran on hardware and exposed the runtime-PM cleanup
failure above. The `--restore-from` mode subsequently restored the live
GPU. The revised automatic hold cleanup shares its power-preparation and
restoration functions and completed the full two-terminal cycle in the
subsequent A/B/A run. The guard cannot prevent a
concurrent driver bind, and process kill, device disappearance, fatal MMIO
faults or power loss can prevent restoration. Do not start another bind,
reset, remove/rescan or power-state change during selection or restoration.
The intended native initialization occurs only after `SELECTOR_HELD`.

The A/B/A protocol below includes the two-terminal hold sequence. Keep
`nvidia-persistenced` stopped and NVIDIA clients closed. After native
initialization, verify the physical identity and GSP `N/A` before collecting
results. A physical `1f0b` means the override did not survive that stage;
the OS cache is expected to remain `1f0b` because this experiment avoids
reenumeration.

### Recovery after an interrupted or failed hold

`hold_selector.py --restore-from` accepts the saved original CMP40 baseline
and restores its selector bits without repeating the alternate-ID
experiment. It refuses a bound device and a cached identity other than the
original CMP40. After reasserting runtime PM, it checks BOOT0, physical
identity and selector against the original or measured alternate state
before restoring. Other CYA3 fields remain unchanged. The original failed
log is input only.

Close NVIDIA clients, stop persistence and unload normally. Set
`cmp_hold_record` to the JSON produced by the interrupted hold. The input
path and external device-directory value are redacted:

```bash
pci_selector_device='<pci-device-directory>'
cmp_hold_record='<saved-hold-json>'
systemctl stop nvidia-persistenced
sudo modprobe -r nvidia_drm nvidia_modeset nvidia_uvm nvidia_peermem nvidia
pci_selector_pm_saved=$(cat "$pci_selector_device/power/control")
sudo python3 tools/devid/hold_selector.py "$pci_selector_device" \
  --restore-from "$cmp_hold_record" > artifacts/devid620/selector-recovery.json
cat artifacts/devid620/selector-recovery.json
```

Proceed only if the command succeeds, `restoration_confirmed` is true, and
the final config and BAR0 identities are `1f0b10de`. Then restore the saved
power policy and reload the installed driver:

```bash
printf '%s\n' "$pci_selector_pm_saved" | sudo tee "$pci_selector_device/power/control"
sudo modprobe nvidia NVreg_EnableGpuFirmware=0
sudo modprobe nvidia_drm
systemctl start nvidia-persistenced
```

If recovery fails, keep NVIDIA unbound. A full power cycle is the fallback
when the selector or identity cannot be restored and confirmed.

### Stock Linux A1/B/A2 results

Verified on 2026-11-14: I completed the sequence with stock proprietary
620.70.06 and stock glcore, GSP OFF. A1 and A2 used physical `1f0b`; B used
physical `1f4b` while the Linux PCI cache stayed `1f0b`. Inspection of the
saved runs confirmed identical recorded sources, executables, workload
settings and stock-module hashes. Each phase retained 133 measured Vulkan
submissions and 945 sampled pixel checks. The offline comparison verified
38 shared metrics and omitted 13 CUDA metrics missing from B.

| Observation | A1 | B | A2 |
| --- | --- | --- | --- |
| Physical Device ID | `1f0b` | `1f4b` | `1f0b` |
| RM policy response | `01 01` | `00 00` | `01 01` |
| PCIe link | Gen1 x16 | Gen1 x16 | Gen1 x16 |
| Memory clock | 7000 MHz | 7000 MHz | 7000 MHz |
| Power limit | 184 W | 184 W | 184 W |
| NVML BAR1 total | 64 MiB | 64 MiB | 64 MiB |
| CUDA discovery | Available | No device | Available |

The following GPU-time medians use seven measured submissions per case:

| Workload | A1, ms | B, ms | A2, ms |
| --- | ---: | ---: | ---: |
| 1000 same-pipeline binds, no dispatch | 138.788576 | 0.000032 | 139.863648 |
| 1000 alternating-pipeline binds, no dispatch | 139.783456 | 0.000032 | 139.847776 |
| 100 bind-and-draw operations | 14.225824 | 0.113024 | 14.343200 |
| 100 bind-and-dispatch operations | 14.598272 | 0.306624 | 14.702272 |
| 100 draws after one initial bind | 0.225504 | 0.113280 | 0.228640 |
| 100 serial dispatches after one initial bind | 0.374816 | 0.306368 | 0.377440 |
| Heavy fragment workload | 1.579872 | 1.398272 | 1.581568 |
| Heavy compute workload | 1.606112 | 1.423488 | 1.607232 |

The bind-and-draw case improved about 126× and bind-and-dispatch about 48×
relative to both controls. The 32 ns bind-only result is the timestamp floor;
it does not measure shader throughput. The graphics workloads executed and
passed their sampled readback checks under `1f4b`. Games were not tested.

clpeak Vulkan medians from five processes per phase were:

| Measurement | A1 | B | A2 |
| --- | ---: | ---: | ---: |
| FP32 float4, GFLOPS | 8263.5879 | 8234.5615 | 8210.9482 |
| FP16 half2, GFLOPS | 16516.3027 | 16529.8574 | 16360.1973 |
| INT32 int4, GOPS | 5646.4185 | 5595.8970 | 5611.4189 |
| Global memory float4, GB/s | 410.1272 | 410.8267 | 410.2166 |
| Host to device, GB/s | 3.4002 | 3.4002 | 3.4002 |
| Device to host, GB/s | 3.3522 | 3.3512 | 3.3522 |
| Roundtrip latency, µs | 187.7030 | 38.7587 | 189.3355 |
| Dispatch latency, µs | 4.9624 | 6.2355 | 4.9559 |

The arithmetic and memory results show no systematic increase in peak
throughput. Roundtrip latency improved, while the separately reported
dispatch latency worsened. Scalar FP32 had roughly 15–21% spreads because
the first repetition was slower; all repetitions were retained.

Whole-run graphics-clock medians were 1920 / 1470 / 1905 MHz, and temperature
medians were 49.09 / 47.02 / 53.59 °C. Different workload durations and the
missing CUDA work in B affect these aggregates. They cannot normalize
individual timings. The heavy-workload timing ratios were about 1.13×;
initial bind cost and clocks remain contributors, so those ratios alone
do not establish increased execution resources. The compute-unlock payload
was not independently re-inspected during this identity comparison.

#### CUDA discovery and restoration

Verified: B failed at `cudaGetDeviceCount`, before PCI matching or SM checks
in the independent WMMA program:

```text
CUDA failure: no CUDA-capable device is detected
```

clpeak's own CUDA inventory also reported:

```text
=== CUDA backend ===
CUDA: driver init failed or no devices found
```

The failure therefore precedes clpeak benchmarking. A1 and A2 completed the
WMMA check with 163,840 outputs and maximum absolute error zero.
Hypothesis: a SKU/catalog filter or inconsistent cached and physical IDs
contributes to CUDA's rejection. The exact driver filter has not been traced.

The original runner recorded a secondary `KeyError` for the absent CUDA
selection. The runner now skips clpeak CUDA with the discovery reason while
keeping the WMMA failure visible. Sixteen offline runner tests pass, including
two cases for this reporting path. The corrected reporting has not been
rerun on hardware; the original captured records were preserved.

The revised automatic hold cleanup completed after B: it reasserted runtime
PM from `auto/suspended`, restored CYA3 `099fe0ed` to `0987e0ed`, preserved
unrelated bits and confirmed all three identities as `1f0b10de`. A2 then
reproduced the original policy and CUDA availability.

### Extended Linux benchmark protocol

The comparison uses the same stock proprietary 620.70.06 module and stock
glcore in three phases: A1 with `1f0b`, B with the held `1f4b` selector, and
A2 after restoring `1f0b`. GSP stays OFF. Keep the compute payload, PCIe
configuration, ReBAR setting, power limit and cooling setup unchanged through
the sequence. The runner records their observable effects but does not apply
any unlock, select a driver, change clocks or write GPU registers.

[`bench_linux.py`](../../tools/devid/bench_linux.py) collects the runs.
[`benchmarks/`](../../tools/devid/benchmarks/) contains the Vulkan workloads
and a CUDA numerical WMMA check. Both use the GPU selected at runtime;
the Vulkan program requires NVIDIA vendor and the expected Device ID. It
deduplicates matching enumeration entries by UUID and refuses multiple
distinct matching GPUs. CUDA selection uses that GPU's queried PCI location.
Names and indices are discovered again in each phase because the substituted
identity has no cataloged product name.

| Workload | Configuration | What it measures |
| --- | --- | --- |
| Same and alternating compute pipeline binds | 1,000 binds, no dispatch; two distinct pipeline objects using the same shader | The policy's command-buffer delay; an empty buffer can reach the timestamp floor |
| Command matrix | 256 × 256, 100 operations, two shader iterations | Draws, repeated binds, binds every 1/2/4/8/16/64 draws, rendering-scope changes, indirect draws and serial compute dispatch |
| Fragment workload | 1920 × 1080, four fullscreen draws, 256 shader iterations | Offscreen graphics execution with two dependent floating-point FMA chains |
| Compute workload | 1920 × 1080, four dispatches, 256 shader iterations | The corresponding compute shader and serial dispatch synchronization |
| CUDA WMMA correctness | 20 launches of 32 independent FP16 16 × 16 × 16 tiles | 163,840 FP32 output values compared with an exact CPU reference |
| clpeak CUDA and Vulkan | FP32, FP16, integer arithmetic, global/local memory, host/device transfers and launch latency | Separate arithmetic, bandwidth and latency scores where the backend supports them |

The Vulkan program uses one command buffer and one submission per repeat.
Its GPU timestamps include initial image transition, pipeline/descriptor
programming and workload execution. Shader and pipeline creation are outside
the interval. Image transfer and CPU readback verification follow the ending
timestamp. Each graphics/dispatch repeat checks nine pixel locations against
the CPU reference, allowing one UNORM unit in RGB and requiring exact alpha.
This is a sampled output check, not a verification of every shader invocation.
Compute dispatches writing the same image have explicit write-after-write
barriers. Their timing includes those barriers. The shader workload is a
relative execution test; its dependent chains do not measure peak FLOPS.

Each Vulkan workload follows a loop of repeated compute preconditioning
lasting at least five seconds of wall time, including process setup and
readback, then two untimed warmups and seven measured repeats by default.
Every measured GPU time is retained in CSV, along with CPU command
recording time and submission-plus-fence time. The latter includes image
readback transfer and is not a pure submission-latency metric. clpeak runs
five separate processes per backend, with two kernel warmups and a 150 ms
per-test calibration budget. Unsupported entries remain in the raw CSV and
are excluded from numerical comparisons. The WMMA check verifies correctness;
clpeak throughput alone does not establish numerical correctness or a newly
unlocked hardware feature.

The runner strips inherited Vulkan/CUDA and library-override variables and
disables implicit Vulkan layers. It observes the policy in a separate
`vulkaninfo` process, without preloading the observer into timed workloads.
Each stock phase requires the fresh physical-identity snapshot, proprietary
620.70.06, GSP `N/A`, the expected RM policy and the recorded stock glcore
hash. The glcore path comes from the policy probe's loader log. The prepared
module hash is checked too, but `--stock-loaded` is an acknowledgement of the
manual `insmod`, not an inspection of loaded kernel memory. `srcversion`
alone cannot distinguish a binary-patched core; it matched the prepared stock
file and the currently loaded NVIDIA-patcher module in the readiness check.

Raw files are restricted to ignored `artifacts/` directories. Each run saves
source/executable hashes, Vulkan capabilities, NVML XML before and after,
policy/library observations, GPU selection and 250 ms telemetry for clocks,
temperature, power, utilization and PCIe link. Inspect telemetry availability
and the raw capability/power/link fields before attributing a timing change
to DevID. The runner checks Vulkan/NVML identity again after the workloads.
An extension advertisement is not a successful RT workload. RT execution,
games and reset/resume persistence are not covered by this suite.

Build from the repository root with Vulkan headers, glslc, CMake, a C++20
compiler and the installed CUDA toolkit:

```bash
cmake -S tools/devid/benchmarks -B artifacts/devid620/bench-build \
  -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build artifacts/devid620/bench-build
nvcc -O2 -arch=sm_75 tools/devid/benchmarks/verify_wmma.cu \
  -o artifacts/devid620/bench-build/verify_wmma
python3 tools/devid/test_bench_linux.py
```

Build the observer as above. Separately build the unmodified proprietary
kernel wrapper for the running kernel, and set `cmp_stock_module` below to
the resulting module. The recorded hash at the end identifies this tested
build; another kernel/build requires deliberate revalidation of the runner's
stock-module hash guard. For clpeak, use the same executable in all phases.
The installed 2.0.19 help was inspected on 2026-11-14. Its Vulkan list uses its own
indices, including duplicate entries. The runner checks these against the
deduplicated inventory and checks the GPU name, backend and driver field in
each result CSV. This binary formats NVIDIA's Vulkan `driverVersion` with
generic Vulkan bit fields (`620.280.384` here) and uses the CUDA Driver API
version (`13.5` here) in its CUDA driver field. The runner derives those
labels from queried API values; NVML and Vulkan `driverInfo` establish the
actual NVIDIA release `620.70.06` separately.

Run the phases manually from the repository root. The external device and
executable paths below are redacted. Close NVIDIA clients and stop persistence
as in the earlier experiment; the desktop can remain on the iGPU.

For A1, save the runtime-PM setting and load the prepared stock module while
the selector is original:

```bash
pci_selector_device='<pci-device-directory>'
cmp_clpeak='<clpeak-executable>'
cmp_stock_module='<prepared-stock-nvidia.ko>'
systemctl stop nvidia-persistenced
sudo modprobe -r nvidia_drm nvidia_modeset nvidia_uvm nvidia_peermem nvidia
pci_selector_pm_saved=$(cat "$pci_selector_device/power/control")
printf '%s\n' on | sudo tee "$pci_selector_device/power/control"
sudo insmod "$cmp_stock_module" NVreg_EnableGpuFirmware=0
sudo modprobe nvidia_drm
sudo python3 tools/devid/read_identity.py "$pci_selector_device" \
  > artifacts/devid620/bench-identity-A1.json
python3 tools/devid/bench_linux.py run --device-id 1f0b --stock-loaded \
  --identity artifacts/devid620/bench-identity-A1.json --clpeak "$cmp_clpeak" \
  --out artifacts/devid620/bench/A1
```

If A1 fails, inspect `run.json` and the referenced log before continuing.
For B, unload the modules. In terminal A, start a new hold, keeping the saved
runtime-PM variable for final cleanup:

```bash
sudo modprobe -r nvidia_drm nvidia_modeset nvidia_uvm nvidia_peermem nvidia
sudo python3 tools/devid/hold_selector.py "$pci_selector_device" --experiment \
  > artifacts/devid620/bench-selector-hold.json
```

After `SELECTOR_HELD`, terminal B loads the same stock file and runs B. Set
the three redacted path variables there too:

```bash
pci_selector_device='<pci-device-directory>'
cmp_clpeak='<clpeak-executable>'
cmp_stock_module='<prepared-stock-nvidia.ko>'
sudo insmod "$cmp_stock_module" NVreg_EnableGpuFirmware=0
sudo modprobe nvidia_drm
sudo python3 tools/devid/read_identity.py "$pci_selector_device" \
  > artifacts/devid620/bench-identity-B.json
python3 tools/devid/bench_linux.py run --device-id 1f4b --stock-loaded \
  --identity artifacts/devid620/bench-identity-B.json --clpeak "$cmp_clpeak" \
  --out artifacts/devid620/bench/B
sudo python3 tools/devid/probe_selector.py "$pci_selector_device" \
  > artifacts/devid620/bench-selector-after-B.json
sudo modprobe -r nvidia_drm nvidia_modeset nvidia_uvm nvidia_peermem nvidia
```

Even if B fails, close any remaining NVIDIA clients and unload before
releasing the hold. In terminal A, press Enter and inspect
`bench-selector-hold.json`. Continue only after `restoration_confirmed: true`
and all final identities are `1f0b10de`. If cleanup fails, use the separate
recovery protocol above with this new hold file as its input.

For A2, load the stock module again, retaining runtime PM `on` through the
last run:

```bash
sudo insmod "$cmp_stock_module" NVreg_EnableGpuFirmware=0
sudo modprobe nvidia_drm
sudo python3 tools/devid/read_identity.py "$pci_selector_device" \
  > artifacts/devid620/bench-identity-A2.json
python3 tools/devid/bench_linux.py run --device-id 1f0b --stock-loaded \
  --identity artifacts/devid620/bench-identity-A2.json --clpeak "$cmp_clpeak" \
  --out artifacts/devid620/bench/A2
python3 tools/devid/bench_linux.py compare \
  artifacts/devid620/bench/A1 artifacts/devid620/bench/B artifacts/devid620/bench/A2 \
  --out artifacts/devid620/bench/comparison
```

After collecting A2, close NVIDIA clients, unload, restore the saved power
policy, reload the installed driver and restart persistence:

```bash
sudo modprobe -r nvidia_drm nvidia_modeset nvidia_uvm nvidia_peermem nvidia
printf '%s\n' "$pci_selector_pm_saved" | sudo tee "$pci_selector_device/power/control"
sudo modprobe nvidia NVreg_EnableGpuFirmware=0
sudo modprobe nvidia_drm
systemctl start nvidia-persistenced
```

Output directories must be new; use a new phase name for a rerun. Comparison
requires identical sources, executable hashes, workload settings and repeat
counts. It rejects incomplete Vulkan coverage and smoke-mode evidence, then
reports median, minimum, maximum and spread for each common metric. B is
compared separately with A1 and A2, and A2/A1 drift is reported. Missing
clpeak categories are listed rather than counted as zero. Bind-only results
below 0.01 ms are marked near the timestamp floor; their timing ratio is not
a shader-throughput speedup. Review clock, thermal and PCIe changes alongside
the ratios before publishing conclusions. A successful overall collection can
contain a failed optional category; check the per-category status.

Verified on 2026-11-14: readiness checks used x86-64 Linux kernel
`7.3.4-1-cachyos`, GCC `16.2.1`, Python `3.14.10`, Vulkan headers `1.4.363`
and CUDA toolkit `13.4` (`nvcc 13.4.92`). CMake and CUDA builds exited zero
with no compiler warnings in the final logs. The offline runner tests and
twenty selector-hold/recovery tests passed.

The integrated smoke ran on the installed NVIDIA-patcher driver, original
`1f0b`, GSP OFF and policy `00 00`, and is excluded from stock comparison.
It completed all 15 Vulkan cases, WMMA correctness and one clpeak process
per backend. WMMA checked 163,840 outputs with maximum absolute error zero.
The working-size matrix and heavy graphics/compute cases also passed sampled
readback checks, with three measured repeats after two warmups.
Validation-layer runs logged zero warnings, errors or synchronization
diagnostics. `spirv-val` exited zero for all three generated shaders.

Earlier readiness attempts exposed a wrong clpeak index and incompatible
driver-label assumptions. Those scores are excluded. The runner now selects
from the controlled inventory and verifies each backend's driver encoding.

### Privileged software policy control

Verified, binary inspection: the 620 RM registers command `0x20820102`,
parameter size `0x108`, with handler `_nv013971rm` at `.rodata+0x4556860`.
The handler writes the first parameter byte to GPU-object `+0xaf4` only
after `_nv042408rm` validates 32 QWORDs starting at parameter offset eight.
This is the inversion byte used by the existing `0x20810107` response.
The handler does not write the Device ID or restricted-SKU match byte.

Verified, source inspection: class `NV2082_BINAPI_PRIVILEGED` (`0x2082`)
is a child of `Subdevice`, with a single reserved DWORD allocation
parameter. `resource_list.h` requires privileged allocation, and
`binapiprivControl_IMPL` in `rmapi/binary_api.c` requires
`RS_PRIV_LEVEL_USER_ROOT`. The live proprietary class allocation and
command dispatch have not been exercised.

The token inputs are stored as encoded data referenced by `_nv042424rm`.
`_nv000815rm` decodes them into 65-byte buffers using the embedded key
and per-entry salt. `_nv042404rm` hashes the first 64 bytes of each buffer.
`_nv042408rm` complements each hash whose index has a set bit in
`0xbadcf00d`, then compares all 32 results with the supplied token.

Verified, offline emulation: an ignored helper reproduced the decoder and
hash in Python and compared both with the original x86-64 instructions
executed in Unicorn. All 32 decoded buffers were 64 bytes plus a NUL
terminator; every hash matched. The emulated verifier accepted the
constructed token and rejected a one-bit change. The helper applied the
ELF relocations for calls between these selected routines and provided
synthetic data and stack memory. It opened no GPU device and made no RM
control call.

The selected disassembly and emulation checks exited zero with GNU objdump
`2.47`, Python `3.14.10`, Unicorn `2.1.4` and pyelftools `0.33`. This
establishes the local token calculation for the inspected binary; it does
not establish acceptance by the live driver. The emulation helper is not
published with the repository.

Assumed: on stock CMP40 with table match one, setting inversion to one
would change the response from `01 01` to `01 00`, and a new glcore client
would skip the traced delay emission. A live setter/query pair and a
graphics timing test are still needed. This software path changes the
second response byte while retaining the first and the original Device ID;
its Windows 3D effect is unknown.

### Remaining firmware and Windows questions

Verified, source inspection: `src/nvidia/arch/nvalloc/common/inc/flcnretval.h`
defines `FLCN_ERR_HS_CHK_DEVID_OVERRIDE_ENABLED_ON_PROD` as `0xa9` in the
Heavy Secure Errors group. No selecting secure-firmware check was recovered.
The error name alone does not establish that TU106 runs the check or that
the selector would trigger it. The successful GSP OFF initialization above
does not establish acceptance with GSP ON.

The generated TU10x production Booter image was extracted, but disassembly
of its secure region did not produce a validated override path. The stock
GSP inner ELF was extracted without resolving such a path either. These
negative results do not establish that an override is impossible.
GNU objdump, ELF relocation inspection and Capstone supplied the initial
static RM/glcore evidence. The REA update subsequently enabled the Ghidra
analysis described below.

The static checks used GNU objdump, ELF relocation inspection, Capstone and
REA `6.9.0` with Ghidra `12.2`. Ghidra imports exhausted a 2G and 6G Java
heap in `AddressTableAnalyzer`. A successful focused analysis used a 6G heap
and `REA_GHIDRA_DISABLED_ANALYZERS=Create Address Tables`. The initial
`Address Tables` setting did not name an installed analyzer and produced
no pseudocode.

Verified: the successful batches returned nonempty pseudocode for
`_nv013961rm`, `_nv026818rm`, `_nv025993rm`, `_nv026925rm`, `_nv044869rm`,
`_nv013971rm`, `_nv042408rm`, `_nv042404rm` and `_nv026762rm`, with no item
failures. The output supports the identity readers, response logic and
privileged inversion path described above. Exact stores and registrations
were checked against assembly and ELF relocations. Disabling an analyzer
changes the analysis context; these offline operations do not establish a
writable hardware override.

### Windows boundary

Hypothesis: a firmware override could make the unmodified Windows driver
miss its restricted-SKU lookup. No Windows driver binary was inspected in
this investigation, and no Windows initialization or 3D test ran with
`1f06` or `1f4b`. Driver-package binding to the uncataloged `1f4b` identity
also remains unresolved. Linux's response chain establishes the policy
mechanism for the inspected releases; it does not establish a Windows
bypass or full RTX 2060 SUPER compatibility.

Verified SHA-256 values for the 620 inputs and prepared module:

| Artifact | SHA-256 |
| --- | --- |
| Open source archive | `bc9fa156dcdc5e0ed19c3c9ea70cfe2b8c9a0fdc415426af1ba5c3a1f9a7238d` |
| Stock `nv-kernel.o_binary` | `e70a8b30dde8f47a274883efd6a9f28f8cb5e12711b38a047daccc40c8863157` |
| Stock `libnvidia-glcore.so.620.70.06` | `94b6c6191348c3095469162f643a58d6b9b85feb9c990b3097c6e8ebd3110afc` |
| Prepared stock `nvidia.ko` | `c29383a811f7c175e88cd9340fcceb5fe0845d134a409b9e434f7e6ab8953f23` |
