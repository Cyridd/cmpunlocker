// SPDX-License-Identifier: MIT
//! Primary MBR FAT partitions and GPT ESP entries, including GPT CRC checks.
use crate::fat::{le32, le64, Error, ReadAt, Region};

const ESP: [u8; 16] = [
    0x28, 0x73, 0x2a, 0xc1, 0x1f, 0xf8, 0xd2, 0x11, 0xba, 0x4b, 0x00, 0xa0, 0xc9, 0x3e, 0xc9, 0x3b,
];

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Signature {
    Mbr(u32),
    Gpt([u8; 16]),
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Partition {
    pub number: u32,
    pub first_lba: u64,
    pub blocks: u64,
    pub signature: Signature,
}

impl Partition {
    pub fn region(self, block_bytes: u32) -> Option<Region> {
        if block_bytes == 0 || self.blocks == 0 {
            return None;
        }
        Some(Region {
            offset: self.first_lba.checked_mul(u64::from(block_bytes))?,
            bytes: self.blocks.checked_mul(u64::from(block_bytes))?,
        })
    }
}

#[derive(Clone, Copy, Debug)]
pub struct Table {
    pub entries: u32,
    offset: u64,
    stride: u32,
    block_bytes: u64,
    blocks: u64,
    first_usable: u64,
    last_usable: u64,
    mbr_signature: Option<u32>,
}

impl Table {
    pub fn read<R: ReadAt>(io: &mut R, block_bytes: u32) -> Result<Self, Error<R::Error>> {
        let sector = u64::from(block_bytes);
        if !(512..=4096).contains(&sector)
            || !sector.is_power_of_two()
            || io.capacity() % sector != 0
        {
            return Err(Error::Unsupported);
        }
        let blocks = io.capacity() / sector;
        let region = Region {
            offset: 0,
            bytes: io.capacity(),
        };
        let mut mbr = [0; 512];
        region.read(io, 0, &mut mbr)?;
        if mbr[510..512] != [0x55, 0xaa] {
            return Err(Error::Unsupported);
        }
        let protective = mbr[446..510].chunks_exact(16).any(|entry| entry[4] == 0xee);
        if !protective {
            return Ok(Self {
                entries: 4,
                offset: 446,
                stride: 16,
                block_bytes: sector,
                blocks,
                first_usable: 1,
                last_usable: blocks.saturating_sub(1),
                mbr_signature: Some(le32(&mbr, 440)),
            });
        }
        let mut header = [0; 512];
        region.read(io, sector, &mut header)?;
        if &header[..8] != b"EFI PART" || le32(&header, 8) != 0x0001_0000 {
            return Err(Error::Corrupt);
        }
        let header_bytes = le32(&header, 12) as usize;
        if !(92..=512).contains(&header_bytes) || le32(&header, 20) != 0 {
            return Err(Error::Corrupt);
        }
        let expected_crc = le32(&header, 16);
        header[16..20].fill(0);
        if crc32(&header[..header_bytes]) != expected_crc
            || le64(&header, 24) != 1
            || le64(&header, 32) != blocks.saturating_sub(1)
        {
            return Err(Error::Corrupt);
        }
        let first_usable = le64(&header, 40);
        let last_usable = le64(&header, 48);
        let entries = le32(&header, 80);
        let stride = le32(&header, 84);
        let entries_lba = le64(&header, 72);
        if entries == 0
            || stride < 128
            || stride % 128 != 0
            || entries_lba < 2
            || first_usable > last_usable
            || last_usable >= blocks.saturating_sub(1)
        {
            return Err(Error::Corrupt);
        }
        // Firmware partition handles can supply volumes whose GPT tables
        // exceed this parser's resource limits.
        if entries > 128 || stride > 1024 {
            return Err(Error::Unsupported);
        }
        let offset = entries_lba.checked_mul(sector).ok_or(Error::Corrupt)?;
        let bytes = u64::from(entries) * u64::from(stride);
        if offset
            .checked_add(bytes)
            .is_none_or(|end| end.div_ceil(sector) > first_usable)
        {
            return Err(Error::Corrupt);
        }
        let mut digest = !0;
        let mut done = 0;
        let mut buffer = [0; 512];
        while done < bytes {
            let take = (bytes - done).min(512) as usize;
            region.read(io, offset + done, &mut buffer[..take])?;
            digest = crc_update(digest, &buffer[..take]);
            done += take as u64;
        }
        if !digest != le32(&header, 88) {
            return Err(Error::Corrupt);
        }
        Ok(Self {
            entries,
            offset,
            stride,
            block_bytes: sector,
            blocks,
            first_usable,
            last_usable,
            mbr_signature: None,
        })
    }

    pub fn get<R: ReadAt>(
        self,
        io: &mut R,
        index: u32,
    ) -> Result<Option<Partition>, Error<R::Error>> {
        if index >= self.entries {
            return Err(Error::InvalidInput);
        }
        let region = Region {
            offset: 0,
            bytes: io.capacity(),
        };
        let mut entry = [0; 128];
        let take = if self.mbr_signature.is_some() {
            16
        } else {
            128
        };
        region.read(
            io,
            self.offset + u64::from(index) * u64::from(self.stride),
            &mut entry[..take],
        )?;
        let (first, blocks, signature) = if let Some(signature) = self.mbr_signature {
            if !matches!(entry[4], 0x04 | 0x06 | 0x0b | 0x0c | 0x0e | 0xef) {
                return Ok(None);
            }
            (
                u64::from(le32(&entry, 8)),
                u64::from(le32(&entry, 12)),
                Signature::Mbr(signature),
            )
        } else {
            if entry[..16] != ESP {
                return Ok(None);
            }
            let first = le64(&entry, 32);
            let last = le64(&entry, 40);
            if first < self.first_usable
                || last > self.last_usable
                || first > last
                || entry[16..32].iter().all(|&byte| byte == 0)
            {
                return Err(Error::Corrupt);
            }
            (
                first,
                last - first + 1,
                Signature::Gpt(entry[16..32].try_into().unwrap()),
            )
        };
        if first == 0
            || blocks == 0
            || first
                .checked_add(blocks)
                .is_none_or(|end| end > self.blocks)
            || first.checked_mul(self.block_bytes).is_none()
            || blocks.checked_mul(self.block_bytes).is_none()
        {
            return Err(Error::Corrupt);
        }
        Ok(Some(Partition {
            number: index + 1,
            first_lba: first,
            blocks,
            signature,
        }))
    }
}

pub fn crc32(bytes: &[u8]) -> u32 {
    !crc_update(!0, bytes)
}
fn crc_update(mut crc: u32, bytes: &[u8]) -> u32 {
    for &byte in bytes {
        crc ^= u32::from(byte);
        for _ in 0..8 {
            crc = (crc >> 1) ^ (0xedb8_8320 & 0u32.wrapping_sub(crc & 1));
        }
    }
    crc
}
