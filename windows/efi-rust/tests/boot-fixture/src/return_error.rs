// SPDX-License-Identifier: MIT
//! QEMU-only child for an actual StartImage error; no hardware access.
#![no_std]
#![no_main]
use uefi::prelude::*;
#[entry]
fn main() -> Status {
    if let Err(error) = uefi::helpers::init() {
        return error.status();
    }
    uefi::println!("CMP40-FIXTURE: RETURN_DEVICE_ERROR GPU_WRITES=0");
    Status::DEVICE_ERROR
}
