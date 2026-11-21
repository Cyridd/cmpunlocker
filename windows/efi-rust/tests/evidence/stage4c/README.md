# Stage 4c: carrier-r1 FULL/ZERO Windows acceptance

The original carrier-r1 ZERO and FULL images are confirmed to work:
Windows and NVIDIA start in both cases, and FULL compute unlock was checked
in Windows. The supplied [RZ000003.LOG](RZ000003.LOG) and
[RF000004.LOG](RF000004.LOG) verify native GPU completion with
`HANDOFF_PREPARED parent=false source=Some(RawBuffer)`. This accepts the
combined GPU/carrier Windows path on the tested CMP40HX TU106 configuration.
Details are recorded in [hardware-report.json](hardware-report.json).

## Verified physical observations

Both logs have 576,812 bytes and identify `0000:10:00.0`, Standard route and
BOOT0 `166000a1`. The radix remains in place with a 29,339,648-byte reserved
memory reduction. Payload, Booter, boot image, radix and metadata retain the
accepted memory-r1 addresses; all 256 metadata bytes match the actual C
builder using GCC and Clang and the earlier memory-r1 FULL metadata. The
pinned asset manifests also match.

Both observe GSP HALT after 99 ms of additional FWSEC completion waiting,
zero FRTS error and WPR2 up at `01ffee00:01ffe000` before Booter. Booter
mailbox0 is zero; native completion shows SEC2/GSP HALT, stock FEAT PLM
`ffffff8f` and WPR2 down at `00000000:1ffffe00`. FULL selectors are
`88888888:00000008`; ZERO selectors are `00000000:00000000`.

Each retains 20,499 records, 14,943 omitted firmware data-port accesses and
zero drops. FULL differs from accepted memory-r1 FULL at exactly two retained
records, the PTIMER seed writes. ZERO/FULL differ at eight records: those two
timer writes and six expected selector reads. All other retained records,
including allocation events, DMA addresses, delays and barriers, match.
These comparisons do not include omitted firmware data-port contents.

The existing actual-C harness verified physical metadata; log checks and
original/frozen artifact hashes also passed. This acceptance update changed
only evidence and documentation. Runtime source, payload and image bytes
were preserved, so builds and QEMU/core tests were not repeated.

## Frozen artifacts and remaining acceptance

| Control | EFI SHA-256 |
| --- | --- |
| ZERO | `fab513e7116f86c2de333f7b6050e5083d5e2671347b981ca1935416a0362978` |
| FULL | `8cbc06100323032dd75654f70103c925ae886a863876cdd2df0f595b29176735` |

Both successful GPU logs have `parent=false`. The accepted read-only return
to Limine/Linux does not establish return after GPU execution or Linux compute
behavior. The subsequent [stage-4d record](../stage4d/README.md) accepts FULL
return to Limine and Linux compute with driver 620.70.06, proprietary, GSP
OFF, without another unlocker. `RF000007.LOG` supplies the GPU parent-return
checkpoint and native FULL completion. See the
[GPU procedure](../../CARRIER_GPU_HARDWARE.md) for repetition.

Stored log excerpts omit obsolete boot-option policy diagnostics. File checksums
describe these excerpts; EFI image hashes and GPU observations remain historical.
