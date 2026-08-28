# CMP 40HX GSP pipeline throttle findings

Driver: NVIDIA 610.57.04, GSP enabled, CMP 40HX / TU106.

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
writes a separate local library rather than changing `/usr/lib`.

Measured with the local patched library:

```text
4 binds, stock:       0.738976 ms
4 binds, argument 1:  0.005408 ms

1000 binds, stock:       183.296544 ms
1000 binds, argument 1:    0.769792 ms
```

The 1000-bind result is about 238 times faster.

## Interpretation

The GSP does execute the delay, but the proprietary userspace driver uploads
the MME program and emits its invocation. The currently proven bypass point is
therefore the userspace `libnvidia-glcore` emitter, not a speculative GSP
registry key. `RMPriorityThrottleDelay` is unrelated: NVIDIA's open registry
header documents it as a CPU thread-priority throttle delay while holding a
GPU lock.

The binary offsets and match count are version- and architecture-specific.
The same MME body exists in NVIDIA 580 libraries too, but its emitter needs a
separate signature analysis before patching.
