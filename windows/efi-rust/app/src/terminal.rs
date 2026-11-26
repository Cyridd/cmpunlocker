// SPDX-License-Identifier: MIT
//! A failed GPU attempt must not return control to a boot manager's fallback.
use uefi::Status;

pub fn failure(status: Status, gpu_attempted: bool) -> Status {
    if !gpu_attempted {
        return status;
    }
    uefi::println!("CMPUNLOCKER: FAILURE_PARKED {:?} GPU_ATTEMPTED=1", status);
    uefi::println!("Power off physically. Do not continue Windows or retry this boot.");
    loop {
        // SAFETY: x64 EFI runs at firmware privilege. The watchdog has been
        // disabled. CLI/HLT parks this attempt without executing a GPU reset,
        // OS handoff, firmware boot fallback or warm reboot. No allocations
        // are freed; recovery requires fully powering off the machine.
        unsafe {
            core::arch::asm!("cli", "hlt", options(nomem, nostack));
        }
    }
}
