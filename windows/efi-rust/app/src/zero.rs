// SPDX-License-Identifier: MIT
#![no_std]
#![no_main]
use cmp40_efi_core::state::Control;
use uefi::prelude::*;
#[entry]
fn main() -> Status {
    cmp40_efi::cpu_rm::entry(Control::Zero)
}
