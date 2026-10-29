# How it works

Technical background for the four unlocks. For measured numbers see
[`results.md`](results.md); for installation see [`linux-install.md`](linux-install.md)
and [`../windows/README.md`](../windows/README.md).

## What the compute unlock does and does not change

**Correction.** Earlier versions of this documentation listed FP32 compute at
`~0.39 TFLOPS` before the unlock and `7.0 TFLOPS` after it, and described the
CMP 40HX as having a global FP16/FP32 lock. That was wrong, and the figures have
been withdrawn. Measured values are in [`results.md`](results.md).

- **There is no global FP32/FP16 lock.** On the stock card, FP32 already reaches
  about 8.3 to 9 TFLOPS through cuBLASLt GEMM, OpenCL, Vulkan and CUDA's
  mixed-precision test, and FP16 shader math about 17.5 TFLOPS through Vulkan. These
  are unchanged after the unlock.
- **What the unlock restores** is the protected SM/security initialization state
  behind: the Tensor Core paths (WMMA fp16 `1.1 -> 72 TFLOPS`, cuBLASLt fp16
  `1.1 -> 56.5 TFLOPS`), INT8 `dp4a` throughput (`1 -> 34 TOPS`) and, in clpeak's
  CUDA tests, the plain FP32/FP16 FMA loops (`2.8 -> 8.5` and `5.7 -> 17.9 TFLOPS`).
  PCIe Gen2 (`3.4 -> 6.8 GB/s`) and ReBAR come from the other unlocks.
- Ordinary raster games already ran at normal performance before the compute
  patch, so their regular graphics-shader execution was never limited.
- The severe GSP-enabled gaming slowdown investigated by this project was a
  separate classic Vulkan pipeline-bind throttle. Its current bypass is the
  optional `cmp_glcore_patch` userspace patch, not the compute patch.

## Compute unlock

The tested CMP 40HX exposes a restricted SM/security configuration during
GSP/SEC2 initialization. The patch injects a payload into the standard SEC2
Booter flow and, during its privileged execution phase, restores the state used
by the validated compute workloads:

- `SS0 = 0x88888888`
- `SS1 = 0x00000008`
- `FECS_PLM = 0xFFFFFF8F`
- required `WPR2` / `SEC2 RESET_PLM` state

These writes cannot be reliably replaced with normal `setpci` or `devmem` writes
because the relevant registers are protected during the secure initialization
path.

Expected driver log:

`CMP40_COMPUTE_UNLOCK_V525: ... SS0=0x88888888 SS1=0x00000008 FECS_PLM=0xffffff8f`

## PCIe Gen2 unlock

The CMP 40HX is restricted to PCIe Gen1 x16 (2.5 GT/s) by default.

The PCIe patch:

1. opens the required GSP/RM PCIe policy;
2. requests a 5 GT/s link;
3. retrains the endpoint/upstream bridge;
4. verifies the resulting `LnkSta`.

It uses a protected GSP/RM policy path to enable the higher PCIe link rate, then
performs an actual PCIe link retrain: it sets the Gen2 target on both ends and
asserts Retrain Link on the upstream bridge. This normal retrain is the supported
kernel path in this repository. A separate Link Disable experiment is documented
in [`PCIE_LINK_DISABLE_AUDIT.md`](research/PCIE_LINK_DISABLE_AUDIT.md); it is not
integrated because it can detach GSP/RM after the driver has initialized. The
resulting hardware state is:

```text
LnkSta: Speed 5GT/s, Width x16
LnkCap2: Supported Link Speeds: 2.5-5GT/s
LnkCtl2: Target Link Speed: 5GT/s
```

This is a genuine PCIe Gen2 x16 link, not just a spoofed capability value.

The normal sequence is verified on the maintainer's AMD root port. On Intel
Alder Lake, a manual Link Disable experiment can train the physical link to
Gen2 x16 but can also leave `nvidia-smi` unable to access the GPU; that
platform-specific experiment is intentionally excluded from the kernel patch.

Repeated community testing on VBIOS `90.06.67.00.04` confirmed that a
cold-boot Gen2 failure (`CAP2=0x02`, retrain status `1101`) recovers after an
NVIDIA driver unbind/bind cycle. On the second bootstrap, `before_ovr` already
reports `CAP2=0x06`, showing that the reprobe materializes the Gen2 capability
before the host retrain phase. The optional helper
[`tools/cmp40hx-driver-reprobe-gen2.sh`](../tools/cmp40hx-driver-reprobe-gen2.sh)
performs this recovery only when the endpoint is a CMP 40HX and does not
advertise Gen2. Stop GPU workloads first; the helper does not use Link Disable
and is not installed or enabled automatically. A disabled-by-default
experimental systemd unit is also available for testing before the display
manager starts. The full evidence, safeguards and installation procedure are in
[`PCIE_GEN2_DIAGNOSTIC.md`](research/PCIE_GEN2_DIAGNOSTIC.md).

