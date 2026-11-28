// SPDX-License-Identifier: MIT
//! Physical filesystem diagnostic; no PCI discovery, MMIO or firmware launch.
#![no_std]
#![no_main]
use cmp40_efi::output::Output;
use uefi::{boot, prelude::*};

#[entry]
fn main() -> Status {
    if let Err(error) = uefi::helpers::init() {
        return error.status();
    }
    uefi::println!("CMPUNLOCKER-LOGCHECK: BEGIN GPU_WRITES=0");
    if let Err(error) = boot::set_watchdog_timer(0, 0x10000, None) {
        return error.status();
    }
    run().map_or_else(
        |status| {
            uefi::println!("CMPUNLOCKER-LOGCHECK: FAILED {:?} GPU_WRITES=0", status);
            status
        },
        |()| Status::SUCCESS,
    )
}
fn run() -> Result<(), Status> {
    let mut output = Output::open("logcheck")?;
    output.checkpoint(format_args!(
        "CMPUNLOCKER-LOGCHECK: FILE_WRITE_TEST GPU_WRITES=0\n"
    ))?;
    output.checkpoint(format_args!("CMPUNLOCKER-LOGCHECK: READY GPU_WRITES=0\n"))?;
    output.seal()?;
    uefi::println!("CMPUNLOCKER-LOGCHECK: LOG_CHECK_READY GPU_WRITES=0");
    Ok(())
}
