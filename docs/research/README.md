# Research notes

Evidence and investigation write-ups behind the unlocks. None of this is needed to
install or use the project; start from the [main README](../../README.md) and the
[Linux installation guide](../linux-install.md) instead.

| Note | Contents |
|---|---|
| [`CMP40_NVIDIA_PATCHER_PIPELINE_CORRELATION.md`](CMP40_NVIDIA_PATCHER_PIPELINE_CORRELATION.md) | NVIDIA-patcher's Device ID replacement and RM/glcore throttle control in 610/615, tested through the RM response byte |
| [`CMP40_CPU_RM_VULKAN_PIPELINE_THROTTLE.md`](CMP40_CPU_RM_VULKAN_PIPELINE_THROTTLE.md) | Vulkan pipeline-bind delay with proprietary GSP OFF and argument-0 measurements using command buffers and glcore |
| [`WINDOWS_DISK_PCI_AUDIT.md`](WINDOWS_DISK_PCI_AUDIT.md) | Disk I/O boundaries, Gen2 guards, experimental ReBAR policy and requirements for a conservative standalone EFI |
| [`CPU_RM_SMALL_WPR.md`](CPU_RM_SMALL_WPR.md) | Successful proprietary Linux and Windows GSP-OFF handoff, exact heap geometry, paired throughput and numerical checks |
| [`CPU_RM_PCIE_GEN2.md`](CPU_RM_PCIE_GEN2.md) | Proprietary Linux runtime-only Gen2 and doubled DMA bandwidth; separate VFIO host retrain failure in issue #20 |
| [`GRID_CPU_RM_REPORT.md`](GRID_CPU_RM_REPORT.md) | GRID 19.6 community host/guest compute results, reported clpeak stability, boot ordering and the cached-v1 Gen2 boundary |
| [`WINDOWS_INSTALLER_AUDIT.md`](WINDOWS_INSTALLER_AUDIT.md) | v2.1.1 installer audit: duplicate Gen2 execution, bounded waits, handle cleanup and PCI scan cost |
| [`WINDOWS_CPU_RM_HISTORY.md`](WINDOWS_CPU_RM_HISTORY.md) | Curated earlier Windows controls: Booter boundary, SEC2 halt timeout, unsuccessful FLR/SB/PMU alternatives |
| [`615_BOOTER_COMPATIBILITY.md`](615_BOOTER_COMPATIBILITY.md) | 615.78.08 Booter compatibility and PCIe policy fixes, with hardware-validated compute unlock and Gen2 x16 |
| [`CMP170HX_PATCH_PORTABILITY.md`](CMP170HX_PATCH_PORTABILITY.md) | Source assessment of 170HX patch candidates for 40HX, existing equivalents and memory-specific workarounds |
| [`CMP40_PROFILING.md`](CMP40_PROFILING.md) | Optional profiling patch, confirmed CUPTI support gate and counter-collection checks |
| [`PCIE_GEN2_DIAGNOSTIC.md`](PCIE_GEN2_DIAGNOSTIC.md) | Diagnostic installation, collection and interpretation guide for the PCIe Gen2 path |
| [`PCIE_GEN3_CMP40HX_RESEARCH.md`](PCIE_GEN3_CMP40HX_RESEARCH.md) | Gen3 capability/policy investigation and negative results |
| [`PCIE_LINK_DISABLE_AUDIT.md`](PCIE_LINK_DISABLE_AUDIT.md) | Evidence and limitations of the Link Disable experiment |
| [`CMP40_GSP_PIPELINE_THROTTLE_FINDINGS.md`](CMP40_GSP_PIPELINE_THROTTLE_FINDINGS.md) | Reproducible evidence and reverse-engineering notes for the pipeline throttle |

Related helpers live outside this directory:
[`tools/cmp40hx-driver-reprobe-gen2.sh`](../../tools/cmp40hx-driver-reprobe-gen2.sh),
[`tools/cmp40hx-alder-lake-link-disable-test.sh`](../../tools/cmp40hx-alder-lake-link-disable-test.sh)
and [`cmp_glcore_patch/`](../../cmp_glcore_patch/README.md).
