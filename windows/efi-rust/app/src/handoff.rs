// SPDX-License-Identifier: MIT
use crate::{
    image::{load_from_buffer, load_from_volume, protocol},
    raw::{self, Source},
};
use cmp40_efi_core::image_path::file_path_matches;
use uefi::boot;
use uefi::proto::device_path::DevicePath;
use uefi::proto::loaded_image::LoadedImage;
use uefi::{cstr16, Handle, Status};

const WINDOWS_PATH: &str = "\\EFI\\Microsoft\\Boot\\bootmgfw.efi";

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum SourceKind {
    RawBuffer,
    Firmware,
}
pub struct Prepared {
    pub image: Handle,
    pub source: SourceKind,
}

pub fn windows() -> Result<Handle, Status> {
    windows_prepared().map(|prepared| prepared.image)
}

pub fn windows_prepared() -> Result<Prepared, Status> {
    let (own_volume, self_is_windows) = {
        let image = protocol::<LoadedImage>(boot::image_handle())?;
        (
            image.device(),
            image
                .file_path()
                .is_some_and(|path| file_path_matches(path.as_bytes(), WINDOWS_PATH)),
        )
    };
    let own_path = own_volume
        .and_then(|volume| protocol::<DevicePath>(volume).ok())
        .and_then(|path| path.get().map(|path| path.to_boxed()));
    if let Some(volume) = own_volume {
        if !self_is_windows {
            match Source::own(volume).and_then(|source| prepare(&source)) {
                Ok(image) => {
                    uefi::println!("CMP40-READONLY: WINDOWS_PREPARED own-volume");
                    return Ok(image);
                }
                Err(Status::NOT_FOUND) => {}
                Err(status) => return Err(status),
            }
        } else {
            uefi::println!("CMP40-READONLY: SELF_WINDOWS_PATH_SKIPPED");
        }
    }
    let mut candidate: Option<Prepared> = None;
    for source in raw::sources()? {
        if Some(source.handle) == own_volume
            && source.region.is_some_and(|region| region.offset == 0)
            || own_path
                .as_ref()
                .is_some_and(|path| path.as_bytes() == source.path)
        {
            continue;
        }
        match prepare(&source) {
            Ok(image) => {
                if let Some(previous) = candidate {
                    release(previous.image);
                    release(image.image);
                    uefi::println!("CMP40-READONLY: WINDOWS_AMBIGUOUS");
                    return Err(Status::NO_MAPPING);
                }
                candidate = Some(image);
            }
            Err(Status::NOT_FOUND) => {}
            Err(status) => {
                if let Some(image) = candidate {
                    release(image.image);
                }
                return Err(status);
            }
        }
    }
    let image = candidate.ok_or(Status::NOT_FOUND)?;
    uefi::println!("CMP40-READONLY: WINDOWS_PREPARED unique-volume");
    Ok(image)
}

fn prepare(source: &Source) -> Result<Prepared, Status> {
    match source.read(&["EFI", "Microsoft", "Boot", "bootmgfw.efi"], 0x02000000) {
        Ok(bytes) => {
            let image = load_from_buffer(
                source.device_path()?,
                cstr16!("\\EFI\\Microsoft\\Boot\\bootmgfw.efi"),
                &bytes,
            )?;
            uefi::println!(
                "CMP40: WINDOWS_FROM_BUFFER bytes={} SFS_CALLS=0",
                bytes.len()
            );
            Ok(Prepared {
                image,
                source: SourceKind::RawBuffer,
            })
        }
        Err(Status::UNSUPPORTED) if source.firmware_filesystem => load_from_volume(
            source.handle,
            cstr16!("\\EFI\\Microsoft\\Boot\\bootmgfw.efi"),
        )
        .map(|image| Prepared {
            image,
            source: SourceKind::Firmware,
        }),
        Err(Status::UNSUPPORTED) => Err(Status::NOT_FOUND),
        Err(status) => Err(status),
    }
}

pub fn release(image: Handle) {
    if let Err(error) = boot::unload_image(image) {
        uefi::println!("CMP40-READONLY: IMAGE_UNLOAD_FAILED {:?}", error.status());
    }
}
