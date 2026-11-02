# Vulkan pipeline-bind throttle with proprietary CPU-RM and GSP OFF

Verified: the 2026-11-02 experiment reproduced the classic Vulkan pipeline-bind
throttle on CMP 40HX with proprietary NVIDIA 615.78.08 and GSP disabled.
Changing the MME argument in the recorded command buffer or in a local glcore
copy removed the delay. Disabling GSP alone left the throttle in place.

Static inspection traced [NVIDIA-patcher](https://github.com/dartraiden/NVIDIA-patcher)'s
Device ID replacement to the RM response that controls this emitter. See
the [RM policy investigation](CMP40_NVIDIA_PATCHER_PIPELINE_CORRELATION.md)
for the response-byte experiment on the same GSP-OFF stack.

## Setup

- CMP 40HX / TU106, `10de:1f0b`.
- Kernel `7.2.9-2-cachyos`, CPU-RM test module 0.16.0.
- Pre-driver compute unlock, proprietary NVIDIA 615.78.08, then runtime
  Gen2 configuration. No Gen2 writes preceded driver loading.
- Full selectors `88888888/00000008`, WPR2 down, Gen2 x16, 150 W limit.
- Vulkan 1.4.351, GPU timestamp period 1 ns/tick.

Snapshots confirmed `GSP Firmware Version: N/A` before and after both
measurement series, `EnableGpuFirmware: 0` in `/proc/driver/nvidia/params`,
and the proprietary module identity in `/proc/driver/nvidia/version`.

The benchmark was `vk_shader_bind_matrix`. Loader logs confirmed that stock
runs used the system `libnvidia-glcore.so.615.78.08` and patched runs used a
separate local copy.

## Measurement

GPU timestamps enclose one primary command buffer and its binds. The compute
cases issue no dispatch. Pipeline and shader creation, submission overhead
and the command-buffer scanner's CPU time are not included in the GPU time.
The alternating case uses two distinct VkPipeline objects. Shader-object
controls bind VkShaderEXT objects without dispatch. These tests measure bind
commands; they do not measure shader throughput or game performance.

Each result is the median of three process medians, each from seven
submissions. The console values retain six decimal places in
milliseconds; the CSV output rounds the larger values.

### Command-buffer argument control

After recording, the benchmark finds `20010e68 000000f0` and requires exactly
one match per pipeline bind. Only the argument DWORD changed from 240 to 0;
the library, macro header and MME program stayed stock.

| Case, 1000 binds | Stock GPU ms | Command-buffer argument 0 GPU ms |
| --- | ---: | ---: |
| Same compute pipeline | 139.805760 | 0.013504 |
| Alternating two compute pipelines | 139.790112 | 0.013504 |
| Same compute shader object, stock | 0.009280 | n/a |
| Alternating two compute shader objects, stock | 0.009248 | n/a |

With argument 1, 1000 pipeline binds measured 2.448544 ms. Four binds
measured 0.738272 ms stock, 0.005056 ms at argument 0, and 0.014752 ms at
argument 1. Reducing the loop from 240 iterations to zero removed the delay.

The stock scaling series measured 0.000064, 0.185824, 0.738272, 2.946464,
18.399776 and 139.793152 ms at 0, 1, 4, 16, 100 and 1000 binds respectively.
Graphics clocks were not locked: short runs operated near 1470 MHz while
longer runs reached about 1935 MHz. Consequently these separate-process
scaling points should not be fitted as one fixed-frequency linear series.
The observed cost is about 140 to 185 microseconds per classic pipeline bind.

### Existing userspace workaround

`cmp_glcore_patch/patch_glcore.cpp` produced a separate 615.78.08 library
with argument 0 at both recognized slot-52 emitters. Exactly two bytes differ
from the original, the low byte of each argument DWORD:

```text
emitters:          0xb227ad, 0xdae680
changed bytes:     0xb227b1, 0xdae684
original SHA-256:  0431aee1c9d26b85c9a4a3724de4445113273c31b94229b39ba364988ffe491a
patched SHA-256:   24ce391f615df91642331ab4d9288bdf85a9732e83aff892c69e6506254c2967
```

The comparison alternated stock and patched runs, selecting the local library
through `LD_LIBRARY_PATH` and leaving the command-buffer scanner disabled.

| Case, 1000 binds | Stock GPU ms | Local glcore argument 0 GPU ms |
| --- | ---: | ---: |
| Same compute pipeline | 138.649568 | 0.004480 |
| Alternating two compute pipelines | 138.654816 | 0.004544 |

Patched shader-object controls measured 0.007008/0.007264 ms. Clock states
differed between runs, so these values do not establish a shader-object speed-up.
Four pipeline binds with the local library measured 0.001984 ms.

The earlier GSP-ON 615.78.08 measurement on 2026-10-18 was 138.716384 ms for
1000 same pipeline binds. A controlled GSP ON/OFF crossover was not run
here; the GSP-OFF result establishes that the delay can occur without running
GSP firmware.

## Interpretation

Glcore emits the GPU MME call that repeats the paired PIPE_NOP and
WAIT_FOR_IDLE methods 240 times. Setting its argument to zero works with
proprietary CPU-RM as well as the earlier GSP-ON stack. Running GSP firmware
is not required for this delay.

Verified: hash checks confirmed that the benchmark, module and system library
were unchanged. GSP remained disabled, selectors stayed unlocked and the link
remained Gen2 x16. No new Xid/RM/AER/IOMMU errors were recorded. The patched
library was loaded only by the test processes.

The experiment measured classic compute pipeline binds on this card and
driver release. Graphics pipeline binds, other releases, applications and
Windows still need separate tests.
