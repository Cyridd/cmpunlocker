// SPDX-License-Identifier: MIT
//! Test-only logging and terminal refusal; never reads or writes a GPU.
#![no_std]
#![no_main]
use cmp40_efi::{image::protocol, output::Output, terminal};
use cmp40_efi_core::options::has_load_option;
use uefi::{boot, prelude::*, proto::loaded_image::LoadedImage};

#[entry]
fn main() -> Status {
    if let Err(error) = uefi::helpers::init() {
        return error.status();
    }
    if let Err(error) = boot::set_watchdog_timer(0, 0x10000, None) {
        return error.status();
    }
    if let Err(status) = log_test() {
        uefi::println!("CMP40-FIXTURE: LOG_TEST_FAILED {:?}", status);
        return status;
    }
    uefi::println!("CMP40-FIXTURE: LOG_TEST_READY GPU_WRITES=0");
    let park = protocol::<LoadedImage>(boot::image_handle()).is_ok_and(|image| {
        has_load_option(
            image.load_options_as_bytes().unwrap_or(&[]),
            "--park-after-failure",
        )
    });
    if park {
        uefi::println!("CMP40-FIXTURE: SIMULATED_POST_MUTATION_FAILURE GPU_WRITES=0");
        return terminal::failure(Status::DEVICE_ERROR, true);
    }
    let result = terminal::failure(Status::NOT_FOUND, false);
    if result != Status::NOT_FOUND {
        return Status::DEVICE_ERROR;
    }
    Status::SUCCESS
}
fn log_test() -> Result<(), Status> {
    let mut first = Output::open("zero")?;
    first.checkpoint(format_args!("LOG_FIXTURE_FIRST\n"))?;
    // More than one readback chunk, exercising close/reopen and append offsets.
    for index in 0..160 {
        first.write(format_args!("line={index:03} abcdefghijklmnopqrstuvwxyz\n"))?;
    }
    first.checkpoint(format_args!("LOG_FIXTURE_TAIL\n"))?;
    first.seal()?;
    let mut second = Output::open("zero")?;
    second.checkpoint(format_args!("LOG_FIXTURE_SECOND\n"))?;
    second.seal()
}
