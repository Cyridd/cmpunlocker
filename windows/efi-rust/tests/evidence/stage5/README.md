# Stage 5: ReBAR-r1 FULL/ZERO acceptance

On 2026-11-25 I tested ZERO and FULL with the enabled `REBAR.CFG`, first with
`norebar` in the command line and then with an empty command line. I noticed
no errors. Compute unlock survived OS handoff in both FULL runs. In both
ReBAR-enabled runs, ZERO and FULL, `nvidia-smi -q` and GPU-Z showed 8192 MB
BAR1. I observed about 10% higher game FPS than with the stock 64 MB BAR1.
These are my reported OS observations; no game names, raw FPS samples, driver
version or OS version were supplied for this record.

The four logs support acceptance of ReBAR-r1 on this tested configuration.
The [hardware report](hardware-report.json) separates log inspection from
reported OS behavior. The stored `.LOG` files are checkpoint excerpts:
device topology, buffer records and full operation traces are omitted, and
the output is redacted. Their checksums describe the excerpts. Complete logs
were inspected before these excerpts were prepared.

| Checkpoint excerpt | Control | Reported command line | Effective policy | EFI ReBAR result |
| --- | --- | --- | --- | --- |
| [RF000001.LOG](RF000001.LOG) | FULL | `norebar` | false | Skipped |
| [RZ000001.LOG](RZ000001.LOG) | ZERO | `norebar` | false | Skipped |
| [RF000002.LOG](RF000002.LOG) | FULL | empty | true | Active, 8 GiB BAR1 |
| [RZ000002.LOG](RZ000002.LOG) | ZERO | empty | true | Active, 8 GiB BAR1 |

## Verified log inspection

All four complete logs end at `HANDOFF_READY` after `TRANSACTION Ready`,
`failed_at=None` and `result=Ok(())`. Windows preload uses `RawBuffer` with
`parent=false`. FULL selectors are `88888888:00000008`; ZERO selectors are
`00000000:00000000`. Native completion has WPR2 `00000000:1ffffe00`, stock
FEAT PLM `ffffff8f`, Booter mailbox0 zero and halted SEC2/GSP. FWSEC completion
waits an additional 99 ms for GSP HALT, with zero FRTS error and WPR2 up.

Each CPU-RM trace retains 20,499 records, omits 14,943 firmware data-port
accesses and has zero drops. The recorded asset manifests match the current
source inputs and derived ZERO/boot-prefix/signature bytes. All 256 metadata
bytes in each of the four logs match the actual C builder at the recorded DMA
addresses. Comparing the two FULL CPU-RM traces, or the two ZERO traces,
finds exactly two changed records: PTIMER seed writes. Every other retained
CPU-RM record is identical within its control. Omitted data-port contents
were not compared.

The enabled ZERO/FULL ReBAR traces are identical across all 111 retained
records, with zero drops. Both observe the 8 GiB BAR1 sizing mask
`0000000c:fffffffe`, a 32 MiB BAR3, matching BAR1 readback and two zero VRAM
samples. Placement succeeds at attempt 0: BAR1 base `0x600000000`, BAR3 base
`0x800000000` and bridge-window limit `0x801ffffff`. The `norebar` logs contain
the skip checkpoint and no ReBAR transaction, despite the reported enabled
file being present. Neither enabled run exercises rollback.

## Accepted pair and scope

The local candidate archive and images retain the delivered hashes:

| Artifact | SHA-256 |
| --- | --- |
| Test ZIP | `613fd6c17dd5963f114566b48dac76656de6f7d606ee894509774f7bb2b3a46e` |
| FULL EFI | `50b8974e4af13630e98b91c1008fa8e3b8dfe2a548a5edb83f06d0ad549f6473` |
| ZERO EFI | `b5d0657194fc1f86f590d86a88f38c5bf8dce9e9f6bbc01bd96cea2217dddd1b` |

The original package and both images were preserved locally without rebuilding
or replacing earlier accepted variants. Its embedded guide and checksums also
remain unchanged. Assumed: the supplied physical runs used this delivered
pair; the executed EFI hashes were not separately reconfirmed.

This accepts native FULL/ZERO, `norebar` precedence with the file present,
reported OS-visible 8 GiB BAR1 in both enabled controls and retained FULL
compute unlock. The FPS observation is specific to the reported tests and
does not establish a general 10% gain. This follow-up does not establish
physical rollback, parent/Linux handoff with ReBAR, other boards/drivers,
long-run stability or suspend/resume. Cold power events were not separately
confirmed. Earlier carrier-r1 parent/Linux acceptance remains independent.

No runtime source, firmware input, payload or installer image changed for
this acceptance update. Bounded production logging and Go distribution/log
support remain the next integration work.
