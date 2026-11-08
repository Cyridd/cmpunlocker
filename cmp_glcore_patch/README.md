# NVIDIA glcore MME pipeline throttle unlock

This is an optional 64-bit userspace patch for the classic Vulkan pipeline-bind throttle. It is independent of the compute, PCIe and ReBAR kernel-module patches installed by the repository's `install.sh`.

This directory contains a narrow binary patcher for the two command
emitters in x86-64 `libnvidia-glcore.so.<version>`. Versions 610.57.04,
615.78.08 and 620.70.06 write:

```text
NVC597_CALL_MME_MACRO(52), 0xf0
```

Version 580.173.02 uses slot 51 for the same delay program. The patcher
recognizes both command signatures and checks the known MME program plus the
surrounding x86-64 emitter instructions. It has no driver-version whitelist.

The current patch changes the MME loop argument from `0xf0` (240) to a
requested DWORD value. The MME program itself is not modified.

The patcher refuses to operate unless exactly one known eight-byte command
signature occurs exactly twice. It always writes a separate library and does not modify
`/usr/lib`.

## Quick start

```bash
./build_patched_lib.sh                       # loaded driver version, argument 0
./build_patched_lib.sh --driver-version=620.70.06
./build_patched_lib.sh --driver-version=610.57.04 1
./build_patched_lib.sh 0 /path/to/libnvidia-glcore.so.580.173.02
GLCORE_SRC=/path/to/libnvidia-glcore.so.620.70.06 ./build_patched_lib.sh
NVIDIA_VERSION=620.70.06 ./build_patched_lib.sh
```

The helper determines the loaded driver's version from
`/proc/driver/nvidia/version`, then finds the matching original library in
`/usr/lib`, `/usr/lib64` or `/usr/lib/x86_64-linux-gnu`. If no driver is loaded,
it selects the sole installed 64-bit glcore version. Multiple installed
versions require `--driver-version=VER` or an explicit source. It does not
silently choose the newest leftover library.

An explicit source determines its release from the resolved filename, or the
ELF SONAME if the file was renamed (`readelf` is needed for that case). The
output always has the source release's full name, for example
`libnvidia-glcore.so.620.70.06`, in this directory. A requested version that
conflicts with the source is rejected. The CLI version takes precedence over
`NVIDIA_VERSION`; a positional source takes precedence over `GLCORE_SRC`.

Root is not needed. `GLCORE_SRC`, `NVIDIA_VERSION` and `CXX` are environment
variables: put them before the command, not among its arguments. The source
must be an original, unmodified library; the patcher rejects already patched
copies, unknown signatures, conflicting signatures and non-x86-64 libraries
before writing the output. It also refuses input/output aliases.

The patched library is **not distributed with this repository**: it is a modified
copy of NVIDIA's proprietary library, so it is always generated locally from your
own installed copy. Neither the patched library nor the compiled patcher is
tracked in git.

## Build the patcher (manual)

```bash
g++ -std=c++17 -O2 -Wall -Wextra patch_glcore.cpp -o patch_glcore
```

## Create the patched library (manual)

Use the installed NVIDIA userspace library as the input and write the patched
copy into this directory:

```bash
./patch_glcore /usr/lib/libnvidia-glcore.so.620.70.06 \
  ./libnvidia-glcore.so.620.70.06 0
```

The final `0` reduces the MME loop from 240 iterations to zero without removing the macro or changing its program. Other DWORD values are accepted for controlled experiments; `1` retains one iteration.

The original system library remains untouched.

## Test a Vulkan application locally

Run the application with the patched directory prepended to the dynamic
linker search path:

```bash
LD_LIBRARY_PATH="$PWD${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}" program args...
```

## Using the unlock with Steam games

### Native Linux / Proton games

In **Steam → Properties → General → Launch Options**, use:

```text
LD_LIBRARY_PATH=/path/to/cmp_glcore_patch:$LD_LIBRARY_PATH %command%
```

Do not replace the system library in `/usr/lib` merely to make a Steam game use
the patch. Per-process `LD_LIBRARY_PATH` is safer and makes rollback immediate.

## Important: avoid a full-library replacement

Do **not** permanently overwrite:

```text
/usr/lib/libnvidia-glcore.so.<version>
```

The intended workflow is:

```text
official library
      ↓
patch_glcore
      ↓
local patched library
      ↓
LD_LIBRARY_PATH
      ↓
one selected application
```

This keeps the original NVIDIA driver installation intact.


## Troubleshooting application crashes

If the application immediately crashes after adding the launch option:

