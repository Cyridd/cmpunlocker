// SPDX-License-Identifier: MIT
#![no_std]
#![no_main]

use cmp40_efi::{handoff, policy};
use uefi::boot;
use uefi::prelude::*;

mod pci;

#[entry]
fn main() -> Status {
    if let Err(error) = uefi::helpers::init() {
        return error.status();
    }
    uefi::println!(
        "CMPUNLOCKER-READONLY: BEGIN version={} GPU_WRITES=0",
        env!("CARGO_PKG_VERSION")
    );
    if let Err(error) = boot::set_watchdog_timer(0, 0x10000, None) {
        uefi::println!(
            "CMPUNLOCKER-READONLY: WATCHDOG_DISABLE_FAILED {:?}",
            error.status()
        );
        return error.status();
    }
    let options = match policy::options() {
        Ok(options) => options,
        Err(status) => {
            uefi::println!("CMPUNLOCKER-READONLY: OPTIONS_REFUSED {:?}", status);
            return status;
        }
    };
    if let Err(status) = pci::inventory() {
        uefi::println!("CMPUNLOCKER-READONLY: PCI_INVENTORY_FAILED {:?}", status);
        return status;
    }
    uefi::println!("CMPUNLOCKER-READONLY: INVENTORY_DONE GPU_WRITES=0");
    if options.return_to_bootloader {
        uefi::println!("CMPUNLOCKER-READONLY: RETURN_PARENT GPU_WRITES=0");
        return Status::SUCCESS;
    }
    let image = match handoff::windows() {
        Ok(image) => image,
        Err(status) => {
            uefi::println!(
                "CMPUNLOCKER-READONLY: WINDOWS_PREPARE_FAILED {:?} GPU_WRITES=0",
                status
            );
            return status;
        }
    };
    uefi::println!("CMPUNLOCKER-READONLY: START_WINDOWS GPU_WRITES=0");
    let status = boot::start_image(image).map_or_else(|error| error.status(), |()| Status::SUCCESS);
    // A real OS normally never returns. Keep returned child status visible.
    uefi::println!("CMPUNLOCKER-READONLY: WINDOWS_RETURNED {:?}", status);
    // EFI applications are unloaded by firmware when they exit. Do not use
    // their old handle after StartImage returns.
    status
}
