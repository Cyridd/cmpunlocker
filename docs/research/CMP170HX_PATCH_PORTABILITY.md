# CMP 170HX patches: applicability to CMP 40HX

Source review on 2026-10-18: local `cmpunlocker-170hx-amoghmunikote`, upstream
[`amoghmunikote/cmpunlocker`](https://github.com/amoghmunikote/cmpunlocker),
commit `2d10f891a58824f0591c3332c4eccf37ff36b086`.
The profiling query mask is an optional 615 patch with a hardware-confirmed CUPTI
support gate. See [`CMP40_PROFILING.md`](CMP40_PROFILING.md) for upstream
history, installation and hardware checks.

The 170HX patches target GA100 devices `10de:20c2` and `10de:2082`. The 40HX
is TU106, `10de:1f0b`, with 8 GiB of physical GDDR6. Shared driver filenames
such as `kernel_gsp_tu102.c` do not establish register or firmware compatibility.

## Candidates with a purpose beyond the existing unlocks

| Patch or feature | Assessment for 40HX |
| --- | --- |
| `cmp-sku-mask.patch` | Upstream's profiling unlock (`300127f`), ported as optional patch 0004 for 615. It returns `CMP_SKU_NO` for 40HX; CUPTI gate support is confirmed; counter collection awaits hardware validation. |
| `persistent-sw-state.patch` | The flag exists in 615, so the idea is portable. It forces legacy persistence after initialization. Prefer `nvidia-persistenced` for keeping the GPU initialized between clients; the 615 teardown code explicitly deprecates legacy persistence. No new compute throughput is implied. |
| VFIO helpers and `cmp_no_bus_reset.c` | The no-reset handoff idea is useful for a VM project, but needs a separate TU106 implementation and host/guest lifecycle tests. Existing helpers reject 40HX, check GA100, expect at least 30,000 MiB of unlocked memory and restore GA100 GSP registers. Replacing PCI IDs alone is insufficient. |
| `name-string.patch` | Easily adapted in principle, but only changes the reported name. It does not unlock a feature. |

### What the CMP SKU mask actually changes

The upstream patch changes one answer in
`subdevice_ctrl_gpu_kernel.c`, leaving `gpuGetIsCmpSku_FWCLIENT()` and
`GspStaticConfigInfo.bIsCmpSku` intact. In 615 the latter is populated from
`NVKV_VAL64(BRANDING_IS_CMP_SKU, branding)`. A client that consults the masked
RM query may behave differently; a firmware restriction or a client that
checks a different source will remain unaffected.

It is reasonable to test this as an optional patch against the existing
Vulkan pipeline-bind benchmark, without the glcore bypass, and against
CUDA/CUPTI/Nsight operations of interest. The useful evidence is changed
behavior or timing as well as the changed query answer. The source review
does not establish that this removes the measured MME throttle.

### Profiling is the CMP SKU mask's intended purpose

Git history identifies `cmp-sku-mask` as the profiling patch: commit `300127f`
is titled "GPU profiling". CUPTI exposes a separate CMP rejection flag,
even though the open 615 RM `GPU_PROFILING_CAPABILITY` query returns enabled
on bare metal. The port still needs CUPTI/Nsight counter collection testing
on 40HX; profiling permissions and architecture requirements remain separate.

## Existing equivalents and memory-specific workarounds

| Upstream patches | Why they do not add a feature to the current 40HX series |
| --- | --- |
| `pcie-gen2.patch`, `pcie-gen2-probe-retrain.patch` | 40HX already has hardware-validated Gen2 x16, including GSP-ready policy writes and host retrain. GA100's larger protected-write table is architecture-specific; it does not provide a demonstrated Gen3 path. |
| `bar1-resize-unlock.patch` | 40HX already has an 8 GiB ReBAR patch. GA100 uses selector `0xa` for up to 64 GiB; 40HX uses selector `7` and checks readback/supported sizes. BAR1 is a mapping aperture, not extra VRAM. |
| Memory geometry in `sec2-postbl-plm-ss-cfg.patch` | GA100 FBPA/LTC geometry for physically installed HBM cannot create capacity beyond 40HX's physical GDDR6. The SM/security portion already has a separate TU106 implementation here. |
| `bar0-pramin-clamp.patch` | Keeps PRAMIN at the stock 8 GiB boundary when GA100's reported memory grows beyond 8 GiB. That trigger does not apply to an ordinary 8 GiB 40HX. |
| `late-pma.patch` | Primarily adds diagnostics for high reserved memory after geometry changes. In this checkout the named late-extension function leaves the candidate region reserved because it backs WPR2; it does not register an additional usable PMA region. It also changes CE virtual-mode selection for the two GA100 IDs. |
| `ce-scrub-workarounds.patch` | Changes scrub PTE kind and avoids virtual-mode CE scrubbing for GA100. These are initialization workarounds for that configuration, without evidence of a benefit on the working 40HX memory layout. |
| `ecc-enable.patch`, `ecc-fbpa-static.patch`, `ecc-reporting.patch` | GA100 ECC enablement/configuration and reporting are outside the intended 40HX port. Changing reported capability would not establish working error correction. |
| `booter-verify.patch` | Adds launch diagnostics and post-Booter readback. The 40HX patch already logs the relevant secure state, stock handoff and GSP-ready selectors. GA100 geometry readback adds no new feature here. |

The CMP SKU query mask now has a confirmed CUPTI support result. Persistence
should use the supported daemon when needed. VFIO remains a separate project.
Benchmarks on 2026-10-18 confirmed the optional glcore pipeline patch on
`610.57.04` and `615.78.08`. Both releases
use the same emitter signature at different offsets; the helper now selects
the library release automatically. The 580 slot-51 variant is verified offline.
