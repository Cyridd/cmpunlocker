# NVIDIA glcore MME pipeline throttle unlock

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

The final `1` selects the non-throttled argument used by the current unlock.

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

The two emitter locations were identified specifically for:

```text
libnvidia-glcore.so.610.57.04
```

This is version-specific research tooling. Other NVIDIA driver versions require
new binary signatures and revalidation. 32-bit userspace components also
require separate signatures.

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

The experimentally validated unlock changes the argument so that the expensive
throttle sequence is no longer executed.

The observed microbenchmark result on CMP 40HX was:

```text
1000 pipeline binds:
  stock:    ~183.3 ms
  unlocked: ~0.77 ms
```

This corresponds to roughly a 238× reduction in the measured pipeline-bind
overhead in that specific test.

## Safety

Keep an untouched copy of the official library:

```text
/usr/lib/libnvidia-glcore.so.610.57.04
```

Do not mix a patched `libnvidia-glcore.so` from one NVIDIA driver release with
a different driver release.

The project is intended for hardware research and experimentation. Use the
patch at your own risk.
