# NVIDIA-patcher Device ID table and the Vulkan pipeline throttle

Verified: [NVIDIA-patcher](https://github.com/dartraiden/NVIDIA-patcher)'s CMP
Device ID replacement changes the input to the RM response that enables
glcore's Vulkan pipeline-bind delay. Static inspection on 2026-11-02 found
the same path in stock 610.43.03 and 615.78.08. On proprietary 615.78.08 with
GSP OFF, clearing the response's second byte removed the measured delay.

## What the script changes

The script extracts `NVIDIA-Linux*.run` and patches
`kernel/nvidia/nv-kernel.o_binary` after making a backup. Its CMP pattern
changes the little-endian CMP 40HX entry from `0x00071f0b` to `0x0007ffff`.
It also replaces other CMP, P1xx and several engineering SKU IDs. The glcore
library is unchanged.

Static inspection found one CMP pattern match in each stock kernel object:

| Driver | CMP pattern file offset | CMP40 entry file offset |
| --- | ---: | ---: |
| 610.43.03 | `0x6560b6c` | `0x6560b7c` |
| 615.78.08 | `0x5fe26f0` | `0x5fe2700` |

The affected reader walks four-byte entries, compares their low 16-bit Device
IDs, and sets a GPU-object Boolean on a match. Applying the script's exact
CMP replacement to a copy of the table in memory removed its sole
`0x1f0b` match in both releases.

The upper 16-bit entry fields and the Boolean's proprietary name remain
unidentified.

## RM response and glcore emission

| Stage | 610.43.03 | 615.78.08 |
| --- | --- | --- |
| Table reader | `_nv030186rm` | `_nv031055rm` |
| `.rodata` table range, end exclusive | `0x55f10a0–0x55f1588` | `0x5011a20–0x5011f0c` |
| GPU-object match Boolean | `+0x9be` | `+0xa34` |
| GPU-object inversion Boolean | `+0x9bf` | `+0xa35` |
| RM response handler | `_nv017683rm` | `_nv018387rm` |
| Response registration in `.rodata` | `+0x56b0160` | `+0x50dae40` |
| glcore query instruction | `0xfc0e2d` | `0xfcb5f5` |
| glcore response-to-flag store | `0xfc0e67` | `0xfcb62f` |
| Classic bind emitter's flag check | `0xb2084b` | `0xb2279b` |
| Slot-52 constant file offsets | `0xb2085d`, `0xdcbb70` | `0xb227ad`, `0xdae680` |

The RM export table registers `0x20810107` with a two-byte response and the
handler listed above. Both handlers implement:

```text
response[0] = device_table_match
response[1] = device_table_match
if inversion_boolean != 0:
    response[1] = !device_table_match
```

Both glcore releases issue that command and, on success, set their internal
object byte at `+0x213` from whether `response[1]` is nonzero. The classic bind
emitter checks this byte and skips its `CALL_MME_MACRO(52), argument 240`
command pair when it is zero.

The result depends on the inversion Boolean. Every real RM response in this
experiment was `01 01`, which means the Device ID matched and inversion was
inactive. In that state, the patcher's table change predicts `00 00` and
disables emission. The inversion state in the earlier 610 installation was
not measured.

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

The response-byte experiment covers 615.78.08; the equivalent 610.43.03 path
was inspected statically. A comparison of original and NVIDIA-patcher kernels
with matching releases, userspace and pre-boot state remains untested. It
would check the effect of the complete kernel modification. Game performance
was not measured in this experiment.

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
| Patch CMP40's RM table in the driver | Assumed | Static replacement removes the lookup match. A matched kernel A/B test is still needed; the patch must precede RM's lookup. |
| Override the response inside the loaded driver | Hypothesis | A separate hook must target CMP40 and successful two-byte `0x20810107` responses before glcore caches the result. It has not been implemented. |
| Change Device ID or property selection through firmware | Hypothesis | A writable override, persistence through native initialization, and agreement with the OS's cached identity would need to be established. No such path has been validated. |

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

The current glcore helper remains the measured workaround. The
pre-driver firmware can remain responsible for compute unlock while a
separate driver or application step handles the pipeline policy.

The pre-driver feasibility assessment used static disassembly and historical
MME-upload captures. It performed no Device ID write or loaded-driver
modification. Windows driver behavior and a kernel response hook remain
untested.
