// SPDX-License-Identifier: MIT
use cmp40_efi_core::pci::{self, BarError, Bdf, Config, Encoding, CMP40_ID};
use std::collections::BTreeMap;

#[derive(Default)]
struct Model {
    registers: BTreeMap<(u8, u8, u8, u8), u32>,
    reads: Vec<(Bdf, u8)>,
    writes: Vec<(Bdf, u8, u32)>,
    reject_command: bool,
    unreadable_command_after_write: bool,
}
fn key(bdf: Bdf, offset: u8) -> (u8, u8, u8, u8) {
    (bdf.bus, bdf.device, bdf.function, offset)
}
impl Config for Model {
    fn read32(&mut self, bdf: Bdf, offset: u8) -> u32 {
        self.reads.push((bdf, offset));
        if offset == 4 && self.unreadable_command_after_write && !self.writes.is_empty() {
            return u32::MAX;
        }
        *self.registers.get(&key(bdf, offset)).unwrap_or(&u32::MAX)
    }
    fn write32(&mut self, bdf: Bdf, offset: u8, value: u32) {
        self.writes.push((bdf, offset, value));
        if !self.reject_command {
            self.registers.insert(key(bdf, offset), value);
        }
    }
}
const TARGET: Bdf = Bdf {
    bus: 2,
    device: 0,
    function: 0,
};
fn bar(low: u32, high: u32) -> Model {
    Model {
        registers: BTreeMap::from([
            (key(TARGET, 4), 0x00100000),
            (key(TARGET, 0x10), low),
            (key(TARGET, 0x14), high),
        ]),
        ..Model::default()
    }
}

#[test]
fn address_encodings_and_fast_probe_order() {
    let bdf = Bdf {
        bus: 0x10,
        device: 0x12,
        function: 3,
    };
    assert_eq!(Encoding::Standard.address(bdf, 0x14), 0x10120314);
    assert_eq!(Encoding::Compact.address(bdf, 0x14), 0x1093014);
    assert_eq!(Encoding::Cf8.address(bdf, 0x14), 0x80109314);
    let mut io = Model::default();
    io.registers
        .insert(key(Bdf { bus: 3, ..TARGET }, 0), CMP40_ID);
    assert_eq!(
        pci::find(&mut io, Encoding::Standard),
        Some(Bdf { bus: 3, ..TARGET })
    );
    assert_eq!(
        io.reads.iter().map(|(bdf, _)| bdf.bus).collect::<Vec<_>>(),
        [2, 1, 3]
    );
    assert!(io.writes.is_empty());
}
#[test]
fn bounded_and_cf8_scan_discover_multifunction_targets() {
    let secondary = Bdf {
        bus: 16,
        device: 7,
        function: 5,
    };
    let mut io = Model::default();
    io.registers.insert(
        key(
            Bdf {
                function: 0,
                ..secondary
            },
            0,
        ),
        0x12348086,
    );
    io.registers.insert(
        key(
            Bdf {
                function: 0,
                ..secondary
            },
            0x0c,
        ),
        0x00800000,
    );
    io.registers.insert(key(secondary, 0), CMP40_ID);
    assert_eq!(pci::find(&mut io, Encoding::Compact), Some(secondary));
    assert!(io.writes.is_empty());
    let mut io = Model::default();
    let high = Bdf {
        bus: 255,
        device: 31,
        function: 0,
    };
    io.registers.insert(key(high, 0), CMP40_ID);
    io.registers.insert(key(high, 0x0c), 0);
    assert_eq!(pci::find(&mut io, Encoding::Standard), None);
    assert_eq!(pci::find(&mut io, Encoding::Cf8), Some(high));
    assert!(io.writes.is_empty());
}
#[test]
fn unsupported_bars_refuse_before_writes() {
    for (low, high, error) in [
        (u32::MAX, 0, BarError::Unreadable),
        (0xc0000001, 0, BarError::IoBar),
        (0, 0, BarError::Zero),
        (0xc0000004, 1, BarError::Above4G),
        (0xffc00000, 0, BarError::InvalidWindow),
    ] {
        let mut io = bar(low, high);
        assert_eq!(pci::enable_bar(&mut io, TARGET), Err(error));
        assert!(io.writes.is_empty());
    }
}
#[test]
fn bar_command_requires_readable_successful_readback() {
    for low in [0xc0000000, 0xc0000004] {
        let mut io = bar(low, 0);
        assert_eq!(pci::enable_bar(&mut io, TARGET), Ok(0xc0000000));
        assert_eq!(io.writes, [(TARGET, 4, 0x00100006)]);
    }
    for unreadable in [false, true] {
        let mut io = bar(0xc0000000, 0);
        io.reject_command = !unreadable;
        io.unreadable_command_after_write = unreadable;
        assert_eq!(
            pci::enable_bar(&mut io, TARGET),
            Err(BarError::CommandRejected)
        );
        assert_eq!(io.writes.len(), 1);
    }
}
