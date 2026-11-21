# Production logging proposal

The current experimental logger preserves every numbered `RF`/`RZ` file.
Accepted physical logs are approximately 576 to 577 kB (about 563 KiB), so 100
runs occupy roughly 57.7 MB / 55 MiB. On a small Windows ESP, these files can exhaust the space available alongside
boot files. The tested Linux ESP has 4 GiB; distribution must also support
smaller volumes.

Carrier-r1 Windows FULL/ZERO and FULL parent/Linux handoff have physical
acceptance on the tested configuration. Bounded logging is required before
Rust becomes the default image in the Go installer. The following defaults
are proposed; the current logger still preserves every existing file.

## Proposed defaults

- A successful normal run stores a compact summary, targeting 1 to 4 KiB: image
  version/control, target/route, completion phase, FWSEC wait/result, observed
  native state and handoff mode. It uses observations already captured by the
  transaction and adds no GPU reads.
- A failed transaction saves the retained control trace. An explicit diagnostic
  flag can request the same trace for a successful run. Firmware data ports
  remain omitted and counted, as in the accepted diagnostic format.
- Retain up to 32 compact summaries and up to four full control traces, subject
  to a proposed 8 MiB total budget. The byte budget takes precedence over
  the file-count limits, so larger traces retain fewer previous files.
- Rotate only files verified as belonging to the project's log format. Never
  reclaim space from boot managers, configuration, backups or unrelated files.
  Account for temporary/checkpoint files in the budget; migration of existing
  experimental logs requires an explicit migration path, with export available.
- Keep a previous verified summary until its replacement is fully written and
  verified. Select retained failures by generation, not by an unreliable EFI
  clock alone. A failed write cannot label a partial file as a valid record.

## Required validation

Preserve the accepted execution contract: preallocate trace RAM, perform no
filesystem or console calls inside `Transaction::run`, flush/close logs before
handoff, and retain the parked no-retry/no-reset GPU failure path. Changing log
volume must not reintroduce the former dependency on incidental printing time
before FWSEC completion.

Check a small ESP with hundreds of simulated success/failure runs, file-count
and byte bounds, variable trace sizes, interrupted replacement, invalid/stale
files, I/O errors and `VOLUME_FULL`. Verify that non-log files survive, recent
failure diagnostics remain recoverable within the configured budget, and no
temporary file escapes quota accounting. The Go checker/uninstaller must
recognize the new summaries and ownership rules before distribution switches.

Finalize filenames and limits during implementation. The default must bound
log storage and retain enough evidence to diagnose failures.
