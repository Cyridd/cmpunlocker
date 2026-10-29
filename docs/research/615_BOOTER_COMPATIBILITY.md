# CMP 40HX: Booter compatibility with 615.78.08

Hardware validation on 2026-10-18 confirmed successful compute unlock, driver
initialization and `nvidia-smi` after the signed-Booter correction. The boot
log confirms the protected selectors survive the native 615 GSP launch.
The subsequent PCIe Gen2 failure was traced to missing policy writes in patch
0002. After restoration, the log records `RETRAIN_PASS` on the first attempt
and `LnkSta: Speed 5GT/s, Width x16`. Driver 615.78.08 is now the default;
610.57.04 and 580.173.02 remain selectable. This follow-up did not include
a separate ReBAR measurement or a test of the diagnostic PCIe variant.

## Why the previous port failed

The reported first failure was:

```text
sizeOfSignature=0xfa00
mailbox0=0x1c tracepc=0x1b38
WPR=01ffee00:01ffe000 FECS=ffffff8f SS0=0 SS1=0 RESET=8f
```

The native 615.78.08 TU102 Booter rejected the existing unlock payload with
status `0x1c`; the protected selector writes did not take effect. That payload
targets the 610.57.04 TU102 Booter, so selecting a newer driver alone does not
make it compatible with the newer firmware. The subsequent
`PRE_RESET_PLM_MISMATCH RESET=8f` messages follow the first failed launch.

The reference commit `ee41902f86f97c3ad099c0c58344f767f1e82e8a` ports CMP 170HX
(GA100) to 615.71.09. Its successful GA100 tests do not establish compatibility
of the CMP 40HX TU102 Booter in 615.78.08.

## Corrected launch sequence

The patch embeds the complete TU102 Booter Load archive from the official
610.57.04 source archive, including PROD/DBG images, headers, signatures,
signature patch location, ucode metadata and signature count. It uses the
existing NVIDIA allocation/signature-selection helper, preserving fuse-indexed
signature selection and the driver's debug-mode selection.

Only the CMP 40HX unlock branch allocates and executes this temporary Booter.
The allocation is freed immediately after execution, including on an execution
failure. `pBooterLoadUcode` continues to hold the native 615.78.08 Booter;
the stock GSP launch, subsequent launches, suspend/resume and unload use the
native driver firmware. The existing unlock ROP addresses remain paired with
the 610 Booter they were designed for.

The port also had a misplaced cleanup block: it freed `pStockSignatureData` in
`kgspPrepareForBootstrap_TU102`, before the stock-signature restoration could
use it. The corrected patch frees that buffer in `kgspFreeBootArgs_TU102`.
The new GMCAPI/NVKV initialization path retains NVIDIA's error handling;
a timeout no longer continues into decoding a potentially missing response.

The embedded encrypted firmware data retains NVIDIA's MIT license. The
generated header adds about 210 KiB of source text; the compressed images
occupy about 68 KiB. No 610 userspace libraries or installed firmware files
are required.

## Reproducibility and local validation

Source archive SHA256:

| Release | SHA256 |
| --- | --- |
| 610.57.04 | `619d7b5ce1f79c3211afdbf87d02b2174d268b10d005c5b8f994be22299be681` |
| 615.78.08 | `eac0ecd52facab28ad2963cf7e03ae8d2289d596ad590c703b5f72d65899e2bf` |

Extracted PROD Booter image SHA256:

| Release | Size | SHA256 |
| --- | --- | --- |
| 610.57.04 | `0xe700` | `e0f0fc93da097b5b68e5af3adb58b2235ed1fad5fce0b23592d3895af44cb743` |
| 615.78.08 | `0xe900` | `38fc45e436d1de8ad2f251ed7ec6f3baffcedba219cf86964ccccd8d9a902bf9` |

From the repository root, verify a freshly patched tree with:

```sh
python3 tools/check-cmp40-booters.py \
  --legacy-source open-gpu-kernel-modules-610.57.04.tar.gz \
  --native-source open-gpu-kernel-modules-615.78.08.tar.gz \
  --tree /path/to/patched-615-tree
```

The verifier checks archive hashes, compressed-data CRC/size, image/header
layout, signature metadata, exact reproducibility of the generated header,
preservation of the native Booter and the stock signature's lifetime. Use
`--generate-header /path/to/cmp40_booter_load_610.h` to regenerate the header
used inside patch 0001.

Completed local checks:

