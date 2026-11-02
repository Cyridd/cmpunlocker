# CMP 40HX CPU-RM unlock with a small Booter WPR heap

On 2026-10-31, proprietary Linux **610.43.03 and 615.78.08** initialized and
completed CUDA workloads with the full unlock and GSP disabled. I then
confirmed that the Windows **ZERO and FULL 960 KiB EFI variants both
initialize successfully**, and that FULL retains the unlock in multiple
tests. The exact Windows driver version for those tests was not
recorded. Earlier Windows failures on 617.14 are historical controls, not a
claim that the new build was tested on every Windows release.

The repository now ships the FULL Windows path and a separate Linux
[pre-driver CPU-RM module](../../linux/cpu-rm/README.md). Windows keeps the
current repository's ReBAR, signed-image installation and return-to-bootloader
features. I also tested the integrated installer and reproduced an upgrade:
after I deliberately set `EnableGpuFirmware=1`, **Disable GSP** changed it
to `0`, and the Gen2 task left it at `0`.

## What changed

The signed 610.43.03 TU102 Booter and published CMP40HX stock-relock payload
remain unchanged. Reducing the temporary Booter WPR heap moves the old
protected range above the native CPU-RM ACR code placement. The full payload
writes `SS0=88888888`, `SS1=00000008`, restores stock `FECS_PLM=ffffff8f`,
and tears down WPR2. The ZERO control changes only the two selector operands.

| Placement | Linux original | Linux working | Windows working |
| --- | --- | --- | --- |
| Heap request | 88 MiB | 1 MiB | 960 KiB (`0xf0000`) |
| WPR start | `0x1f8900000` | `0x1fe000000` | `0x1fe000000` |
| Heap start | `0x1f8a00000` | `0x1fe100000` | `0x1fe100000` |
| GSP image start | `0x1fe200000` | `0x1fe200000` | `0x1fe1f0000` |
| Non-WPR heap start | `0x1f8800000` | `0x1fdf00000` | `0x1fdf00000` |
| WPR end | `0x1fff00000` | `0x1fff00000` | `0x1fff00000` |

Linux's first observed native ACR code PA, `0x1fdf97000`, lies inside the
old heap and below the new WPR start. A bounded nonsecure marker at that
address starts successfully with the small heap. Native CPU-RM then works
without that probe, signed Booter Unload or an extra MMU invalidate.
This establishes a working placement strategy on the measured board; the
hidden protection state and its precise lifetime remain unresolved.

Windows declares a `0x6000`-byte boot image and a `0x1bfadc8`-byte dummy image.
Copying Linux's literal 1 MiB heap would leave Windows WPR at `0x1fdf00000`,
still overlapping the failing Linux PA. The 960 KiB request instead produces
the intended `0x1fe000000` boundary. The EFI checks the exact 8 GiB geometry
before GFW reset and refuses incompatible layouts.

Windows lets the native Booter complete, checks Falcon HALT, selectors,
mailbox status, stock FECS permissions and WPR teardown, then hands off.
It does not run the old post-Booter health/reset/cleanup/fallback sequence.
ReBAR is attempted only after successful native completion. Linux retains
its separately validated stopped-SEC2/empty-DMEM handoff and restores its
staging state before the proprietary driver probes the unbound device.

## Linux results

clpeak CUDA, proprietary **615.78.08**, 34 SMs / sm_75, GSP Firmware `N/A`.
The benchmark wrapper held an NVML client open and verified a **150 W**
limit before and after both runs.

| Measurement | ZERO / locked | FULL | Ratio |
| --- | ---: | ---: | ---: |
| FP16 WMMA, TFLOPS | 1.0440 | 66.1629 | 63.37x |
| cuBLASLt FP16, TFLOPS | 1.0335 | 52.1347 | 50.44x |
| INT8 WMMA, TOPS | 2.0724 | 131.0379 | 63.23x |
| INT4 WMMA, TOPS | 4.1447 | 245.9474 | 59.34x |
| cuBLASLt INT8, TOPS | 2.0675 | 84.2465 | 40.75x |
| Plain CUDA FP32, TFLOPS | 7.9136 | 7.4750 | 0.94x |

These are individual measurements, not a statistical stability study or
evidence of a global FP32 unlock. A separate WMMA numerical test checked
163,840 outputs over 20 launches against CPU calculations, with maximum
absolute error **0**; it passed again after a fresh FULL unlock. Post-workload
selectors remained full. Complete kernel logs for five native trials contained
no new Xid, RmInitAdapter, NVRM failure or IOMMU fault event.

The earlier matched 610.43.03 pair produced FP16 WMMA
`1.0274 → 66.2484 TFLOPS` and INT8 WMMA `2.0549 → 131.1897 TOPS`, at the
recorded **184 W default limit**. A one-shot 150 W request had been lost when
the last client closed, so those results must not be labelled 150 W.

Newer-architecture tests were unsupported. The INT8 m16n8k32 compile/load
error occurred on both controls and is excluded from passing cases.
The 615 first-query timeline dropped 809 change records; its retained fault
fields were zero, but it cannot establish complete transition ordering.
Native queries, CUDA execution and complete kernel logs independently
establish successful initialization.

## Reproduction and provenance

- Linux firmware hashes and sizes are pinned in
  [`artifacts-610.43.03.json`](../../linux/cpu-rm/firmware/artifacts-610.43.03.json)
  and [`asset_hashes.h`](../../linux/cpu-rm/kernel/asset_hashes.h).
- FULL payload SHA-256:
  `de7288bcfb80c2d4b259a59e823c926ba0a9adcab594219cc6802c94257ce271`.
- ZERO payload SHA-256:
  `6e1f97567bc75b7389782226c73d52c07ae51d04c00fc7df975e5ec325463262`.
- Earlier Windows controls are curated in
  [WINDOWS_CPU_RM_HISTORY.md](WINDOWS_CPU_RM_HISTORY.md).

Use the [Linux guide](../../linux/cpu-rm/README.md) or
[Windows guide](../../windows/README.md). The small heap is for this
pre-driver transaction; running GSP-RM with it is not validated. Other
boards/models, long-duration stability, reset recovery and suspend/resume
remain outside the confirmed scope. Separate community GRID/vGPU host and
guest results are recorded in [the GRID report](GRID_CPU_RM_REPORT.md).

## Repository integration checks

The integrated FULL EFI rebuilds byte-for-byte with gcc 16.2.1, binutils 2.47
and GNU-EFI 4.0.4. Its embedded SHA-256 is
`ac16567c21625cf99a10995bc1e74e45f4e2d68d72061d1e29b4116e269d8d69`.
FULL and ZERO EFI builds, payload/firmware hash checks and the actual
256-byte metadata-builder comparison pass. The Windows release builder
explicitly selects FULL, and a packaging test rejects a ZERO embedded image.

All five Windows Go modules cross-build and pass vet. Windows core and
installer unit tests pass through an isolated Wine registry, including
explicit-zero migration, preservation of unrelated registry values,
outdated/missing EFI detection, signature parsing and the existing Gen2 PL0
tests. The regenerated Windows resource object matches the committed copy.

The Linux module builds with LLVM and `W=1` against `7.2.9-2-cachyos` headers.
Firmware preparation from cached pinned archives, selector-control tests,
offline Booter/GSP/layout/malformed-input tests and the saved board-ROM FWSEC
parser checks pass. These build/offline checks complement the native Linux
workloads and my Windows installation and upgrade tests described above.
