# Roadmap

Research directions for unlocking the CMP 40HX, in priority order. This is a list
of goals, **not** a schedule or a promise: every item starts as a feasibility
study, and any of them may turn out to be impractical. What the project does
today is described in the [README](README.md) and in
[`docs/how-it-works.md`](docs/how-it-works.md).

Want to help, or have hardware to test on? Open an issue or a discussion before
you start.

## 1. Linux with the proprietary driver (including GRID / vGPU)

**Goal.** Make the unlock usable with NVIDIA's proprietary Linux driver stack,
including the GRID driver used for vGPU hosts.

**Why.** The Linux patches in this repository modify the NVIDIA open kernel
modules, so they cannot be used with the proprietary driver. vGPU needs
the GRID driver, and that driver is not compatible with the open kernel modules.

**Status.** Probably already possible, to be confirmed. The UEFI-stage unlock works
on Linux as well as on Windows, so patched open modules are not required for it.
With the `--return-to-bootloader` load option of the EFI application (see
[`windows/README.md`](windows/README.md#returning-to-a-boot-manager-grub--limine--refind)),
it applies the unlock and returns to the boot manager (GRUB, Limine, rEFInd)
instead of starting Windows, and any Linux distribution can be booted afterwards.

**To do.**

- Confirm this with the proprietary and the GRID/vGPU drivers, including whether
  they use GSP on this card (the current payload depends on GSP being enabled; see
  item 2).
- Write a Linux guide for this path; today it is documented only from the Windows
  side.
- Optional, longer term: study the proprietary and GRID drivers directly. This is
  still an interesting idea, but it is not needed for the use case above.

## 2. Windows unlock without GSP

**Goal.** Unlock the CMP 40HX on Windows without enabling GSP firmware
(`EnableGpuFirmware=1`).

**Why.**

- GSP mode is reported as unstable on Windows by many users, including me:
  severe freezes while a game is loading (they stop afterwards) and,
  occasionally, a Windows TDR GPU reset during loading. The game then crashes, and
  the reset clears the unlock, which is volatile. The cause has not been isolated.
- With GSP on, the driver reserves some video memory: the reported VRAM drops from
  8191 MB to 8024 MB (see [`docs/results.md`](docs/results.md)).

**Status.** Not available for the 40HX. A Windows unlock without GSP already works
on the Ampere cards (CMP 90HX and 70HX), but not on the Turing cards (CMP 40HX
and 50HX).

**Approach.** The Windows implementation this project inherited is hard to audit:
parts of its behaviour are not documented or not understood, and the 50HX unlock
is built on the same base. A rewrite modelled on the 90HX implementation is likely
needed, rather than extending the inherited code.

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
