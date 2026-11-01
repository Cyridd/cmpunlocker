# Roadmap

The former items 1 and 2 are completed for the tested CMP40HX CPU-RM paths:

| Completed direction | Result |
| --- | --- |
| 1. Proprietary Linux driver | Pre-driver compute unlock with GSP OFF; native 610.43.03 and 615.78.08 initialization and CUDA workloads pass. [Guide](linux/cpu-rm/README.md). Resolves the proprietary-driver feature requested in [issue #8](https://github.com/Cyridd/cmpunlocker/issues/8). |
| 2. Windows without GSP | ZERO/FULL 960 KiB EFI initialization and FULL unlock tested by me. Installer installation and GSP-OFF upgrade also pass; Gen2 leaves GSP OFF. [Report](docs/research/CPU_RM_SMALL_WPR.md). |

GRID/vGPU was part of the original Linux request but has not been independently
validated. [A community report on GRID 19.6 / 580.178.05](docs/research/GRID_CPU_RM_REPORT.md)
now confirms compute unlock and healthy native/vGPU-manager initialization on
one Proxmox host. Gen2 remains unresolved, and guest throughput, numerical
correctness and lifecycle tests are still needed.

Remaining directions are feasibility studies, with no promised schedule.
Open an issue or discussion if you have hardware or results to contribute.

## 3. PCIe Gen3

**Goal.** Raise the link from the current Gen2 x16 (5 GT/s) to Gen3 (8 GT/s).

**Status.** Not implemented. The existing investigation found that the endpoint
advertises only Gen1 before the stock Booter runs and canonicalizes runtime
capability writes to Gen2; what remains is VBIOS/FWSEC research. Evidence in
[`docs/research/PCIE_GEN3_CMP40HX_RESEARCH.md`](docs/research/PCIE_GEN3_CMP40HX_RESEARCH.md).

## 4. RT cores

**Goal.** Enable the RT cores (hardware ray tracing).

**Why this is different.** The GPU reports zero RT cores. Unlike the Tensor Cores,
which are present in full number and were only throttled before the unlock, the RT
cores are completely disabled, probably by a fuse.

**Status.** Not implemented. Feasibility is unknown: if the cores are fused off in
hardware, this may not be achievable in software at all.
