# Stage 4d: FULL return to Limine and Linux acceptance

Successful FULL return to Limine, subsequent Linux boot, working NVIDIA
and compute unlock are confirmed. The Linux driver is 620.70.06, proprietary,
GSP OFF. No other unlockers were reported.
The supplied [RF000007.LOG](RF000007.LOG) identifies
`HANDOFF_PREPARED parent=true source=None` and `NATIVE_READY Full`.
Together these accept the FULL parent/Linux handoff on the tested CMP40HX
configuration. Details are in [hardware-report.json](hardware-report.json).

The retained log records native GPU completion. It observes the same
Standard target route, memory-r1 DMA layout and all 256 metadata bytes as
the accepted Windows FULL log. The asset manifest also matches. FWSEC observes
GSP HALT after 99 ms of additional waiting, zero FRTS error and expected WPR2
up. Native completion has FULL selectors `88888888:00000008`, Booter mailbox0
zero, SEC2/GSP HALT, stock FEAT PLM `ffffff8f` and WPR2 down at
`00000000:1ffffe00`.

The trace retains 20,499 records with zero drops and 14,943 omitted firmware
data-port accesses. Against [Windows FULL](../stage4c/RF000004.LOG), exactly
two records differ: the PTIMER seed writes. All other retained operations,
allocation events, addresses, delays and barriers match. Omitted firmware
data-port contents are not compared.

The EFI log records native completion before parent return; post-return menu,
Linux driver and compute behavior are reported observations. The report
supplies a driver version/mode and excludes an independent Linux unlocker,
but provides no numeric benchmark, kernel version, new executable checksum
or independent post-OS register dump. Cold power events were not separately
reconfirmed in this report. This accepts the reported behavior without
extending it to other driver/GSP modes, boards, repeated-boot statistics or
suspend/resume. ZERO's Linux compute behavior is not part of this FULL test.

## Baseline and remaining work

The original `fe02bf7` FULL/ZERO images remain frozen unchanged under
`target/baseline-carrier-gpu-fe02bf7/`. Their archive/EFI hashes were rechecked;
no runtime, firmware asset, payload, build or diagnostic logging behavior
changed for this evidence update. The Windows acceptance remains recorded
in [stage 4c](../stage4c/README.md); this Linux follow-up closes the outstanding
carrier/handoff acceptance on the tested configuration.

Stored log excerpts omit obsolete boot-option policy diagnostics. File checksums
describe these excerpts; EFI image hashes and GPU observations remain historical.
