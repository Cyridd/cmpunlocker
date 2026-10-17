# File structure

## Linux (repository root)

| File | Description |
|---|---|
| `0001-cmp40hx-unlock.patch` | Compute / SM unlock for NVIDIA 610.57.04 |
| `0002-cmp40hx-pcie2-unlock.patch` | PCIe Gen2 x16 unlock |
| `0002-cmp40hx-pcie2-diagnostic.patch` | Verbose replacement for `0002`; captures XVE/BAR0 and PCI capability state |
| `0003-cmp40hx-rebar-unlock.patch` | 8 GiB Resizable BAR unlock |
| `docs/research/PCIE_GEN2_DIAGNOSTIC.md` | Diagnostic installation, collection and interpretation guide |
| `docs/research/PCIE_GEN3_CMP40HX_RESEARCH.md` | Gen3 capability/policy investigation and negative results |
| `docs/research/PCIE_LINK_DISABLE_AUDIT.md` | Evidence and limitations of the Link Disable experiment |
| `tools/cmp40hx-alder-lake-link-disable-test.sh` | Manual, explicitly-confirmed research reproducer |
| `tools/check-linux-patches.sh` | Verifies that the three patches still apply to the pinned NVIDIA source, in both PCIe modes; used by CI and runnable locally |
| `tools/cmp40hx-driver-reprobe-gen2.sh` | Optional driver unbind/bind recovery for cold-boot Gen1 capability state |
| `tools/cmp40hx-driver-reprobe-gen2.service` | Disabled-by-default experimental systemd boot unit for the reprobe helper |
| `cmp_glcore_patch/` | Userspace Vulkan pipeline/MME throttle unlock for `libnvidia-glcore.so.610.57.04`: patcher source, `build_patched_lib.sh` helper, usage guide |
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
