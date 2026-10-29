# CMP 40HX GSP pipeline throttle findings

Initial research: NVIDIA 610.57.04, GSP enabled, CMP 40HX / TU106.
Follow-up: 615.78.08 runtime confirmation and 580.173.02 offline analysis on
2026-10-18.

## Proven trigger

Each classic Vulkan pipeline bind emits this pushbuffer command:

```text
NVC597_CALL_MME_MACRO(52), argument 0xf0
```

Raw words:

```text
0x20010e68 0x000000f0
```

Replacing only those command pairs with length-preserving NOP pairs after
`vkEndCommandBuffer` reduced four-bind GPU time from `0.738976 ms` to
`0.002368 ms`.

Aliasing MME slot 53 to slot 52's instruction address and calling slot 53
twice produced `0.369600 ms`, almost exactly half of four original calls.
Therefore the expensive behavior is not keyed to macro index 52.

## MME program

Slot 52 points at Turing MME instruction RAM address `0x128` and contains four
instructions (three dwords each):

```text
00000003 1cc00000 b1cc5f00
9d140113 18c001a2 f18c0300
00000003 18c00000 318c0301
00000003 18c00000 318c0300
```

Decoded with Mesa's TU104 MME encoding:

```text
LOOP $load0, body length 2
mthd(0x1a2c, 0); emit(0)   # NVC597_PIPE_NOP
mthd(0x0110, 0); emit(0)   # NVC597_WAIT_FOR_IDLE
END_NEXT
```

The normal argument `0xf0` executes 240 iterations of the paired method
sequence.

The pair is essential. A single 240-iteration macro call measured:

```text
PIPE_NOP + WAIT_FOR_IDLE: 0.186208 ms
PIPE_NOP only:            0.003648 ms
WAIT_FOR_IDLE only:       0.004768 ms
```

This is not the ordinary cost of `WAIT_FOR_IDLE`; the expensive path is the
specific repeated pair.

## Userspace origin and bypass

The macro data and command emitter are in:

```text
/usr/lib/libnvidia-glcore.so.610.57.04
```

The two x86-64 command constants are at file offsets:

```text
0x00b20c2d
0x00dcbf60
```

Original bytes:

```text
68 0e 01 20 f0 00 00 00
```

They encode the method header followed by argument 240. Changing only the
argument DWORD to `1` preserves the pipeline programming path while reducing
the throttle loop from 240 iterations to one.

The checked patcher is in `cmp_glcore_patch/patch_glcore.cpp`. It refuses to
patch unless the exact original eight-byte constant occurs exactly twice and
writes a separate local library rather than changing `/usr/lib`. The current
patcher also recognizes the equivalent slot-51 emitter, checks the MME program
and surrounding instructions, and rejects incompatible ELF architectures.

Measured with the local patched library:

```text
4 binds, stock:       0.738976 ms
4 binds, argument 1:  0.005408 ms

1000 binds, stock:       183.296544 ms
1000 binds, argument 1:    0.769792 ms
```

The 1000-bind result is about 238 times faster.

The recommended configuration (and the one measured here) uses argument `0`. A
follow-up three-run measurement of a library patched that way at 1000 binds produced `0.005248`,
`0.005440` and `0.005568 ms` (median `0.005440 ms`). Relative to the same
`183.297 ms` stock capture, that is approximately `33,700x` in this GPU
timestamp microbenchmark. The large ratio reflects removal of the artificial
MME loop and must not be interpreted as a universal application speed-up.

## Interpretation

The GSP does execute the delay, but the proprietary userspace driver uploads
the MME program and emits its invocation. The currently proven bypass point is
therefore the userspace `libnvidia-glcore` emitter, not a speculative GSP
registry key. `RMPriorityThrottleDelay` is unrelated: NVIDIA's open registry
header documents it as a CPU thread-priority throttle delay while holding a
GPU lock.

## Additional releases

615.78.08 uses the same slot-52 eight-byte constant at `0xb227ad` and
`0xdae680`. A `vk_shader_bind_matrix` run on 2026-10-18 measured 1000 same
pipeline binds at `138.716384 ms` stock and `0.005856 ms` patched; alternating
two pipelines measured `138.715072 ms` and `0.005952 ms`. Shader-object binds
remained in the microsecond range in both runs. The regenerated 615 output
matches the runtime-tested library byte for byte.

580.173.02 uses `66 0e 01 20 f0 00 00 00` at `0xb51c8d` and `0xde8560`,
corresponding to slot 51. Disassembly shows the same `movabs` constant/store
emitter structure, and the library contains the exact known MME delay program.
The current patcher accepts this variant and changes only the argument DWORDs.
That is offline validation; a runtime benchmark needs the matching 580 stack.

The helper no longer pins a driver release. Binary signatures and instruction
layout still determine compatibility; unknown releases/layouts are rejected.
See [`cmp_glcore_patch/README.md`](../../cmp_glcore_patch/README.md) for the
complete 615 comparison and automatic version-selection rules.
