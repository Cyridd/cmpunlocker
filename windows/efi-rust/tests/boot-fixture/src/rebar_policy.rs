// SPDX-License-Identifier: MIT
//! Test-only parent for the real CPU-RM policy reader, without a GPU.
#![no_std]
#![no_main]
use cmp40_efi::image::{load_from_volume, protocol};
use uefi::{
    boot,
    prelude::*,
    proto::{
        loaded_image::LoadedImage,
        media::{
            file::{File, FileAttribute, FileMode},
            fs::SimpleFileSystem,
        },
    },
};

#[entry]
fn main() -> Status {
    if let Err(error) = uefi::helpers::init() {
        return error.status();
    }
    run().map_or_else(
        |status| {
            uefi::println!("CMP40-FIXTURE: REBAR_POLICY_FAILED {:?}", status);
            status
        },
        |()| Status::SUCCESS,
    )
}
fn run() -> Result<(), Status> {
    boot::set_watchdog_timer(0, 0x10000, None).map_err(|e| e.status())?;
    let volume = protocol::<LoadedImage>(boot::image_handle())?
        .device()
        .ok_or(Status::NOT_FOUND)?;
    let mut fs = protocol::<SimpleFileSystem>(volume)?;
    let mut root = fs.open_volume().map_err(|e| e.status())?;
    let mut file = root
        .open(
            cstr16!("\\MODE.TXT"),
            FileMode::Read,
            FileAttribute::empty(),
        )
        .map_err(|e| e.status())?
        .into_regular_file()
        .ok_or(Status::UNSUPPORTED)?;
    let mut mode = [0; 32];
    let bytes = file.read(&mut mode).map_err(|e| e.status())?;
    let options = match &mode[..bytes] {
        b"literal" => cstr16!("--experimental-rebar"),
        b"optout" => cstr16!("--experimental-rebar norebar"),
        _ => cstr16!(""),
    };
    let child = load_from_volume(volume, cstr16!("\\EFI\\CMP40\\FULL.EFI"))?;
    {
        let mut loaded = protocol::<LoadedImage>(child)?;
        // SAFETY: Static options outlive child StartImage and unload.
        unsafe { loaded.set_load_options(options.as_bytes().as_ptr(), options.num_bytes() as u32) };
    }
    // Exercise the fallible memory-map boundary on OVMF before child entry.
    cmp40_efi::rebar::check_memory_map()?;
    uefi::println!("CMP40-FIXTURE: REBAR_MEMORY_MAP_OK GPU_WRITES=0");
    uefi::println!("CMP40-FIXTURE: START_REBAR_POLICY_CHILD");
    let status = boot::start_image(child)
        .err()
        .map_or(Status::SUCCESS, |e| e.status());
    if status != Status::NOT_FOUND {
        if status == Status::SUCCESS {
            boot::unload_image(child).map_err(|e| e.status())?;
        }
        return Err(Status::DEVICE_ERROR);
    }
    uefi::println!("CMP40-FIXTURE: REBAR_POLICY_CHILD_RETURNED NOT_FOUND GPU_WRITES=0");
    Ok(())
}
