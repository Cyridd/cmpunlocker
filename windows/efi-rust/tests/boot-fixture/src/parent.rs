// SPDX-License-Identifier: MIT
#![no_std]
#![no_main]

use cmp40_efi::image::{load_from_volume, protocol};
use uefi::boot;
use uefi::prelude::*;
use uefi::proto::loaded_image::LoadedImage;

#[entry]
fn main() -> Status {
    if let Err(error) = uefi::helpers::init() {
        return error.status();
    }
    run().map_or_else(
        |status| {
            uefi::println!("CMPUNLOCKER-FIXTURE: PARENT_FAILED {:?}", status);
            status
        },
        |()| Status::SUCCESS,
    )
}

fn run() -> Result<(), Status> {
    boot::set_watchdog_timer(0, 0x10000, None).map_err(|error| error.status())?;
    let volume = protocol::<LoadedImage>(boot::image_handle())?
        .device()
        .ok_or(Status::NOT_FOUND)?;
    let image = load_from_volume(volume, cstr16!("\\EFI\\CMP40\\READONLY.EFI"))?;
    let options = cstr16!("--return-to-grub");
    {
        let mut loaded = protocol::<LoadedImage>(image)?;
        // SAFETY: Static UTF-16 bytes outlive StartImage and image unload.
        unsafe {
            loaded.set_load_options(options.as_bytes().as_ptr(), options.num_bytes() as u32);
        }
    }
    uefi::println!("CMPUNLOCKER-FIXTURE: START_READONLY_AS_CHILD");
    let result = boot::start_image(image).map_err(|error| error.status());
    result?;
    uefi::println!("CMPUNLOCKER-FIXTURE: PARENT_RESUMED");
    Ok(())
}
