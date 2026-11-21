// SPDX-License-Identifier: MIT
//! Parse bounded UTF-16LE load options before GPU mutation.
use crate::image::protocol;
use cmp40_efi_core::options::CpuRmOptions;
use uefi::{boot, proto::loaded_image::LoadedImage, Status};

pub fn options() -> Result<CpuRmOptions, Status> {
    let image = protocol::<LoadedImage>(boot::image_handle())?;
    CpuRmOptions::parse(image.load_options_as_bytes().unwrap_or(&[]))
        .map_err(|_| Status::INVALID_PARAMETER)
}