1. Remove the `LD_LIBRARY_PATH` launch option and confirm the application
   starts normally.
2. Test the patched library with a small native Vulkan application.
3. Confirm that the patched library matches the installed NVIDIA driver version.
4. Verify that the patched directory contains the complete matching filename,
   for example `libnvidia-glcore.so.620.70.06`.
5. Check whether the application is using Vulkan. This unlock does not target
   every graphics API or every NVIDIA userspace component.
6. For Proton titles, test the same GPU workload with a native Vulkan program
   before debugging Steam Runtime / Proton environment handling.

Do not assume that an instant crash proves the binary patch itself is wrong:
an incompatible userspace library, mixed NVIDIA driver versions, Steam
Runtime isolation, or loading the wrong library copy can produce the same
symptom.

## Rollback

Per-process testing requires no rollback at all. Simply remove:

```text
LD_LIBRARY_PATH=/path/to/cmp_glcore_patch:$LD_LIBRARY_PATH %command%
```

from the Steam launch options.

If the system library was never overwritten, the NVIDIA installation itself
is unchanged.

## Current patch semantics

The current patch changes only the MME loop argument:

```text
0xf0 (240)  →  requested DWORD value
```

It does not alter the MME program itself.

The exact original bytes are:

```text
68 0e 01 20 f0 00 00 00
```

The recognized emitter signatures and observed offsets are:

| Library version | MME slot | Original eight-byte constant | File offsets |
| --- | --- | --- | --- |
| 580.173.02 | 51 | `66 0e 01 20 f0 00 00 00` | `0xb51c8d`, `0xde8560` |
| 610.57.04 | 52 | `68 0e 01 20 f0 00 00 00` | `0xb20c2d`, `0xdcbf60` |
| 615.78.08 | 52 | `68 0e 01 20 f0 00 00 00` | `0xb227ad`, `0xdae680` |
| 620.70.06 | 52 | `68 0e 01 20 f0 00 00 00` | `0xb27cfd`, `0xdbf780` |

Offsets are recorded evidence; the patcher searches by signature. Original
local libraries for all four releases pass offline checks, with only the
argument DWORDs changing and the system originals left intact. Runtime
performance is confirmed on 610, 615 and 620. The 580 variant is verified offline
and still needs a benchmark with a matching 580 driver stack.

Other releases can work unchanged when they retain one of these emitter
signatures and the known delay program. An unknown layout is rejected and
needs further analysis; no compatibility claim covers every future driver.
32-bit userspace requires separate signatures and is rejected.

The reproduced trigger is the classic pipeline path. A shader-object binding
diagnostic did not reproduce the same delay, so the patch should not be
described as a global shader-execution or FP32/FP16 unlock.

## Research result

The throttled path used:

```text
NVC597_CALL_MME_MACRO(52), argument 0xf0
```

The selected MME macro executes a long sequence of:

```text
NVC597_PIPE_NOP
NVC597_WAIT_FOR_IDLE
```

The experimentally validated unlock changes the argument so that **zero**
iterations of the expensive sequence are executed instead of 240.

The observed microbenchmark results on CMP 40HX were:

```text
4 pipeline binds:
  stock:      0.738976 ms
  argument 0: 0.002304 ms
  argument 1: 0.005408 ms

1000 pipeline binds:
  stock:      183.296544 ms
  argument 0:  0.005248 ms (0.005440 ms median across 3 runs)
  argument 1: 0.769792 ms
```

The recommended configuration is argument 0. Three argument-0 runs measured
`0.005248`, `0.005440` and `0.005568 ms`; the median is `0.005440 ms`, or
approximately **33,700x faster than stock** in this specific timestamp test.
The argument-1 result is included as a separate controlled comparison and is
approximately 238x faster than stock. These are microbenchmark measurements,
not universal application speed-up guarantees.

### 615.78.08 follow-up

The 2026-11-02 experiment confirmed the workaround with proprietary CPU-RM and
`GSP Firmware Version: N/A`. For 1000 same pipeline binds, GPU time was
138.649568 ms stock and 0.004480 ms with the local argument-0 glcore library.
Alternating two pipelines measured 138.654816 and 0.004544 ms. GSP OFF alone
does not remove the throttle. Each result is the median of three process
medians, each from seven GPU timestamp submissions. See the
[CPU-RM report](../docs/research/CMP40_CPU_RM_VULKAN_PIPELINE_THROTTLE.md).

On 2026-10-18, `vk_shader_bind_matrix` confirmed the same patch on CMP 40HX
with Vulkan 1.4.351 and a timestamp period of 1 ns/tick:

