// SPDX-License-Identifier: MIT
#![no_std]
#![no_main]
use uefi::prelude::*;
#[entry]
fn main() -> Status {
    cmp40_efi::unlock::zero_entry()
}
