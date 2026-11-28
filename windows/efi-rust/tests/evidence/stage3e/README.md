# Stage 3e: FULL/ZERO physical acceptance

Code checkpoint: `9f0882b` / diagnostic-r2. Both images are confirmed to
work, with FULL compute unlock surviving OS handoff. The supplied logs are
[RF000001.LOG](RF000001.LOG) and [RZ000002.LOG](RZ000002.LOG).
The ELF/EFI image SHA-256 was not separately reconfirmed; provided release
hashes are recorded in [summary.json](summary.json).

Both logs show the same CMP40HX TU106 target, RootBridgeIo Standard route,
DMA addresses and 256 metadata bytes. The metadata matches the actual C
builder. FWSEC first raises WPR2 after three explicit 1 ms waits; the added
completion gate then waits another 99 ms before observing GSP HALT,
scratch0e zero and the expected WPR2 window.

Both complete with Booter mailbox0 zero, GSP/SEC2 HALT, stock FEAT PLM
`ffffff8f` and WPR2 down (`00000000:1ffffe00`). FULL selectors are
`88888888:00000008`; ZERO selectors are `00000000:00000000`.
Each retains 20,499 trace records with zero drops. After omitting PTIMER
writes and selector observations, the two recorded control traces match.
Firmware data-port contents remain omitted, so this comparison covers the
retained control operations only. `DEBUGINFO=deadb00d` is present in both
successful runs and is not a standalone failure verdict.

These results accept the Rust CPU-RM execution milestone on the tested
board and system. The prior early-WPR2 completion defect is supported by the paired
failure/success evidence; the fix's HALT wait and result reads were introduced
together and were not separately ablated.

The reported OS result establishes post-handoff compute behavior. Numeric
benchmarks, driver/OS versions, image hash checks, repeated boot statistics,
suspend/resume and other boards were not supplied. Both logs use `parent=false`;
physical parent-return/Linux compatibility are outside this acceptance.

## Next work

Freeze the accepted binaries and logs, then optimize memory as a separate
variant: build the zero-filled image directly in the radix allocation and
remove the separate 29,339,648-byte dummy allocation (27.98 MiB). This changes
the placement of later DMA buffers, so compare tables/metadata and rerun the
FULL/ZERO hardware checks before accepting that variant. Handle DMA mapping and release policy in separate changes.

Stored log excerpts omit obsolete boot-option policy diagnostics. File checksums
describe these excerpts; EFI image hashes and GPU observations remain historical.
