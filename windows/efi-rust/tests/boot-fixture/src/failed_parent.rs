// SPDX-License-Identifier: MIT
#![no_std]
#![no_main]
use cmp40_efi::image::{load_from_volume, protocol};
use uefi::{boot, prelude::*, proto::loaded_image::LoadedImage};

#[entry]
fn main() -> Status {
    if let Err(error) = uefi::helpers::init() {
        return error.status();
    }
    run().map_or_else(
        |status| {
            uefi::println!("CMP40-FIXTURE: FAILED_PARENT_ERROR {:?}", status);
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
    let image = load_from_volume(volume, cstr16!("\\EFI\\CMP40\\FAILED.EFI"))?;
    let options = cstr16!("--park-after-failure");
    {
        let mut loaded = protocol::<LoadedImage>(image)?;
        // SAFETY: Static option bytes remain live until the child is unloaded.
        unsafe {
            loaded.set_load_options(options.as_bytes().as_ptr(), options.num_bytes() as u32);
        }
    }
    uefi::println!("CMP40-FIXTURE: START_FAILURE_CHILD");
    let result = boot::start_image(image);
    uefi::println!("CMP40-FIXTURE: PARENT_RESUMED_AFTER_FAILURE");
    result.map_err(|e| e.status())?;
    Ok(())
}
