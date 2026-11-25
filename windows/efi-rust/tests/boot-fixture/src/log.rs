// SPDX-License-Identifier: MIT
//! Test-only logging and terminal refusal; never reads or writes a GPU.
#![no_std]
#![no_main]
use cmp40_efi::{
    image::protocol,
    output::{Output, Trace},
    terminal,
};
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
    second.seal()?;
    for (name, requested, failed, diagnostic) in [
        ("SUMMARY", false, false, false),
        ("EXPLICIT", true, false, false),
        ("LATE_FAILURE", false, true, false),
        ("ROLLBACK", false, false, true),
    ] {
        let mut output = Output::open("zero")?;
        output.request_trace(requested);
        output.checkpoint(format_args!("LOG_FIXTURE_MODE {}\n", name))?;
        let mut native = Trace::new()?;
        for _ in 0..10000 {
            native.record(b'R', 0x1100f4, 0);
            native.record(b'S', 0, 100);
        }
        native.record(b'R', 0x1100f4, 1);
        native.record(b'C', 0, 0);
        native.record(b'C', 0, 0);
        native.record(b'R', 0x110184, 123);
        native.record(b'W', 0x1101c4, 456);
        if native.last_read(0x1100f4) != Some(1) {
            return Err(Status::DEVICE_ERROR);
        }
        output.retain_trace("CPU_RM", native)?;
        let mut tail = Trace::new()?;
        tail.record(b'W', 0x88050, 6);
        output.retain_trace("REBAR", tail)?;
        // Simulate a later phase's handled failure or best-effort rollback.
        if diagnostic {
            output.diagnostic();
        }
        output.finish_traces(failed)?;
        output.checkpoint(format_args!("LOG_FIXTURE_MODE_DONE\n"))?;
        output.seal()?;
    }
    let mut output = Output::open("zero")?;
    let mut trace = Trace::new()?;
    for _ in 0..65536 {
        trace.record(b'R', 0x1100f4, 0);
        trace.record(b'S', 0, 100);
    }
    trace.record(b'W', 0x88050, 6);
    output.retain_trace("CPU_RM", trace)?;
    output.finish_traces(false)?;
    output.seal()
}
