// SPDX-License-Identifier: MIT
//! Read-only access to the standard EFI_PCI_IO_PROTOCOL ABI. Slots for writes,
//! DMA and attributes are opaque, with no callable Rust methods exposed.
use core::ffi::c_void;
use uefi::boot::{self, SearchType};
use uefi::proto::unsafe_protocol;
use uefi::{Identify, Status};

use cmp40_efi::image::protocol;

type ReadConfig =
    unsafe extern "efiapi" fn(*const ReadOnlyPciIo, u32, u32, usize, *mut c_void) -> Status;
type Location = unsafe extern "efiapi" fn(
    *const ReadOnlyPciIo,
    *mut usize,
    *mut usize,
    *mut usize,
    *mut usize,
) -> Status;

#[repr(C)]
#[unsafe_protocol("4cf5b200-68b8-4ca5-9eec-b23e3f50029a")]
struct ReadOnlyPciIo {
    prefix: [usize; 6], // PollMem, PollIo, Mem.Read/Write, Io.Read/Write
    read_config: ReadConfig,
    middle: [usize; 7], // Pci.Write through Flush
    location: Location,
    suffix: [usize; 5], // Attributes through RomImage
}

const _: () = assert!(core::mem::offset_of!(ReadOnlyPciIo, read_config) == 6 * 8);
const _: () = assert!(core::mem::offset_of!(ReadOnlyPciIo, location) == 14 * 8);
const _: () = assert!(core::mem::size_of::<ReadOnlyPciIo>() == 20 * 8);

impl ReadOnlyPciIo {
    fn read32(&self, offset: u32) -> Result<u32, Status> {
        if offset > 0xfc || !offset.is_multiple_of(4) {
            return Err(Status::INVALID_PARAMETER);
        }
        let mut value = 0;
        // SAFETY: Standard protocol layout pinned above, a live firmware
        // protocol, UINT32 width=2, one aligned DWORD and valid out storage.
        let status = unsafe { (self.read_config)(self, 2, offset, 1, ptr_of(&mut value)) };
        if status.is_error() {
            Err(status)
        } else {
            Ok(value)
        }
    }

    fn location(&self) -> Result<[usize; 4], Status> {
        let [mut segment, mut bus, mut device, mut function] = [0; 4];
        // SAFETY: Firmware protocol stays open; all four UINTN outputs live.
        let status =
            unsafe { (self.location)(self, &mut segment, &mut bus, &mut device, &mut function) };
        if status.is_error() {
            Err(status)
        } else {
            Ok([segment, bus, device, function])
        }
    }
}

fn ptr_of(value: &mut u32) -> *mut c_void {
    core::ptr::from_mut(value).cast()
}

pub fn inventory() -> Result<(), Status> {
    let handles = match boot::locate_handle_buffer(SearchType::ByProtocol(&ReadOnlyPciIo::GUID)) {
        Ok(handles) => Some(handles),
        Err(error) if error.status() == Status::NOT_FOUND => None,
        Err(error) => return Err(error.status()),
    };
    let mut targets = 0;
    let mut errors = 0;
    for &handle in handles.iter().flat_map(|handles| handles.iter()) {
        let result = (|| {
            let pci = protocol::<ReadOnlyPciIo>(handle)?;
            let [segment, bus, device, function] = pci.location()?;
            let identity = pci.read32(0)?;
            let class = pci.read32(8)? >> 8;
            uefi::println!(
                "CMP40-READONLY: PCI {:04x}:{:02x}:{:02x}.{} {:04x}:{:04x} CLASS={:06x}",
                segment,
                bus,
                device,
                function,
                identity & 0xffff,
                identity >> 16,
                class
            );
            if identity == 0x1f0b10de && function == 0 {
                targets += 1;
            }
            Ok::<(), Status>(())
        })();
        if let Err(status) = result {
            errors += 1;
            uefi::println!("CMP40-READONLY: PCI_READ_FAILED {:?}", status);
        }
    }
    uefi::println!(
        "CMP40-READONLY: PCI_HANDLES={} TARGETS={} READ_ERRORS={}",
        handles.as_ref().map_or(0, |handles| handles.len()),
        targets,
        errors
    );
    // Also exercise the same RootBridgeIo/CF8 discovery route used by FULL/ZERO.
    // Discovery reads config only and never enables BARs or touches GPU MMIO.
    match cmp40_efi::hardware::Pci::discover() {
        Ok(target) => uefi::println!(
            "CMP40-READONLY: TARGET_ROUTE {:04x}:{:02x}:{:02x}.{} {:?} GPU_WRITES=0",
            target.segment,
            target.bdf.bus,
            target.bdf.device,
            target.bdf.function,
            target.encoding
        ),
        Err(Status::NOT_FOUND) => {
            uefi::println!("CMP40-READONLY: TARGET_ROUTE NOT_FOUND GPU_WRITES=0")
        }
        Err(status) => uefi::println!(
            "CMP40-READONLY: TARGET_ROUTE_FAILED {:?} GPU_WRITES=0",
            status
        ),
    }
    Ok(())
}
