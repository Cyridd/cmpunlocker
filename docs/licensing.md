# Licensing and attribution

This project contains code derived from multiple authors. The repository's own
license is the MIT License in [`LICENSE`](../LICENSE); the components below keep
their original terms.

## Linux

- Compute Unlock — originally developed by @sbccc1888 (https://github.com/sbccc1888/cmpunlocker).
  Licensed under the MIT License. Original attribution is preserved.
- PCIe / ReBAR patches - ported from @xrip (https://github.com/xrip/cmp50hx-unlock). 
  Licensed under copyleft-next 0.3.1. Original attribution is preserved.
- NVIDIA open-gpu-kernel-modules remains subject to NVIDIA's applicable open-source license terms.
- CMP 40HX pipeline throttle unlock and related modifications —
  Copyright (c) 2026 Cyridd, licensed under the MIT License.
- Optional CMP SKU profiling unlock — adapted from
  [amoghmunikote/cmpunlocker](https://github.com/amoghmunikote/cmpunlocker),
  commits `300127f` and `ee41902`, licensed under MIT.

## Windows / UEFI (`windows/`)

- The tree is a fork of [`PZH1gdmu/CMP40HX-Unlock`](https://github.com/PZH1gdmu/CMP40HX-Unlock),
  which builds on [`WildFlash1st/cmp90hx-unlock-for-windows`](https://github.com/WildFlash1st/cmp90hx-unlock-for-windows).
  Upstream terms apply to the inherited code; see [`windows/FORK_CHANGES.md`](../windows/FORK_CHANGES.md)
  for what this fork changed.
- **WinRing0** — `WinRing0x64.sys` and its source under
  `windows/40HX_Gen2_Windows/source/WinRing0-master/` are licensed under the
  **GNU GPL v3.0** (see that directory's `LICENSE`). The source is kept in the
  repository so the corresponding source for the redistributed driver binary is
  available, as GPL-3.0 requires.
- **ThrottleStop** — `ThrottleStop.sys` is a redistributed third-party
  proprietary signed driver. It is not covered by this repository's MIT license
  and remains the property of its author.
- The TU106/GA102 firmware blobs under `windows/tools/unlock40x/` are NVIDIA
  firmware images and remain subject to NVIDIA's terms. They are present as
  research inputs to the EFI build.
- Prebuilt executables are no longer committed. The Windows tools are built from
  source by CI and published on GitHub Releases with SHA-256 checksums. The
  patched `libnvidia-glcore.so` is not distributed at all: it is a modified copy
  of NVIDIA's proprietary library, so it is generated locally from the user's own
  installed copy.
- Remaining fork-specific changes — Copyright (c) 2026 Cyridd, licensed under
  the MIT License.

Because the Windows tree redistributes a GPL-3.0 driver and a proprietary
driver, the repository as a whole is not distributable under MIT alone. Honour
each component's terms when redistributing.
