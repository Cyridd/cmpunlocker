# Licensing and attribution

This project contains code derived from multiple projects. The repository's own
license is the MIT License in [`LICENSE`](../LICENSE); the components below keep
their original terms.

## Linux

- Linux pre-driver module and firmware tooling in `linux/preboot/`
  — derived from [nlzy/turing-unlock](https://github.com/nlzy/turing-unlock)
  commit `417f333263ff169e8f449c931b70db9dfd7b8ef4`, GPL-2.0-only, with
  CMP40HX-specific changes. License copies are in
  [`linux/preboot/licenses/`](../linux/preboot/licenses/).
  The shared published selector payload is MIT. Extracted NVIDIA generated
  source assets retain NVIDIA's MIT terms; the binary GSP firmware retains
  its own license notice, extracted into the prepared assets directory.

- Compute Unlock — originally developed by @sbccc1888 (https://github.com/sbccc1888/cmpunlocker).
  Licensed under the MIT License. Original attribution is preserved.
- PCIe / ReBAR patches - ported from @xrip (https://github.com/xrip/cmp50hx-unlock). 
  Licensed under copyleft-next 0.3.1. Original attribution is preserved.
- NVIDIA open-gpu-kernel-modules remains subject to NVIDIA's applicable open-source license terms.
- CMP 40HX pipeline throttle unlock and related modifications are licensed
  under the MIT License; see [`LICENSE`](../LICENSE) for the copyright notice.
- Optional CMP SKU profiling unlock — adapted from
  [amoghmunikote/cmpunlocker](https://github.com/amoghmunikote/cmpunlocker),
  commits `300127f` and `ee41902`, licensed under MIT.

## Windows / UEFI (`windows/`)

- The independently implemented Rust sources under `windows/efi-rust/` are MIT,
  under this repository's `LICENSE`. It contains no NVIDIA firmware and no
  imported code from `nv-uefi-unlocker`. The EFI carrier depends on uefi-rs
  (MIT OR Apache-2.0) and the dependencies pinned in its Cargo.lock. Host tests
  compile selected functions from the existing C EFI as a reference; this does not relicense
  the inherited C code or third-party inputs listed below.
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
  and remains the property of the ThrottleStop developer.
- The TU106/GA102 firmware blobs under `windows/tools/unlock40x/` are NVIDIA
  firmware images and remain subject to NVIDIA's terms. They are present as
  research inputs to the EFI build.
- Prebuilt executables are no longer committed. The Windows tools are built from
  source by CI and published on GitHub Releases with SHA-256 checksums. The
  patched `libnvidia-glcore.so` is not distributed at all: it is a modified copy
  of NVIDIA's proprietary library, so it is generated locally from the installed
  original copy.
- Remaining fork-specific changes are licensed under the MIT License; see
  [`LICENSE`](../LICENSE) for the copyright notice.

Because the Windows tree redistributes a GPL-3.0 driver and a proprietary
driver, the repository as a whole is not distributable under MIT alone. Honour
each component's terms when redistributing.
