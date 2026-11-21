// SPDX-License-Identifier: MIT
//! QEMU-only parent replaces its filesystem with a counted failing interface.
#![no_std]
#![no_main]
use cmp40_efi::{
    image::{load_from_volume, protocol},
    raw::Source,
};
use core::{
    ffi::c_void,
    sync::atomic::{AtomicUsize, Ordering},
};
use uefi::{
    boot,
    prelude::*,
    proto::{loaded_image::LoadedImage, media::fs::SimpleFileSystem},
    Identify,
};

static CALLS: AtomicUsize = AtomicUsize::new(0);
#[repr(C)]
struct FailingFs {
    revision: u64,
    open: unsafe extern "efiapi" fn(*mut FailingFs, *mut *mut c_void) -> Status,
}
unsafe extern "efiapi" fn fail(_: *mut FailingFs, _: *mut *mut c_void) -> Status {
    CALLS.fetch_add(1, Ordering::SeqCst);
    Status::DEVICE_ERROR
}
static mut FAILING: FailingFs = FailingFs {
    revision: 0x10000,
    open: fail,
};

#[entry]
fn main() -> Status {
    if let Err(error) = uefi::helpers::init() {
        return error.status();
    }
    run().unwrap_or_else(|status| {
        uefi::println!("CMP40-FIXTURE: RAW_PARENT_FAILED {:?}", status);
        status
    })
}
fn run() -> Result<Status, Status> {
    boot::set_watchdog_timer(0, 0x10000, None).map_err(|e| e.status())?;
    let volume = protocol::<LoadedImage>(boot::image_handle())?
        .device()
        .ok_or(Status::NOT_FOUND)?;
    let mode = Source::own(volume)?.read(&["MODE.TXT"], 64)?;
    let child = load_from_volume(volume, cstr16!("\\EFI\\CMP40\\READONLY.EFI"))?;
    let options = if mode == b"parent" {
        cstr16!("--return-to-limine")
    } else {
        cstr16!("")
    };
    {
        let mut loaded = protocol::<LoadedImage>(child)?;
        // SAFETY: Static option strings remain alive through StartImage.
        unsafe {
            loaded.set_load_options(options.as_bytes().as_ptr(), options.num_bytes() as u32);
        }
    }
    let original = {
        let fs = protocol::<SimpleFileSystem>(volume)?;
        core::ptr::from_ref(&*fs).cast::<c_void>()
    };
    let failing = core::ptr::addr_of!(FAILING).cast::<c_void>();
    // SAFETY: Private QEMU filesystem, no outstanding references; both
    // interfaces remain allocated and the original is restored after return.
    unsafe {
        boot::reinstall_protocol_interface(volume, &SimpleFileSystem::GUID, original, failing)
            .map_err(|e| e.status())?;
    }
    uefi::println!("CMP40-FIXTURE: SFS_BLOCKED START_RAW_CHILD");
    let result = boot::start_image(child).map_or_else(|error| error.status(), |()| Status::SUCCESS);
    // SAFETY: Same private handle and live interface pointers; child exited.
    unsafe {
        boot::reinstall_protocol_interface(volume, &SimpleFileSystem::GUID, failing, original)
            .map_err(|e| e.status())?;
    }
    uefi::println!(
        "CMP40-FIXTURE: RAW_CHILD_RETURNED {:?} SFS_CALLS={}",
        result,
        CALLS.load(Ordering::SeqCst)
    );
    if CALLS.load(Ordering::SeqCst) != 0 {
        return Err(Status::DEVICE_ERROR);
    }
    Ok(Status::SUCCESS)
}
