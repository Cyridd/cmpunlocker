// SPDX-License-Identifier: MIT
//! Parse bounded UTF-16LE load options before GPU mutation.
use crate::image::protocol;
use cmp40_efi_core::options::CpuRmOptions;
use uefi::{boot, proto::loaded_image::LoadedImage, Status};
#[cfg(feature = "cpu-rm")]
use uefi::{
    cstr16,
    proto::media::{
        file::{File, FileAttribute, FileMode},
        fs::SimpleFileSystem,
    },
};

#[cfg(feature = "cpu-rm")]
pub fn rebar_requested() -> bool {
    let Ok(image) = protocol::<LoadedImage>(boot::image_handle()) else {
        return false;
    };
    let policy = (|| {
        let volume = image.device().ok_or(Status::NOT_FOUND)?;
        let mut fs = protocol::<SimpleFileSystem>(volume)?;
        let mut root = fs.open_volume().map_err(|e| e.status())?;
        let mut file = root
            .open(
                cstr16!("\\EFI\\40HX\\REBAR.CFG"),
                FileMode::Read,
                FileAttribute::empty(),
            )
            .map_err(|e| e.status())?
            .into_regular_file()
            .ok_or(Status::UNSUPPORTED)?;
        let mut data = [0; 64];
        let bytes = file.read(&mut data).map_err(|e| e.status())?;
        if bytes > data.len() {
            return Err(Status::DEVICE_ERROR);
        }
        let parsed = cmp40_efi_core::rebar::file_policy(&data[..bytes]);
        let mut extra = [0; 1];
        if parsed.is_some() && file.read(&mut extra).map_err(|e| e.status())? != 0 {
            return Ok(None);
        }
        Ok::<_, Status>(parsed)
    })();
    let file_enabled = matches!(policy, Ok(Some(true)));
    let enabled = cmp40_efi_core::rebar::requested(
        image.load_options_as_bytes().unwrap_or(&[]),
        file_enabled,
    );
    uefi::println!(
        "CMP40-REBAR: POLICY file={:?} requested={}",
        policy,
        enabled
    );
    enabled
}

pub fn options() -> Result<CpuRmOptions, Status> {
    let image = protocol::<LoadedImage>(boot::image_handle())?;
    CpuRmOptions::parse(image.load_options_as_bytes().unwrap_or(&[]))
        .map_err(|_| Status::INVALID_PARAMETER)
}