> PCIe Gen3 is **not** currently implemented by this project. The CMP 40HX
> investigation found that the endpoint advertises only Gen1 before the stock
> Booter and canonicalizes runtime capability writes to Gen2. See
> [`PCIE_GEN3_CMP40HX_RESEARCH.md`](research/PCIE_GEN3_CMP40HX_RESEARCH.md) for the
> collected evidence and the remaining VBIOS/FWSEC research boundary.

## Resizable BAR unlock

The patch configures the required XVE registers and BAR1 size selector, then lets
the NVIDIA driver perform its normal PCI BAR resizing.

The current configuration uses selector 7, corresponding to an 8 GiB BAR1
aperture. The resulting hardware and driver state is:

```text
BAR 1: current size: 8GB
BAR1 Memory Usage
    Total : 8192 MiB
```

## Pipeline bind / MME throttle unlock

The CMP 40HX also has a separate **userspace** performance restriction affecting
classic Vulkan pipeline binding. Unlike the compute, PCIe and ReBAR patches, this
unlock does **not** modify the open kernel module: the restriction lives in the
proprietary `libnvidia-glcore.so` (runtime-confirmed on 610.57.04 and 615.78.08).

The reproduced trigger is the classic pipeline path. The shader-object binding
diagnostic did not reproduce the same delay, so this project does not claim a
global shader-execution throttle.

The throttle was identified experimentally in the NVIDIA userspace driver. Each
classic `vkCmdBindPipeline` path invokes:

```text
NVC597_CALL_MME_MACRO(52), argument 0xf0
```

The selected MME macro executes a repeated sequence of:

```text
NVC597_PIPE_NOP
NVC597_WAIT_FOR_IDLE
```

for 240 iterations. The important point is that the slowdown is caused by the
**combination** of the two operations in the MME macro, not by either operation in
isolation.

The relevant emitters were located in the 64-bit 610.57.04 library at file offsets:

```text
0xb20c2d
0xdcbf60
```

The patcher ([`cmp_glcore_patch/patch_glcore.cpp`](../cmp_glcore_patch/patch_glcore.cpp))
changes only the emitted MME loop argument from `0xf0` (240) to a user-selected
DWORD. The recommended value is `0` (used for all reported measurements): it
retains the macro call and command shape while eliminating all 240 delay
iterations. Microbenchmarks showed that this removes the dominant bind overhead
while leaving the actual pipeline-bind functionality intact in the tested
applications.

This unlock is therefore a **userspace Vulkan command-generation patch**, not a
GSP/SEC2 hardware-security unlock.

### What ships and what does not

`cmp_glcore_patch/` contains the patcher source, the `build_patched_lib.sh` helper
and its own README. The patched library itself is **not** distributed: it is a modified copy of
NVIDIA's proprietary library, so you generate it locally from your own installed
copy (one command, see [`cmp_glcore_patch/README.md`](../cmp_glcore_patch/README.md)).
It can be tested without replacing the system library. For the command-level
evidence see [`CMP40_GSP_PIPELINE_THROTTLE_FINDINGS.md`](research/CMP40_GSP_PIPELINE_THROTTLE_FINDINGS.md).

### Limitations

- Runtime performance is confirmed on **610.57.04 and 615.78.08**;
  **580.173.02** passes offline validation with the slot-51 signature.
- The validated patch signatures cover the 64-bit library only.
- The helper selects the library version automatically. Releases with unknown
  emitter signatures or delay programs require further analysis and validation.
- NVIDIA 32-bit userspace components require separate signatures / patching.
- Do not replace the system `/usr/lib/libnvidia-glcore.so.*`; use the local
  library through `LD_LIBRARY_PATH` as documented in `cmp_glcore_patch/README.md`.
- The kernel-module unlocks (`0001`-`0003`) are independent of this userspace
  patch and are not modified by it.

## Windows / UEFI implementation

The Windows path lives in [`windows/`](../windows/). Instead of patching a kernel
module, it applies the unlock from a UEFI application that runs before Windows
boots and then chainloads it, with Windows-side tools handling GSP state, the
PCIe Gen2 bring-up after the NVIDIA driver loads, and cleanup:

- compute/Tensor unlock through the UEFI application (`40HXUNLK.EFI`);
- an 8 GiB Resizable BAR path in the same EFI application;
- optional PCIe Gen2 bring-up after the NVIDIA driver loads;
- GUI, command-line, status-check and uninstall tools, in English, Russian and
  Chinese.

Like the Linux patches, this is a volatile unlock: it does not flash the VBIOS and
must be reapplied after a full GPU reset or power cycle. Read
[`windows/README.md`](../windows/README.md) for firmware prerequisites, the
recovery procedure, and the current limitations before installing.
