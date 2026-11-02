# Windows disk and PCI safety

[Issue #19](https://github.com/Cyridd/cmpunlocker/issues/19) reports disk I/O
errors after unlock. Verified by source inspection: the executable unlock
paths contain no raw-disk writes, partition edits, formatting, filesystem
repair commands or AHCI resets. PCI resource changes and link resets can
still affect storage indirectly. The reported failure has not been reproduced
on the affected system, so its cause remains unresolved.

## File I/O boundaries

| Component | Operations | Target |
| --- | --- | --- |
| EFI logger | Replace, write, flush and close `\40hx_log.txt` | Filesystem containing the loaded EFI image |
| Windows handoff | `LoadImage` and `StartImage` for `\EFI\Microsoft\Boot\bootmgfw.efi` | The Windows loader is prepared before GPU mutation; another filesystem is tried if necessary |
| Installer | Deploy primary/fallback EFI images, backup and ReBAR policy | System ESP mounted with `mountvol /S` |
| Gen2 and checker | Deploy helper drivers; write status and diagnostic logs | Windows driver directory and project log directories |
| Uninstaller and USB helper | Remove project files or restore a verified fallback backup | System ESP or the explicitly selected USB drive |

Verified: the shipped CPU-RM entry does not call the historical FAT32,
BlockIo or GA102 disk-preload helpers. EFI logging uses firmware file services
and flushes each line. It has no fixed 8 KiB length and is not a raw-sector
write. A firmware filesystem error, failing storage or interrupted FAT
metadata update can still prevent durable deployment.

EFI replacement writes a unique staging file in the destination directory,
checks write/flush/close results, verifies its complete contents and uses a
same-volume replacement rename. Backup creation copies and verifies the
original without moving it or overwriting an existing backup. Restore retains
the backup until exact readback succeeds. Empty backup sources are refused.
The USB helper stops on backup or restore errors. An uninstall without a
backup removes the fallback only if it exactly matches the installed unlock.

The two EFI paths are separate file operations. Their verification protects
against ordinary copy and rename failures; it does not make FAT32 updates
atomic across power loss. Occupied drive letters are skipped, and a failed
ESP mount does not select a data volume as a substitute.

## Gen2 PCI boundaries

Verified: mutating root-port operations require the present Windows PCI
inventory, matching live identities, a direct secondary/subordinate bus and
PCIe Root Port type. Shared storage, PCIe switches, ambiguous addresses,
incomplete inventory or failed reads prevent root-port writes. Only the
target GPU's non-storage, non-bridge NVIDIA functions may share that direct
bus. GPU preparation remains available when an exclusive root cannot be
established.

BAR0 must be read and validated; the code does not substitute a fixed address
or truncate an unexpected 64-bit BAR. PCI control updates require successful
reads and use WORD writes to preserve the W1C Status half. Short driver reads
are rejected, and PL0 preparation checks write results and readback.

Link Disable uses the verified pre-disable snapshot for its clear operation.
TLS is configured first, and no GPU access occurs while the link is disabled.
Clear is retried once and checked through root configuration readback. Failure
stops the fallback before another GPU transaction. PnP recovery attempts to
reenable the GPU in `finally` and rediscovers its address before proceeding.
No storage-controller reset is part of this path.

## Experimental ReBAR relocation

The current allocator changes BAR1 size, moves BAR1/BAR3 and replaces the
immediate bridge's prefetchable window. Verified by source inspection: it
does not establish complete platform resource ownership or routing.

- An address absent from the EFI memory map can still be reserved or outside
  the root bridge's grant.
- The conflict scan does not cover every endpoint BAR, non-prefetch window
  and ancestor resource.
- Replacing one bridge window can affect siblings, and does not configure
  higher ancestors behind a switch.
- The allocator changes ReBAR Control before clearing Memory Decode, does
  not fully quiesce DMA, uses DWORD Command writes and does not verify every
  write or rollback step.

These gaps do not establish that the allocator caused the reported disk
failure. Two successful aperture reads also do not prove that an entire
8 GiB window maps VRAM correctly.

Relocation is disabled by default, including full installation and
USB/fallback boot. An explicit `--experimental-rebar` token or enabled ESP
policy opts in; `norebar` overrides both. Compute and Gen2 are independent of
ReBAR relocation.

BCD loadoptions readback alone does not prove that firmware passes a UTF-16
command line: Windows-created boot entries can contain a binary `WINDOWS`
record. The installer therefore also writes the exact ASCII policy
`CMP40HX_REBAR_V1=1` or `CMP40HX_REBAR_V1=0`, followed by one LF, to
`\EFI\40HX\REBAR.CFG`. Enabling requires matching policy-aware primary
and fallback EFI images. The file is flushed and verified.

The EFI reads policy once from its own loaded-image volume before GPU
discovery. Missing, unreadable, malformed or unknown-version files supply no
opt-in. Installer success confirms saved configuration; actual activation
requires the EFI result and BAR1 readback in Windows. Boot-entry creation
preserves existing order and appends new entries. Boot priority is configured
manually.

## Conservative standalone-EFI design

Hypothesis: a replacement should prefer resources already allocated for the
GPU, and skip ReBAR before register writes whenever routing, ownership,
configuration access or firmware use cannot be established. A new allocation
would also need an explicit grant for the matching root bridge, consistent
address translation and exact reservation of existing DXE GCD MMIO. This
design has not been implemented or tested on hardware.

Verified: `PciRootBridgeIo.Configuration()` reports current root resources,
not all free routable space. GCD records MMIO type and ownership, but a free
descriptor alone does not identify which root bridge routes it. A gap must
not be added with `AddMemorySpace()` to manufacture permission. Space inside
a bridge window is not automatically allocated to the GPU. Dynamic ACPI
`_CRS` methods also require evaluation; a plausible range in a DSDT is
insufficient evidence.

One tested stock bridge window is 96 MiB, containing 64 MiB BAR1 and 32 MiB
BAR3. It cannot contain an 8 GiB BAR1. A strict existing-window policy would
skip full-size ReBAR on that layout.

Verified: the current Control write uses BAR0 MMIO, which becomes inaccessible
when GPU Memory Decode is cleared. A replacement must use extended PCI
configuration access for this write. The current `U40X_CFG_ADDR()` supports
low offsets only; reusing it for `0xbc0` corrupts the function field. EDK II's
`EFI_PCI_ADDRESS()` uses bits 63:32 for extended offsets. The current CF8/CFC
fallback masks offsets with `0xfc` and cannot provide that access. Capability
discovery and configuration/MMIO agreement must be checked before mutation.

The proposed transaction would prepare its plan, reservation and rollback
snapshot first; establish GPU ownership and DMA quiescence; expose the size
list while BAR0 is accessible; then clear decode and resize through
configuration space. All Command writes would be WORD-sized. Decode and
Bus Master would return to their original states only after complete
readback. Rollback would restore the original size and resources before
releasing a new reservation. A failure after mutation requires a separate
recovery result and cannot be reported as a safe skip. Bounded retries do
not guarantee that a hardware or firmware call cannot stall.

Verified: EDK II's PCI I/O implementation caches BAR base and length.
`SetBarAttributes()` changes attributes within an existing BAR; it is not a
resize service. GCD reservation does not update these records or ACPI.
Both relocation and expansion in place must resolve their lifetime through
handoff without editing private PciBusDxe structures. No portable service for
that reconciliation was identified in the inspected interfaces.

ReBarUEFI and NvStrapsReBar run before firmware BAR collection from an early
DXE module integrated into BIOS. A normal boot-entry application executes
too late to reproduce that enumeration pass. Restarting the global PCI
allocator would also involve storage and boot devices.

| Inspected source | Revision |
| --- | --- |
| [EDK II](https://github.com/tianocore/edk2/tree/a89ea171d7c57018d5534777f9c0c6fb98e57e13) | `a89ea171d7c57018d5534777f9c0c6fb98e57e13` |
| [ReBarUEFI](https://github.com/xCuri0/ReBarUEFI/tree/9c80fdb2cd3db94bdd19c58bd00d5ecf822f6430) | `9c80fdb2cd3db94bdd19c58bd00d5ecf822f6430` |
| [NvStrapsReBar](https://github.com/terminatorul/NvStrapsReBar/tree/5ce12bb5585c19cd54eefe670910ebf19793586d) | `5ce12bb5585c19cd54eefe670910ebf19793586d` |
| [Linux PCI ReBAR](https://github.com/torvalds/linux/blob/6628e7d4c6b25c8d18f1345e3a20d21829aae7e4/drivers/pci/rebar.c) | `6628e7d4c6b25c8d18f1345e3a20d21829aae7e4` |

## Validation limits

Verified: offline checks exercised the production ReBAR option/policy parser
with stub firmware services, file-replacement failures, PCI identity and
topology rejection, WORD-only controls and bounded Link Disable recovery.
FULL/ZERO EFI builds and selector/metadata checks completed, as did Windows
cross-builds, vet and core/installer unit tests under Wine. Compute, Gen2 and
ReBAR were confirmed with GPU-Z and `40HXCheck.exe` on one Windows system.
The address-macro harness passed all 65,536 segment-local BDF combinations.
These results do not validate the proposed resource allocator.

The PowerShell USB test was not run locally because the Linux environment
has no Windows PowerShell. Wine and mocks do not validate real driver IOCTLs,
firmware services, FAT32 durability or the affected system's storage failure.
The useful hardware comparison remains a cold boot with ReBAR and Gen2
auto-start disabled, followed by Gen2 with the stock BAR layout. A future
read-only EFI preflight should capture root grants, GCD ownership, PCI I/O
BAR records, driver bindings and the full PCI path before any resize is
considered.
