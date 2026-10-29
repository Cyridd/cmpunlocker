# Research notes

Evidence and investigation write-ups behind the unlocks. None of this is needed to
install or use the project; start from the [main README](../../README.md) and the
[Linux installation guide](../linux-install.md) instead.

| Note | Contents |
|---|---|
| [`615_BOOTER_COMPATIBILITY.md`](615_BOOTER_COMPATIBILITY.md) | 615.78.08 Booter compatibility and PCIe policy fixes, with hardware-validated compute unlock and Gen2 x16 |
| [`CMP40_PROFILING.md`](CMP40_PROFILING.md) | Optional profiling patch, confirmed CUPTI support gate and counter-collection checks |
| [`PCIE_GEN2_DIAGNOSTIC.md`](PCIE_GEN2_DIAGNOSTIC.md) | Diagnostic installation, collection and interpretation guide for the PCIe Gen2 path |
| [`PCIE_GEN3_CMP40HX_RESEARCH.md`](PCIE_GEN3_CMP40HX_RESEARCH.md) | Gen3 capability/policy investigation and negative results |
| [`PCIE_LINK_DISABLE_AUDIT.md`](PCIE_LINK_DISABLE_AUDIT.md) | Evidence and limitations of the Link Disable experiment |
| [`CMP40_GSP_PIPELINE_THROTTLE_FINDINGS.md`](CMP40_GSP_PIPELINE_THROTTLE_FINDINGS.md) | Reproducible evidence and reverse-engineering notes for the pipeline throttle |

Related helpers live outside this directory:
[`tools/cmp40hx-driver-reprobe-gen2.sh`](../../tools/cmp40hx-driver-reprobe-gen2.sh),
[`tools/cmp40hx-alder-lake-link-disable-test.sh`](../../tools/cmp40hx-alder-lake-link-disable-test.sh)
and [`cmp_glcore_patch/`](../../cmp_glcore_patch/README.md).