- Both 615.78.08 PCIe variants apply at fuzz 0 to clean pinned sources.
- The existing 610.57.04 patch variants still apply at fuzz 0.
- All five modules build with the complete normal 615 patch series on kernel
  `7.2.9-2-cachyos`, using `CC=clang HOSTCC=clang LD=ld.lld LLVM=1`.
- Embedded 610 Booter data/signatures match the official archive exactly;
  native 615 Booter source is unchanged.
- The version-selection bug in `check-linux-patches.sh` is fixed: a single
  `--version` now selects only that version, and repeated options are retained.

## Hardware validation

Install from the repository with matching 615.78.08 userspace and firmware:

```sh
sudo ./install.sh --driver-version=615.78.08
sudo poweroff
```

Use a full cold power cycle after the failed previous boot. Reusing the old
patched tree with `--no-download` or `--source-dir` is unsuitable; the default
installer extracts the verified source archive and moves the prior tree aside.

After powering on, collect:

```sh
sudo dmesg | rg 'CMP40|Booter|NVRM|GMCAPI'
nvidia-smi
```

Expected progression:

```text
CMP40_BOOTER_COMPAT_V552: UNLOCK_BOOTER=610.57.04 GSP_BOOTER=615.78.08
CMP40_COMPUTE_UNLOCK_V526: SIGNED_FULLSPEED_STOCK_RELOCK_WPR_DOWN_PASS
CMP40_COMPUTE_UNLOCK_V525: INPLACE_STOCK_BOOTER_AFTER_UNLOCK_PASS
CMP40_GSP_READY_V551: ...
```

Hardware validation confirmed acceptance of the signed legacy Booter on this
card, successful native 615 GSP initialization, and retained selectors
`SS0=0x88888888`, `SS1=8`, `FECS_PLM=0xffffff8f`. The compute unlock and
`nvidia-smi` worked. This test did not include new benchmark measurements.

## PCIe Gen2: omitted policy setup in the original 615 port

The next boot log showed:

```text
CMP40_GSP_READY_V551: ... ss0=0x88888888 ... ss1=0x00000008
CMP40_PCIE_GEN2_V2: CYA=068731b3(DIS_G2=0) CFG=800c5800(bits[19:18]=3) PL=00120036(bits[18:17]=1)
CMP40_PCIE_GEN2_V2: GSP policy not ready; retrain skipped
```

Both original 615 PCIe variants contained only the `kernel-open/nvidia/nv.c`
host retrain helper. They omitted the entire GSP-ready policy hunk from the
working 610 patch. The helper requires CFG bits `[19:18]=2` and PL bits
`[18:17]=2`; without those writes it correctly skips retraining. Cold restarts
cannot supply the omitted setup.

The corrected normal and diagnostic 0002 patches restore the exact working
610 policy block immediately after `CMP40_GSP_READY_V551` inside the CMP40
branch of `kgspBootstrap_TU102`. It updates the two fields while preserving
all other register bits, then logs readback before the host helper runs.
For the reported input values, expected readback is:

```text
CMP40_PCIE_GEN2_V2: LNKCFG=0x80085800(bits[19:18]=2) PL_RATE=0x00140036(bits[18:17]=2) CYA0=0x068731b3(DIS_G2=0)
```

The subsequent helper attempts retraining and reports `RETRAIN_PASS` or
`RETRAIN_FAIL`. Actual Gen2 is established by `LnkSta: Speed 5GT/s, Width x16`,
not just policy readback. The compute patch is unchanged by this correction.
The patch checker now requires the GSP policy marker separately from the host
retrain marker, so the omitted hunk cannot pass the prior marker-only check.

Both corrected variants apply at fuzz 0, and all five modules build with the
complete normal series on `7.2.9-2-cachyos` using Clang. A regression check with
the original 615 PCIe patch fails specifically on the missing GSP policy
marker. The host retrain helper and compute patch are unchanged.

The 2026-10-18 hardware test confirmed the normal variant on `0000:10:00.0`:

```text
CMP40_PCIE_GEN2_V2: LNKCFG=0x80085800(bits[19:18]=2) PL_RATE=0x00140036(bits[18:17]=2) CYA0=0x068731b3(DIS_G2=0)
CMP40_PCIE_GEN2_V2: RETRAIN_PASS status=1102 attempt=1
LnkCap: Speed 5GT/s, Width x16
LnkSta: Speed 5GT/s, Width x16
LnkCtl2: Target Link Speed: 5GT/s
```
