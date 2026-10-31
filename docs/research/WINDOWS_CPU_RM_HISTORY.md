# Windows CPU-RM investigation: useful negative controls

These October 2026 experiments explain how the search narrowed before the
successful [small-WPR handoff](CPU_RM_SMALL_WPR.md). Code 43 remained
unresolved on 2026-10-07; I later confirmed successful ZERO/FULL 960 KiB
initialization and FULL unlock.

The earlier stand was CMP 40HX / TU106 on MSI MPG B550 Gaming Plus. Older
tests used KMD 616.92; later controls and Windows traces used 617.14.

| Control | Observed result | Implication |
| --- | --- | --- |
| Stock, no unlock EFI, normal ACR, GSP OFF | Native driver works | CPU-RM itself is supported on this card |
| FULL with GSP ON | Unlock retained | Payload can restore the selectors |
| Final FLR before Windows | Code 43 disappears; unlock lost | FLR is not a completed CPU-RM unlock |
| EARLY through FWSEC / BOOTLOAD cut points, GSP OFF | Driver works | Failure begins after Booter execution |
| BOOTRUN FULL and ZERO, GSP OFF, old heap | Both fail with Code 43 | Nonzero selectors are not necessary for the failure |
| Independent PLM/reset-nibble/WPR2 cleanup controls | Failure remains | Those visible-state repairs are insufficient |
| FWSEC-SB restore, FULL and ZERO | FWSEC completes; Code 43 remains | SB restoration alone is insufficient |
| ACR disabled, GSP OFF, including no unlock EFI | PMU halt timeout / Code 43 | PMU is not a working bypass on this stand |

The trace localized the primary failure to Windows ACR waiting about
**60 seconds** for SEC2 `CPUCTL.HALTED`; total observed initialization was
about 62 seconds. NVIDIA NvLog corroborated the timeout path and outer
`NV_STATUS=0x65`. Later register dumps came from cleanup after the failure,
so their zeros are not snapshots of the initial fault. The PMU timeout with
ACR disabled was a distinct control and must not be conflated with the
primary SEC2 failure under normal ACR.

I corrected some initial observations about stock/GSP OFF, compute retention
and ReBAR through repeat testing. Code 43 alone does not
identify signature rejection, BROM failure or a particular MMIO register.
The successful Ampere CMP90HX CPU-RM implementation was useful as a control
flow reference; its registers and firmware layout were not assumed to apply
to TU106.

The shipped solution keeps normal ACR and changes the temporary Booter heap
placement.
