// SPDX-License-Identifier: MIT
//! Pure data operations for the CMP40HX EFI port. No allocation, firmware
//! services or embedded firmware. Transaction execution uses a caller's IO
//! backend supplied by the EFI application or a host model.
#![no_std]

pub mod allocation;
pub mod fat;
pub mod gen2;
pub mod image_path;
pub mod log_store;
pub mod log_write;
pub mod metadata;
pub mod options;
pub mod partition;
pub mod pci;
pub mod radix;
pub mod rebar;
pub mod sha256;
pub mod state;
pub mod timer;
pub mod trace;
pub mod transaction;

const DMA_LIMIT: u64 = 1 << 49;
pub const PAGE_BYTES: usize = 4096;

/// Numeric range/alignment check; does not establish CPU or GPU mapping.
pub fn valid_dma_region(base: u64, bytes: usize) -> bool {
    base != 0
        && base % PAGE_BYTES as u64 == 0
        && bytes != 0
        && base
            .checked_add(bytes as u64)
            .is_some_and(|end| end <= DMA_LIMIT)
}
