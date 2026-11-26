// SPDX-License-Identifier: MIT
#![no_std]
#![no_main]
use uefi::prelude::*;
#[entry]
fn main() -> Status {
    cmp40_efi::unlock::gen2_entry(cmp40_efi_core::gen2::Action::Retrain)
}
