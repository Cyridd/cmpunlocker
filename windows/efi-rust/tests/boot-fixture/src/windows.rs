// SPDX-License-Identifier: MIT
#![no_std]
#![no_main]

use cmp40_efi::image::protocol;
use cmp40_efi_core::image_path::file_path_matches;
use uefi::prelude::*;
use uefi::{
    boot,
    proto::{loaded_image::LoadedImage, media::block::BlockIO},
};

#[entry]
fn main() -> Status {
    if let Err(error) = uefi::helpers::init() {
        return error.status();
    }
    let valid = (|| {
        let image = protocol::<LoadedImage>(boot::image_handle())?;
        let volume = image.device().ok_or(Status::NOT_FOUND)?;
        let _block = protocol::<BlockIO>(volume)?;
        if !image.file_path().is_some_and(|path| {
            file_path_matches(path.as_bytes(), "\\EFI\\Microsoft\\Boot\\bootmgfw.efi")
        }) {
            return Err(Status::NO_MAPPING);
        }
        Ok::<_, Status>(())
    })();
    if let Err(status) = valid {
        uefi::println!("CMPUNLOCKER-FIXTURE: WINDOWS_VOLUME_INVALID {:?}", status);
        return status;
    }
    uefi::println!("CMPUNLOCKER-FIXTURE: WINDOWS_VOLUME_OK");
    uefi::println!("CMPUNLOCKER-FIXTURE: WINDOWS_CHAIN_REACHED");
    Status::SUCCESS
}
