// SPDX-License-Identifier: MIT
//! Configuration-space policy for the existing RootBridgeIo/CF8 paths.

pub const CMP40_ID: u32 = 0x1f0b10de;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Bdf {
    pub bus: u8,
    pub device: u8,
    pub function: u8,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Encoding {
    Standard,
    Compact,
    Cf8,
}

impl Encoding {
    pub const fn address(self, bdf: Bdf, offset: u8) -> u64 {
        let bus = bdf.bus as u64;
        let device = bdf.device as u64;
        let function = bdf.function as u64;
        match self {
            Self::Standard => (bus << 24) | (device << 16) | (function << 8) | offset as u64,
            Self::Compact => (bus << 20) | (device << 15) | (function << 12) | offset as u64,
            Self::Cf8 => {
                0x80000000 | (bus << 16) | (device << 11) | (function << 8) | (offset as u64 & 0xfc)
            }
        }
    }
}

pub trait Config {
    fn read32(&mut self, bdf: Bdf, offset: u8) -> u32;
    fn write32(&mut self, bdf: Bdf, offset: u8, value: u32);
}

/// Same fast probes, bus limits and multifunction reads as the C finder.
pub fn find(io: &mut impl Config, encoding: Encoding) -> Option<Bdf> {
    for bus in [2, 1, 3, 0, 4, 5] {
        let bdf = Bdf {
            bus,
            device: 0,
            function: 0,
        };
        if io.read32(bdf, 0) == CMP40_ID {
            return Some(bdf);
        }
    }
    let limit = if encoding == Encoding::Cf8 { 256 } else { 17 };
    for bus in 0..limit {
        for device in 0..32 {
            let mut bdf = Bdf {
                bus: bus as u8,
                device,
                function: 0,
            };
            let id = io.read32(bdf, 0);
            if id == u32::MAX || id & 0xffff == 0 {
                continue;
            }
            let header = io.read32(bdf, 0x0c);
            let functions = if header & 0x800000 != 0 { 8 } else { 1 };
            for function in 0..functions {
                bdf.function = function;
                let identity = if function == 0 { id } else { io.read32(bdf, 0) };
                if identity == CMP40_ID {
                    return Some(bdf);
                }
            }
        }
    }
    None
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum BarError {
    Unreadable,
    IoBar,
    Zero,
    Above4G,
    InvalidWindow,
    CommandRejected,
}

pub fn enable_bar(io: &mut impl Config, bdf: Bdf) -> Result<u32, BarError> {
    let command = io.read32(bdf, 4);
    let low = io.read32(bdf, 0x10);
    if command == u32::MAX || low == u32::MAX {
        return Err(BarError::Unreadable);
    }
    if low & 1 != 0 {
        return Err(BarError::IoBar);
    }
    if low & !15 == 0 {
        return Err(BarError::Zero);
    }
    if (low >> 1) & 3 == 2 && io.read32(bdf, 0x14) != 0 {
        return Err(BarError::Above4G);
    }
    if u64::from(low & !15) + 0x900000 > 0x100000000 {
        return Err(BarError::InvalidWindow);
    }
    io.write32(bdf, 4, command | 6);
    let readback = io.read32(bdf, 4);
    if readback == u32::MAX || readback & 6 != 6 {
        return Err(BarError::CommandRejected);
    }
    Ok(low & !15)
}