| Case | Binds | Stock GPU ms | Patched GPU ms (argument 0) |
| --- | --- | --- | --- |
| `compute_pipeline_bind_only_same` | 1000 | 138.716384 | 0.005856 |
| `compute_pipeline_bind_only_alt2` | 1000 | 138.715072 | 0.005952 |
| `compute_shader_bind_only_same` | 1000 | 0.007200 | 0.008992 |
| `compute_shader_bind_only_alt2` | 1000 | 0.007232 | 0.009376 |
| `graphics_one_bind_shader_draw` | 1 | 0.003584 | 0.004832 |

The pipeline cases reproduce removal of the delay. The shader-object cases
were already fast; these single-run figures do not establish a performance
change for those cases. The new patcher's 615 output matches the runtime-tested
copy byte for byte.

### 620.70.06 follow-up

Verified on 2026-11-08 with CMP 40HX, kernel `7.3.3-1-cachyos`, patched
nvidia-open 620.70.06 and `GSP Firmware Version: 620.70.06`. The kernel stack
used the compute, normal Gen2 and ReBAR patches without the optional profiling
patch; BAR1 was 256 MiB for this comparison. Vulkan reported API `1.4.357`
and a timestamp period of 1 ns/tick.
The power limit was checked at 150 W during the comparison and restored to
184 W afterward.

Three rounds alternated the original system glcore library and the local
argument-0 copy. Each process ran seven GPU timestamp submissions with
`vk_shader_bind_matrix --device='CMP 40HX' --case=CASE --binds=1000 --repeats=7`.
The figures are medians of the three process medians:

| Case | Binds | Original GPU ms | Patched GPU ms (argument 0) |
| --- | --- | --- | --- |
| `compute_pipeline_bind_only_same` | 1000 | 138.757888 | 0.004640 |
| `compute_pipeline_bind_only_alt2` | 1000 | 138.766816 | 0.004384 |
| `compute_shader_bind_only_same` | 1000 | 0.000032 | 0.000032 |
| `compute_shader_bind_only_alt2` | 1000 | 0.000032 | 0.000032 |
| `graphics_one_bind_shader_draw` | 1 | 0.003552 | 0.003584 |

The pipeline cases show removal of the throttle, approximately 29,900x and
31,700x in this timestamp test. Binding-only cases do not dispatch shaders;
the 32 ns shader-object timestamps do not measure shader execution. The
graphics control gives no evidence of an application performance improvement.
No command-buffer patch options were used. `LD_DEBUG=libs` confirmed the
selected library in every process, and both library hashes stayed unchanged
through the comparison.

The 620 library is 35,770,664 bytes. Its SHA-256 hashes are:

```text
original:   94b6c6191348c3095469162f643a58d6b9b85feb9c990b3097c6e8ebd3110afc
argument 0: 72ae1ccea97a276e8ca42d1440f36ddacac57662db77a2b687a1f202381f2755
```

The generated copy differs only at `0xb27d01` and `0xdbf784`, where `f0`
becomes `00`. The existing signature checks accept the 620 emitters and known
48-byte MME program without an algorithm change. Explicit version selection
and automatic selection both generated the same library.

`python3 cmp_glcore_patch/test_patch_glcore.py` passed all nine patching and
rejection-path tests. Focused disassembly used:

```bash
objdump -d --start-address=0xb27cfb --stop-address=0xb27d10 /usr/lib/libnvidia-glcore.so.620.70.06
objdump -d --start-address=0xdbf77e --stop-address=0xdbf793 /usr/lib/libnvidia-glcore.so.620.70.06
```

The first emitter loads the constant into RDI, the second into RSI, then
each stores it through RAX. Full Ghidra analysis exceeded its 330-second
startup deadline; no decompiler result is used for this validation.

## Application observations

With the local userspace patch enabled on the tested system:

- Cyberpunk 2077 through Proton-CachyOS reached about 66 FPS average in its benchmark at High settings with DLSS 4 Quality.
- War Thunder native Vulkan reached about 90 FPS average during gameplay at Ultra with DLAA 4.

Before this pipeline fix, affected configurations showed a severe slowdown,
reported as up to roughly 10-15x. These are single-system observations and not
universal performance guarantees.

## Safety

Keep an untouched copy of the official library:

```text
/usr/lib/libnvidia-glcore.so.<version>
```

Do not mix a patched `libnvidia-glcore.so` from one NVIDIA driver release with
a different driver release.

The project is intended for hardware research and experimentation. Use the
patch at your own risk.
