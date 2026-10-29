# CMP 40HX: profiling unlock

## Profiling: upstream history and the 615 port

The upstream CMP170HX commit
[`300127f` (GPU profiling)](https://github.com/amoghmunikote/cmpunlocker/commit/300127f44b82b00ea1a4705c3d4e9fd8bc2979a6)
introduced `cmp-sku-mask.patch` specifically to unlock profiling. Its original
implementation cleared `pChipInfo->isCmpSku` for the two GA100 CMP IDs.
[`ee41902`](https://github.com/amoghmunikote/cmpunlocker/commit/ee41902f86f97c3ad099c0c58344f767f1e82e8a)
later moved the mask into the `NV2080_CTRL_GPU_INFO_INDEX_CMP_SKU` query,
because 615 moved CMP branding into GSP static info.

The installed CUPTI header documents
`CUpti_Profiler_DeviceSupported_Params.cmp` as supported for non-CMP GPUs
and unsupported for CMP GPUs. CUPTI's support-query sample reports CMP as
a separate reason that profiling is unavailable. This is distinct from
the generic RM profiling-capability query, which reports enabled on bare metal.

`patches/615.78.08/0004-cmp40hx-profiling-unlock.patch` changes only the CMP
SKU query for `10de:1f0b`: the answer becomes `CMP_SKU_NO`. Other GPUs retain
the stock answer. GSP branding, PCI ID, memory layout, secure compute payload,
PCIe and ReBAR code are unchanged. This ports the current upstream profiling
idea; actual counter collection on TU106 remains to be validated.

Profiling is opt-in:

```bash
sudo ./install.sh --profiling
sudo poweroff
```

615.78.08 is the installer default; the profiling option is not provided for
580/610 yet. The manifest records the flag and patch hash. To return to the
usual series, reinstall from fresh sources without `--profiling` and power
off/on. A reused source tree can still contain the optional patch.

## Verify the profiling gate and counter collection

The helper `tools/cmp40-profiling-support.cpp` queries CUPTI range-profiling
support and prints individual reasons including `cmp`. Build it from the
repository root against the installed CUDA/CUPTI toolkit (adjust the example paths to your installation):

```bash
g++ -std=c++17 -O2 -Wall -Wextra tools/cmp40-profiling-support.cpp \
  -I/opt/cuda/include -L/opt/cuda/lib64 -Wl,-rpath,/opt/cuda/lib64 \
  -lcuda -lcupti -o /tmp/cmp40-profiling-support
/tmp/cmp40-profiling-support
```

An optional argument selects the CUDA device index; the printed PCI address
identifies the selected card. Compare before installation and after power-on.
The intended change is `cmp=UNSUPPORTED` to `cmp=SUPPORTED`; overall
`range_profiling=SUPPORTED` is also needed. Exit 0 means supported, 2 means
a successful query reported unsupported, and 1 means an API/argument error.

Then collect counters from a known CUDA kernel with Nsight Compute or a CUPTI
range-profiling sample. The support query alone does not prove working counter
collection. Insufficient profiling permissions are a separate issue from the
CMP gate. The glcore bypass is independent of this patch.

Validation on 2026-10-18: the patch applied at fuzz 0 after the full 615
series in both PCIe modes. All five modules built with the normal series plus
profiling on `7.2.9-2-cachyos`, using Clang. The CUPTI helper built with
`-Werror`. An initial query returned `CUDA_ERROR_NO_DEVICE` and provided no
hardware baseline.

The subsequent hardware test on `0000:10:00.0`, recorded in
`profiling_test.txt`, reported `cmp=UNSUPPORTED range_profiling=UNSUPPORTED`
before the patch and `cmp=SUPPORTED range_profiling=SUPPORTED` afterwards.
The other support reasons remained supported. This confirms the CUPTI support
gate; the log does not demonstrate counter collection.
