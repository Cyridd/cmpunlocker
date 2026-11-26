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
use uefi::proto::media::{
    file::{File, FileAttribute, FileMode},
    fs::SimpleFileSystem,
};
use uefi::{boot, prelude::*, proto::loaded_image::LoadedImage};

#[entry]
fn main() -> Status {
    if let Err(error) = uefi::helpers::init() {
        return error.status();
    }
    if let Err(error) = boot::set_watchdog_timer(0, 0x10000, None) {
        return error.status();
    }
    let rotate = (|| {
        let volume = protocol::<LoadedImage>(boot::image_handle())?
            .device()
            .ok_or(Status::NOT_FOUND)?;
        let mut fs = protocol::<SimpleFileSystem>(volume)?;
        let mut root = fs.open_volume().map_err(|e| e.status())?;
        Ok::<_, Status>(
            root.open(
                cstr16!("\\ROTATE.TXT"),
                FileMode::Read,
                FileAttribute::empty(),
            )
            .is_ok(),
        )
    })()
    .unwrap_or(false);
    if let Err(status) = if rotate { retention_test() } else { log_test() } {
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
    let mut metadata = [0; 256];
    for (index, byte) in metadata.iter_mut().enumerate() {
        *byte = index as u8;
    }
    for (index, bytes) in metadata.chunks(32).enumerate() {
        first.hex_line(format_args!("META {:02x} ", index * 32), bytes)?;
    }
    first.checkpoint(format_args!("LOG_FIXTURE_TAIL\n"))?;
    if first.physical_writes() > 4 {
        return Err(Status::DEVICE_ERROR);
    }
    uefi::println!(
        "CMP40-FIXTURE: LOG_BATCH_WRITES={}",
        first.physical_writes()
    );
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

fn retention_test() -> Result<(), Status> {
    // More than the retained summary count; every file is independently sealed.
    for index in 0..40 {
        let mut output = Output::open("full")?;
        output.write(format_args!("RETENTION_SUMMARY {}\n", index))?;
        for _ in 0..20 {
            output.write(format_args!("small summary padding\n"))?;
        }
        output.seal()?;
    }
    // Trace count and byte quota compete; three 2.5 MiB traces fit, four do not.
    for index in 0..6 {
        let mut output = Output::open("full")?;
        output.request_trace(true);
        output.write(format_args!("RETENTION_TRACE {}\n", index))?;
        for _ in 0..81920 {
            output.write(format_args!("0123456789abcdef0123456789abcde\n"))?;
        }
        output.finish_traces(false)?;
        output.seal()?;
    }
    // A hard stop after a checkpoint has ownership but no completion footer.
    let mut output = Output::open("full")?;
    output.checkpoint(format_args!("RETENTION_INTERRUPTED\n"))?;
    drop(output);
    let mut output = Output::open("full")?;
    output.write(format_args!("RETENTION_AFTER_INTERRUPT\n"))?;
    output.seal()?;
    uefi::println!("CMP40-FIXTURE: RETENTION_READY GPU_WRITES=0");
    Ok(())
}
