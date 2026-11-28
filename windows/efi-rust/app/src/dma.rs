// SPDX-License-Identifier: MIT
//! Identity-addressed reserved pages, retained across failure and OS handoff.
use cmp40_efi_core::allocation::{Allocator, HighSlots, Request};
use cmp40_efi_core::{valid_dma_region, PAGE_BYTES};
use core::ptr::NonNull;
use uefi::boot::MemoryType;
use uefi::Status;
use uefi_raw::table::boot::AllocateType;

pub struct Reserved;
impl Allocator for Reserved {
    type Error = Status;
    fn reserve(&mut self, pages: usize, request: Request) -> Result<u64, Status> {
        let table = uefi::table::system_table_raw().ok_or(Status::NOT_READY)?;
        let (kind, mut address) = match request {
            Request::Address(address) => (AllocateType::ADDRESS, address),
            Request::MaxAddress(address) => (AllocateType::MAX_ADDRESS, address),
        };
        // SAFETY: Live boot services, valid page count supplied by HighSlots,
        // and a live UINT64 out parameter. Raw call preserves one request per
        // C policy probe (uefi-rs retries allocations returned at address zero).
        let status = unsafe {
            ((*table.as_ptr())
                .boot_services
                .as_ref()
                .ok_or(Status::NOT_READY)?
                .allocate_pages)(kind, MemoryType::RESERVED, pages, &mut address)
        };
        if status.is_error() {
            Err(status)
        } else {
            Ok(address)
        }
    }
}

/// Unique ownership of an initialized reserved allocation. Deliberately no
/// FreePages/Drop: even on error, firmware may still hold a DMA pointer.
pub struct Dma {
    pointer: NonNull<u8>,
    bytes: usize,
}
impl Dma {
    pub fn allocate(slots: &mut HighSlots, bytes: usize) -> Result<Self, Status> {
        let pages = bytes
            .checked_add(PAGE_BYTES - 1)
            .ok_or(Status::INVALID_PARAMETER)?
            / PAGE_BYTES;
        let address = slots
            .reserve(&mut Reserved, pages)
            .map_err(|_| Status::OUT_OF_RESOURCES)?;
        let bytes = pages
            .checked_mul(PAGE_BYTES)
            .ok_or(Status::INVALID_PARAMETER)?;
        if !valid_dma_region(address, bytes) {
            return Err(Status::INVALID_PARAMETER);
        }
        let pointer = NonNull::new(address as *mut u8).ok_or(Status::OUT_OF_RESOURCES)?;
        // SAFETY: AllocatePages returned exclusively owned, contiguous system
        // RAM. EFI x64 uses the physical address as a valid CPU pointer. Clear
        // uninitialized pages before constructing any Rust slice/reference.
        unsafe {
            pointer.as_ptr().write_bytes(0, bytes);
        }
        Ok(Self { pointer, bytes })
    }
    pub fn address(&self) -> u64 {
        self.pointer.as_ptr() as u64
    }
    pub fn bytes(&self) -> &[u8] {
        // SAFETY: Initialized, owned allocation; borrow prevents mutation.
        unsafe { core::slice::from_raw_parts(self.pointer.as_ptr(), self.bytes) }
    }
    pub fn bytes_mut(&mut self) -> &mut [u8] {
        // SAFETY: Exclusive borrow of the unique initialized allocation.
        unsafe { core::slice::from_raw_parts_mut(self.pointer.as_ptr(), self.bytes) }
    }
    pub fn copy(&mut self, source: &[u8]) {
        self.bytes_mut()[..source.len()].copy_from_slice(source);
    }
    /// Transfer the allocation permanently to the single transaction. This
    /// consumes the owner; no second slice or release path can be obtained.
    pub fn into_bytes(self) -> &'static mut [u8] {
        // SAFETY: Self is consumed and reserved pages are never freed. No CPU
        // alias survives; firmware DMA is used only at the transaction's flush
        // boundaries, as in the C reference. This is not an IOMMU mapping.
        unsafe { core::slice::from_raw_parts_mut(self.pointer.as_ptr(), self.bytes) }
    }
}
