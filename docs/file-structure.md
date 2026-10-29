# File structure

## Linux (repository root)

| File | Description |
|---|---|
| `patches/580.173.02/` | Patch set for NVIDIA `580.173.02`: compute unlock, PCIe Gen2, ReBAR (no diagnostic PCIe variant) |
| `patches/610.57.04/` | Patch set for NVIDIA `610.57.04`: `0001` compute / SM unlock, `0002` PCIe Gen2 unlock, `0002-cmp40hx-pcie2-diagnostic.patch` verbose replacement, `0003` 8 GiB ReBAR unlock |
| `patches/615.78.08/` | Default patch set: the same four patches, with signed 610 TU102 Booter compatibility for compute unlock and restored GSP-ready PCIe policy setup |
| `docs/research/PCIE_GEN2_DIAGNOSTIC.md` | Diagnostic installation, collection and interpretation guide |
| `patches/615.78.08/0004-cmp40hx-profiling-unlock.patch` | Optional CMP SKU profiling query mask, selected by `--profiling` |
| `tools/check-cmp40-booters.py` | Verifies the signed 610 Booter archive embedded by the 615 compute patch, header reproducibility and preservation of the native 615 Booter; used by CI |
| `tools/cmp40-profiling-support.cpp` | CUPTI range-profiling support query for before/after hardware validation |
| `docs/research/CMP40_PROFILING.md` | Profiling patch, CUPTI support validation and counter-collection checks |
| `docs/research/PCIE_GEN3_CMP40HX_RESEARCH.md` | Gen3 capability/policy investigation and negative results |
| `docs/research/PCIE_LINK_DISABLE_AUDIT.md` | Evidence and limitations of the Link Disable experiment |
| `tools/cmp40hx-alder-lake-link-disable-test.sh` | Manual, explicitly-confirmed research reproducer |
| `tools/check-linux-patches.sh` | Verifies that each version's patch set still applies to its pinned NVIDIA source, in both PCIe modes where available; used by CI and runnable locally |
| `tools/cmp40hx-driver-reprobe-gen2.sh` | Optional driver unbind/bind recovery for cold-boot Gen1 capability state |
| `tools/cmp40hx-driver-reprobe-gen2.service` | Disabled-by-default experimental systemd boot unit for the reprobe helper |
| `cmp_glcore_patch/` | Userspace Vulkan pipeline/MME throttle unlock: version-selecting build helper, signature-checked x86-64 patcher, rejection-path tests and usage guide (610/615 runtime-confirmed, 580 offline-checked) |
| `docs/research/CMP40_GSP_PIPELINE_THROTTLE_FINDINGS.md` | Reproducible evidence and reverse-engineering notes for the pipeline throttle |
| `install.sh` | Build and installation script |
| `README.md` | Project overview |
| `docs/` | Detailed guides: install, how it works, results, compatibility, licensing |

## Windows / UEFI (`windows/`)

| Path | Description |
|---|---|
| `windows/README.md` | Windows user guide: prerequisites, installation, verification, recovery |
| `windows/FORK_CHANGES.md` | Fork-specific changes, the boot-manager handoff, and research boundaries |
| `windows/tools/unlock40x/` | UEFI unlock application source, firmware blobs, and `build_v70.sh` |
| `windows/tools/inst40hx/` | Installer GUI/CLI (`40HXInstaller.exe`) and its embedded EFI/drivers |
| `windows/tools/check40x/` | Status and diagnostic utility (`40HXCheck.exe`) |
| `windows/tools/uninstall40x/` | Component-level uninstaller (`40HXUninstaller.exe`) |
| `windows/tools/40hxcore/` | Shared Windows operations and Gen2 link logic |
| `windows/tools/build_release.bat` | Packages a release with a SHA-256 manifest |
| `windows/tools/verify_source.ps1` | Verifies expected source inputs and prints EFI hashes |
| `windows/release-files/` | Recovery guide, FAT32 USB helper, and other packaged documents |
| `windows/40HX_Gen2_Windows/` | Legacy standalone Gen2 scripts, helper drivers, and WinRing0 source |

## Build outputs that are not committed

Prebuilt Windows executables (`40HXInstaller.exe`, `40HXUninstaller.exe`,
`40HXCheck.exe`) are published on the GitHub Releases page together with
`SHA256SUMS.txt`; they are produced by `windows/tools/build_release.bat` and by CI.
The patched `libnvidia-glcore.so` and the compiled `patch_glcore` are generated
locally (see [`cmp_glcore_patch/README.md`](../cmp_glcore_patch/README.md)).
None of these are tracked in git.
