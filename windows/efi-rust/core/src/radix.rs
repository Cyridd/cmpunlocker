// SPDX-License-Identifier: MIT
//! Three-level tables for the current EFI's root + L1 + L2 + image layout.
//! The caller supplies the buffer and its DMA base. No pages are allocated.

use crate::{valid_dma_region, PAGE_BYTES};

const ENTRIES_PER_PAGE: usize = PAGE_BYTES / 8;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RadixError {
    EmptyImage,
    ImageTooLarge,
    InvalidDmaAddress,
    BufferTooSmall,
    ImageSizeMismatch,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct RadixPlan {
    image_bytes: usize,
    leaf_pages: usize,
    l2_pages: usize,
    total_bytes: usize,
}

impl RadixPlan {
    pub fn new(image_bytes: usize) -> Result<Self, RadixError> {
        if image_bytes == 0 {
            return Err(RadixError::EmptyImage);
        }
        let leaf_pages = image_bytes
            .checked_add(PAGE_BYTES - 1)
            .ok_or(RadixError::ImageTooLarge)?
            / PAGE_BYTES;
        let l2_pages = leaf_pages.div_ceil(ENTRIES_PER_PAGE);
        // The shipped C layout has exactly one L1 page. Additional L2
        // pointers must not spill into its first L2 page.
        if l2_pages > ENTRIES_PER_PAGE {
            return Err(RadixError::ImageTooLarge);
        }
        let total_bytes = (2 + l2_pages + leaf_pages)
            .checked_mul(PAGE_BYTES)
            .ok_or(RadixError::ImageTooLarge)?;
        Ok(Self {
            image_bytes,
            leaf_pages,
            l2_pages,
            total_bytes,
        })
    }

    pub const fn table_bytes(self) -> usize {
        (2 + self.l2_pages) * PAGE_BYTES
    }

    pub const fn total_bytes(self) -> usize {
        self.total_bytes
    }

    pub const fn leaf_pages(self) -> usize {
        self.leaf_pages
    }

    /// All fallible checks precede the first store. Unused table entries
    /// and the last leaf's padding are zero; bytes beyond this plan survive.
    pub fn write_image(
        self,
        buffer: &mut [u8],
        dma_base: u64,
        image: &[u8],
    ) -> Result<(), RadixError> {
        if image.len() != self.image_bytes {
            return Err(RadixError::ImageSizeMismatch);
        }
        self.write_zero_image(buffer, dma_base)?;
        buffer[self.table_bytes()..self.table_bytes() + image.len()].copy_from_slice(image);
        Ok(())
    }

    /// Construct a zero-filled image directly in its leaves, without a
    /// separate source allocation. Validate before stores; clear all unused
    /// entries and leaf padding while preserving bytes beyond this plan.
    pub fn write_zero_image(self, buffer: &mut [u8], dma_base: u64) -> Result<(), RadixError> {
        if buffer.len() < self.total_bytes {
            return Err(RadixError::BufferTooSmall);
        }
        if !valid_dma_region(dma_base, self.total_bytes) {
            return Err(RadixError::InvalidDmaAddress);
        }

        let buffer = &mut buffer[..self.total_bytes];
        buffer.fill(0);
        put_address(buffer, 0, dma_base + PAGE_BYTES as u64);
        for index in 0..self.l2_pages {
            put_address(
                buffer,
                PAGE_BYTES + index * 8,
                dma_base + ((2 + index) * PAGE_BYTES) as u64,
            );
        }
        for index in 0..self.leaf_pages {
            put_address(
                buffer,
                2 * PAGE_BYTES + index * 8,
                dma_base + (self.table_bytes() + index * PAGE_BYTES) as u64,
            );
        }
        Ok(())
    }
}

fn put_address(buffer: &mut [u8], offset: usize, address: u64) {
    buffer[offset..offset + 8].copy_from_slice(&address.to_le_bytes());
}
