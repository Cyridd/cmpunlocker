// SPDX-License-Identifier: MIT
use core::mem::MaybeUninit;
use core::ptr;
use uefi::boot::{self, OpenProtocolAttributes, OpenProtocolParams, ScopedProtocol};
use uefi::proto::device_path::{build, DevicePath, DeviceType};
use uefi::proto::loaded_image::LoadedImage;
use uefi::proto::ProtocolPointer;
use uefi::{CStr16, Handle, Status};

/// Borrow a protocol without disconnecting drivers or claiming the device.
pub fn protocol<P: ProtocolPointer + ?Sized>(handle: Handle) -> Result<ScopedProtocol<P>, Status> {
    // SAFETY: These callers neither remove protocols nor disconnect handles.
    // Each ScopedProtocol is dropped before a child image is started.
    unsafe {
        boot::open_protocol::<P>(
            OpenProtocolParams {
                handle,
                agent: boot::image_handle(),
                controller: None,
            },
            OpenProtocolAttributes::GetProtocol,
        )
    }
    .map_err(|error| error.status())
}

/// Prepare an image using an exact volume device path, before handoff.
pub fn load_from_volume(volume: Handle, filename: &CStr16) -> Result<Handle, Status> {
    let volume_path = protocol::<DevicePath>(volume)?;
    let volume_path = volume_path.get().ok_or(Status::UNSUPPORTED)?;
    load_with_filename(volume_path, filename, None)
}

/// Load verified/read image bytes with the exact source volume + file path.
/// The source can be released after LoadImage; firmware copies the PE image.
pub fn load_from_buffer(
    volume_path: &DevicePath,
    filename: &CStr16,
    bytes: &[u8],
) -> Result<Handle, Status> {
    if bytes.is_empty() {
        return Err(Status::LOAD_ERROR);
    }
    let image = load_with_filename(volume_path, filename, Some(bytes))?;
    let verified = (|| {
        let loaded = protocol::<LoadedImage>(image)?;
        let device = loaded.device().ok_or(Status::NO_MAPPING)?;
        let actual = protocol::<DevicePath>(device)?;
        if actual
            .get()
            .is_none_or(|path| path.as_bytes() != volume_path.as_bytes())
        {
            return Err(Status::NO_MAPPING);
        }
        Ok(())
    })();
    if let Err(status) = verified {
        if let Err(error) = boot::unload_image(image) {
            uefi::println!(
                "CMP40: SOURCE_VOLUME_REJECTED unload failed {:?}",
                error.status()
            );
        }
        return Err(status);
    }
    uefi::println!("CMP40: SOURCE_VOLUME_VERIFIED");
    Ok(image)
}

fn load_with_filename(
    volume_path: &DevicePath,
    filename: &CStr16,
    source: Option<&[u8]>,
) -> Result<Handle, Status> {
    let mut storage = [MaybeUninit::uninit(); 4096];
    let mut builder = build::DevicePathBuilder::with_buf(&mut storage);
    for node in volume_path.node_iter() {
        if node.device_type() == DeviceType::END {
            return Err(Status::UNSUPPORTED); // multi-instance path
        }
        builder = builder.push(&node).map_err(|_| Status::BAD_BUFFER_SIZE)?;
    }
    let path = builder
        .push(&build::media::FilePath {
            path_name: filename,
        })
        .and_then(build::DevicePathBuilder::finalize)
        .map_err(|_| Status::BAD_BUFFER_SIZE)?;
    load(path, source)
}

fn load(path: &DevicePath, source: Option<&[u8]>) -> Result<Handle, Status> {
    let table = uefi::table::system_table_raw().ok_or(Status::NOT_READY)?;
    // SAFETY: The entry macro initializes the system table; boot services
    // remain active throughout this application. All input/out buffers live
    // through LoadImage. The firmware owns any returned image handle.
    unsafe {
        let services = (*table.as_ptr())
            .boot_services
            .as_ref()
            .ok_or(Status::NOT_READY)?;
        let mut raw_handle = ptr::null_mut();
        let status = (services.load_image)(
            false.into(),
            boot::image_handle().as_ptr(),
            path.as_ffi_ptr().cast(),
            source.map_or(ptr::null(), |bytes| bytes.as_ptr().cast()),
            source.map_or(0, <[u8]>::len),
            &mut raw_handle,
        );
        let handle = Handle::from_ptr(raw_handle);
        if status.is_error() {
            // uefi-rs 0.41's load_image wrapper loses the out-handle on an
            // error. UEFI permits one on SECURITY_VIOLATION: release it.
            if let Some(handle) = handle {
                if let Err(error) = boot::unload_image(handle) {
                    uefi::println!(
                        "CMP40-READONLY: rejected-image unload failed {:?}",
                        error.status()
                    );
                }
            }
            Err(status)
        } else {
            handle.ok_or(Status::DEVICE_ERROR)
        }
    }
}
