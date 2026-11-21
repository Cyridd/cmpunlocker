// SPDX-License-Identifier: MIT
//! Existing reserved-memory placement policy, independent of EFI allocation.
//! Successful pages stay owned by the caller; this policy never frees them.
use crate::{valid_dma_region, PAGE_BYTES};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Request {
    Address(u64),
    MaxAddress(u64),
}

/// Adapter must allocate EfiReservedMemoryType pages and return the actual
/// physical address. Failed requests must not leave an allocation behind.
pub trait Allocator {
    type Error;
    fn reserve(&mut self, pages: usize, request: Request) -> Result<u64, Self::Error>;
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Error<E> {
    InvalidPages,
    InvalidAddress,
    Backend(E),
}

pub struct HighSlots {
    next: u64,
}

impl Default for HighSlots {
    fn default() -> Self {
        Self::new()
    }
}

impl HighSlots {
    pub const fn new() -> Self {
        Self { next: 0x110000000 }
    }

    pub const fn next_address(&self) -> u64 {
        self.next
    }

    pub fn reserve<A: Allocator>(
        &mut self,
        allocator: &mut A,
        pages: usize,
    ) -> Result<u64, Error<A::Error>> {
        let bytes = pages
            .checked_mul(PAGE_BYTES)
            .filter(|&n| n != 0)
            .ok_or(Error::InvalidPages)?;
        let step = 0x1000000;
        // Validate the largest candidate and the potential next slot before
        // making any allocation request. Numeric limits do not prove DMA.
        let last = self
            .next
            .checked_add(11 * step)
            .ok_or(Error::InvalidAddress)?;
        if !valid_dma_region(last, bytes)
            || last
                .checked_add(bytes as u64)
                .and_then(|n| n.checked_add(step))
                .is_none()
        {
            return Err(Error::InvalidAddress);
        }
        for index in 0..12 {
            let address = self.next + index * step;
            if let Ok(actual) = allocator.reserve(pages, Request::Address(address)) {
                if actual != address {
                    return Err(Error::InvalidAddress);
                }
                self.next = actual + bytes as u64 + step;
                return Ok(actual);
            }
        }
        for address in [0x1a0000000, 0x1c0000000, 0x1e0000000] {
            if let Ok(actual) = allocator.reserve(pages, Request::Address(address)) {
                if actual != address || !valid_dma_region(actual, bytes) {
                    return Err(Error::InvalidAddress);
                }
                // Static fallbacks do not advance next_high_slot in C.
                return Ok(actual);
            }
        }
        let actual = allocator
            .reserve(pages, Request::MaxAddress(0xffffffff))
            .map_err(Error::Backend)?;
        if !valid_dma_region(actual, bytes) || actual + bytes as u64 > 0x100000000 {
            return Err(Error::InvalidAddress);
        }
        Ok(actual)
    }
}
