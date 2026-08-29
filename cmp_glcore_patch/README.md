# NVIDIA glcore MME pipeline throttle unlock

This is an optional 64-bit userspace patch for the classic Vulkan pipeline-bind throttle. It is independent of the compute, PCIe and ReBAR kernel-module patches installed by the repository's `install.sh`.

This directory contains a narrow binary patcher for the two x86-64 command
emitters in `libnvidia-glcore.so.610.57.04` that write:

```text
NVC597_CALL_MME_MACRO(52), 0xf0
```

The current patch changes the MME loop argument from `0xf0` (240) to a
requested DWORD value. The MME program itself is not modified.

The patcher refuses to operate unless the exact eight-byte command constant
occurs exactly twice. It always writes a separate library and does not modify
`/usr/lib`.

## Build the patcher

```bash
g++ -std=c++17 -O2 -Wall -Wextra patch_glcore.cpp -o patch_glcore
```

## Create the patched library

Use the installed NVIDIA userspace library as the input and write the patched
copy into this directory:

```bash
./patch_glcore /usr/lib/libnvidia-glcore.so.610.57.04 \
  ./libnvidia-glcore.so.610.57.04 1
```

The final `1` reduces the MME loop from 240 iterations to one. It does not remove the macro or change its program.

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
/usr/lib/libnvidia-glcore.so.610.57.04
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
3. Confirm that the patched library matches the installed NVIDIA driver:
   `610.57.04`.
4. Verify that the patched directory contains the complete library filename:
   `libnvidia-glcore.so.610.57.04`.
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

For the validated 64-bit 610.57.04 library, the two emitter constants were found at file offsets:

```text
0x00b20c2d
0x00dcbf60
```

The two emitter locations were identified specifically for:

```text
libnvidia-glcore.so.610.57.04
```

This is version-specific research tooling. Other NVIDIA driver versions require
new binary signatures and revalidation. 32-bit userspace components also
require separate signatures.

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

The experimentally validated unlock changes the argument so that only one
iteration of the expensive sequence is executed instead of 240.

The observed microbenchmark results on CMP 40HX were:

```text
4 pipeline binds:
  stock:      0.738976 ms
  argument 1: 0.005408 ms

1000 pipeline binds:
  stock:      183.296544 ms
  argument 1:   0.769792 ms
```

This corresponds to roughly a 238× reduction in the measured pipeline-bind
overhead in that specific test.

A separate in-process causal test replaced the complete command pairs with
length-preserving NOPs and measured `0.002368 ms` for four binds. That result
proves the command pair is the trigger, but it is not the timing of the
distributed argument-`1` library patch.

## Application observations

With the local userspace patch enabled on the tested system:

- Cyberpunk 2077 through Proton-CachyOS reached about 60 FPS average in its benchmark at High settings with DLSS Transformer Quality.
- War Thunder native Vulkan reached about 80-90 FPS during gameplay at Ultra with DLAA 4.

Before this pipeline fix, affected configurations showed a severe slowdown,
reported as up to roughly 15x. These are single-system observations and not
universal performance guarantees.

## Safety

Keep an untouched copy of the official library:

```text
/usr/lib/libnvidia-glcore.so.610.57.04
```

Do not mix a patched `libnvidia-glcore.so` from one NVIDIA driver release with
a different driver release.

The project is intended for hardware research and experimentation. Use the
patch at your own risk.
