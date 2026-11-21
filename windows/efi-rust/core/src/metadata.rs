// SPDX-License-Identifier: MIT
//! The existing Windows CPU-RM profile, serialized explicitly in little
//! endian. This is a boot argument block, never a native Rust ABI structure.

use crate::valid_dma_region;

pub const METADATA_BYTES: usize = 256;
pub const FRAMEBUFFER_BYTES: u64 = 0x2_0000_0000;
pub const IMAGE_BYTES: usize = 0x1bf_adc8;
pub const BOOTLOADER_BYTES: usize = 0x6000;
pub const PAYLOAD_BYTES: usize = 0xfa00;
pub const CPU_RM_HEAP_BYTES: u64 = 0xf0000;
const MIB: u64 = 0x10_0000;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum SignatureKind {
    Payload,
    Stock,
}

impl SignatureKind {
    pub const fn bytes(self) -> usize {
        match self {
            Self::Payload => PAYLOAD_BYTES,
            Self::Stock => 0x1000,
        }
    }
}

/// Addresses already allocated/mapped by the caller. Checking their numeric
/// range does not establish GPU reachability or an identity DMA mapping.
#[derive(Clone, Copy, Debug)]
pub struct DmaAddresses {
    pub radix: u64,
    pub signature: u64,
    pub bootloader: u64,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum MetadataError {
    InvalidDmaAddress,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct WprMetadata([u8; METADATA_BYTES]);

impl WprMetadata {
    /// Build only the validated 8 GiB CMP40HX Windows CPU-RM profile. The
    /// small heap, image size, bootloader offsets and flags match the C EFI.
    pub fn cpu_rm(
        addresses: DmaAddresses,
        signature: SignatureKind,
    ) -> Result<Self, MetadataError> {
        if !valid_dma_region(addresses.radix, crate::PAGE_BYTES)
            || !valid_dma_region(addresses.signature, signature.bytes())
            || !valid_dma_region(addresses.bootloader, BOOTLOADER_BYTES)
        {
            return Err(MetadataError::InvalidDmaAddress);
        }

        let vga_start = FRAMEBUFFER_BYTES - MIB;
        let wpr_end = vga_start & !0x1ffff;
        let frts_start = wpr_end - MIB;
        let boot_start = (frts_start - BOOTLOADER_BYTES as u64) & !0xfff;
        let image_start = (boot_start - IMAGE_BYTES as u64) & !0xffff;
        let heap_start = (image_start - CPU_RM_HEAP_BYTES) & !(MIB - 1);
        let wpr_start = heap_start - MIB;
        let non_wpr_start = wpr_start - MIB;

        let mut result = Self([0; METADATA_BYTES]);
        for (offset, value) in [
            (0x00, 0xdc3a_ae21_371a_60b3),
            (0x08, 1),
            (0x10, addresses.radix),
            (0x18, IMAGE_BYTES as u64),
            (0x20, addresses.bootloader),
            (0x28, BOOTLOADER_BYTES as u64),
            (0x30, 0x1800),
            (0x38, 0x800),
            (0x48, addresses.signature),
            (0x50, signature.bytes() as u64),
            (0x58, non_wpr_start),
            (0x60, non_wpr_start),
            (0x68, MIB),
            (0x70, wpr_start),
            (0x78, heap_start),
            (0x80, CPU_RM_HEAP_BYTES),
            (0x88, image_start),
            (0x90, boot_start),
            (0x98, frts_start),
            (0xa0, MIB),
            (0xa8, wpr_end),
            (0xb0, FRAMEBUFFER_BYTES),
            (0xb8, vga_start),
            (0xc0, MIB),
        ] {
            result.0[offset..offset + 8].copy_from_slice(&value.to_le_bytes());
        }
        result.0[0xf1] = 1; // GSP_FW_FLAGS_CLOCK_BOOST; byte-sized field.
        Ok(result)
    }

    pub const fn as_bytes(&self) -> &[u8; METADATA_BYTES] {
        &self.0
    }
}
