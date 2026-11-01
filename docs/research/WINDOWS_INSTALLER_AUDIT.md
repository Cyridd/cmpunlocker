# Windows installer audit for v2.1.1

## Findings and changes

- **Duplicate Gen2 execution.** `windows.CreateMutex` returns
  `ERROR_ALREADY_EXISTS` together with a valid handle. The old generic
  error branch allowed execution anyway and leaked that handle. The
  duplicate branch now closes it and exits; other lock failures also exit.
  The instance guard uses the named object's lifetime, without depending
  on thread ownership across migrating Go goroutines. The resident watchdog
  has its own instance guard and stops when the resident policy is removed.
- **Handle cleanup.** ThrottleStop was not closed if opening WinRing0
  failed. Cleanup is now registered immediately after ThrottleStop opens;
  PnP reopening clears the old handle values before trying replacements.
- **External commands.** Ordinary commands now have a 30-second timeout,
  Defender calls 60 seconds and PnP recovery 90 seconds. A two-second
  `WaitDelay` bounds output-pipe draining. PowerShell recovery also runs
  noninteractively with a hidden window. These are individual command
  limits, not a total Gen2 deadline, and do not cancel a kernel operation
  already submitted to a driver or service.
- **NVIDIA readiness.** The wait queries SCM directly with read access and
  numeric service states, avoiding repeated `sc.exe` launches and localized
  text parsing. Missing/stopped NVIDIA or a readiness timeout now skips Gen2
  before driver deployment or PCIe writes. Unknown watchdog TLS reads no
  longer trigger a complete recovery attempt.
- **PCI discovery.** A validated registry BDF is tried first, including
  buses above 7. Fallback scanning skips absent and single-function devices.
  A known target takes one PCI read; an empty full scan takes 8,192 instead
  of 65,536, while retaining high-bus and multifunction coverage.
- **ESP probing.** Occupied drive letters are skipped using the Windows
  drive mask. A failed mount is no longer accepted just because that drive
  already exists, and a missing ESP or mount failure does not launch the
  same command for all seven candidate letters.
- **Idle logging and deployment.** Windows write-through logging replaces
  an unconditional flush every 100 ms. Defender exclusions are applied
  once per Gen2 driver preparation instead of once for each driver.
  Both Gen2 buttons are disabled during the initial environment scan.

The ordinary installer GUI remains open until closed. A scheduled instance
using the explicitly selected resident-watchdog policy intentionally remains
running; the default unload-after-use policy is one-shot. The source contains
no compute benchmark or GPU stress workload in these installer paths. That
does not identify or exclude a driver/firmware cause of the reported heat.
