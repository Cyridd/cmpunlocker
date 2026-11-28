// SPDX-License-Identifier: MIT
//! Behavioral host model. No MMIO, vendor code or firmware execution.
use std::collections::BTreeMap;

use cmp40_efi_core::state::{Control, STOCK_PLM, WPR2_HI_UP, WPR2_LO_DOWN, WPR2_LO_UP};
use cmp40_efi_core::transaction::{
    reg::*, BooterFailure, Io, BOOTER_BYTES, FWSEC_BYTES, LOADER_BYTES,
};

pub type Event = [u32; 3];

pub fn synthetic(bytes: usize) -> Vec<u8> {
    (0..bytes).map(|i| (i.wrapping_mul(17) + 3) as u8).collect()
}

pub fn assets() -> (Vec<u8>, Vec<u8>, Vec<u8>) {
    (
        synthetic(FWSEC_BYTES),
        synthetic(LOADER_BYTES),
        synthetic(BOOTER_BYTES),
    )
}

pub struct Model {
    pub events: Vec<Event>,
    pub regs: BTreeMap<u32, u32>,
    memory: BTreeMap<(u32, u32), u32>,
    ports: BTreeMap<u32, u32>,
    pub fault: u32,
    pub control: Control,
    pub failures: Vec<BooterFailure>,
    gsp_started: bool,
    sec_started: bool,
    ticks: u32,
}

impl Model {
    pub fn new(fault: u32, control: Control) -> Self {
        let mut result = Self {
            events: Vec::new(),
            regs: BTreeMap::new(),
            memory: BTreeMap::new(),
            ports: BTreeMap::new(),
            fault,
            control,
            failures: Vec::new(),
            gsp_started: false,
            sec_started: false,
            ticks: 0,
        };
        for (offset, value) in [
            (GSP + CPU, 0x10),
            (SEC2 + CPU, 0x10),
            (GSP + HWCFG2, 0x8000_0000),
            (GSP + 0x108, 255),
            (WPR_LO, WPR2_LO_DOWN),
            (FEAT_PLM, STOCK_PLM),
            (SEC2 + MBOX0, 0x91),
            (SEC2 + 0x110, 0x1234),
        ] {
            result.regs.insert(offset, value);
        }
        if [26, 29, 31].contains(&fault) {
            result.regs.insert(SEC2 + 0x118, 0xbadf5040);
            if fault != 31 {
                result.regs.insert(SEC2 + CPU, 0xbadf5040);
            }
        }
        if fault == 30 {
            result.regs.insert(SEC2 + CPU, u32::MAX);
        }
        if fault == 32 {
            result.regs.insert(SEC2 + BOOTVEC, 4);
        }
        result
    }

    pub fn completed(&mut self) {
        self.gsp_started = true;
        self.sec_started = true;
        let (ss0, ss1) = match self.control {
            Control::Full => (0x88888888, 8),
            Control::Zero => (0, 0),
        };
        for (offset, value) in [
            (SS0, ss0),
            (SS1, ss1),
            (WPR_HI, 0),
            (WPR_LO, WPR2_LO_DOWN),
            (SEC2 + MBOX0, 0),
            (SEC2 + MBOX1, 1),
        ] {
            self.regs.insert(offset, value);
        }
        self.completion_faults();
    }

    fn completion_faults(&mut self) {
        let pair = match self.fault {
            14 | 15 => Some((GSP + CPU, 0)),
            16 => Some((GSP + ENGINE, 1)),
            17 => Some((GSP + DMA, 6)),
            18 => Some((GSP + HWCFG2, 0x80001000)),
            19 => Some((FEAT_PLM, u32::MAX)),
            20 => Some((SS0, 1)),
            21 => Some((WPR_HI, WPR2_HI_UP)),
            22 => Some((SEC2 + CPU, 0xbadf5040)),
            23 => Some((GSP + DMA, 0xbadf1100)),
            24 => Some((GSP + CPU, u32::MAX)),
            27 => Some((SS1, 0x18)),
            28 => Some((SEC2 + MBOX1, 0xbadf5040)),
            _ => None,
        };
        if let Some((offset, value)) = pair {
            self.regs.insert(offset, value);
        }
    }

    fn value(&self, offset: u32) -> u32 {
        *self.regs.get(&offset).unwrap_or(&0)
    }
}

impl Io for Model {
    fn read32(&mut self, offset: u32) -> u32 {
        let mut value = self.value(offset);
        for base in [GSP, SEC2] {
            if offset == base + IMEMD || offset == base + DMEMD {
                let port = base + if offset == base + IMEMD { IMEMC } else { DMEMC };
                let address = *self.ports.get(&port).unwrap_or(&0) & 0xfffffc;
                value = *self.memory.get(&(port, address)).unwrap_or(&0);
                if offset == SEC2 + IMEMD && self.ports[&port] & (1 << 28) != 0 {
                    value = 0xdead5ec1;
                }
                if (self.fault == 4 && offset == GSP + IMEMD)
                    || (self.fault == 5 && offset == GSP + DMEMD && address == 0x20)
                    || (self.fault == 8 && offset == SEC2 + IMEMD && address == 0)
                    || (self.fault == 9 && offset == SEC2 + DMEMD && address == 0x200)
                {
                    value ^= 1;
                }
            }
        }
        if self.fault == 3 && offset == GSP + 0x108 {
            value = 1;
        }
        if self.fault == 25 && offset == GSP + HWCFG2 {
            value = 0;
        }
        if self.fault == 7 && offset == SEC2 + CPU && !self.sec_started {
            value = 0;
        }
        self.events.push([0, offset, value]);
        value
    }

    fn write32(&mut self, offset: u32, value: u32) {
        self.events.push([1, offset, value]);
        if [WPR_LO, WPR_HI].contains(&offset) {
            return;
        } // Host WPR stores are gated.
        if self.fault == 32 && offset == SEC2 + BOOTVEC {
            return;
        }
        if (self.fault == 10 && offset == SEC2 + MBOX0 && value != 0xa5a5c3c3 && value != 0x91)
            || (self.fault == 11 && offset == SEC2 + MBOX1)
        {
            return;
        }
        self.regs.insert(offset, value);
        if self.fault == 26 && offset == SEC2_BCR && value == 0 {
            self.regs.insert(SEC2 + CPU, 0x10);
            self.regs.insert(SEC2 + 0x118, 0);
        }
        for base in [GSP, SEC2] {
            if offset == base + IMEMC || offset == base + DMEMC {
                self.ports.insert(offset, value);
            }
            if offset == base + IMEMD || offset == base + DMEMD {
                let port = base + if offset == base + IMEMD { IMEMC } else { DMEMC };
                let position = *self.ports.get(&port).unwrap_or(&0);
                self.memory.insert((port, position & 0xfffffc), value);
                if position & (1 << 24) != 0 {
                    self.ports.insert(port, position + 4);
                }
            }
        }
        if offset == GSP + ENGINE && value == 1 {
            self.regs.insert(GSP + CPU, 0x10);
            self.regs
                .insert(GSP + DMA, if self.fault == 2 { 6 } else { 0 });
        }
        if offset == GSP + CPU && value == 2 {
            self.gsp_started = true;
            self.ticks = 0;
            self.regs.insert(GSP + CPU, 0x10);
            if self.fault != 1 && self.fault != 6 {
                self.regs.insert(WPR_LO, WPR2_LO_UP);
                self.regs.insert(WPR_HI, WPR2_HI_UP);
            }
        }
        if offset == SEC2 + CPU && value == 2 {
            self.completed();
            self.regs
                .insert(SEC2 + CPU, if self.fault == 13 { 0 } else { 0x10 });
            self.regs
                .insert(SEC2 + MBOX0, if self.fault == 12 { 0x31 } else { 0 });
        }
    }

    fn stall(&mut self, us: u32) {
        self.events.push([2, us, 0]);
        if self.gsp_started && us == 1000 {
            self.ticks += 1;
            if self.fault == 1 && self.ticks >= 2 {
                self.regs.insert(WPR_LO, WPR2_LO_UP);
                self.regs.insert(WPR_HI, WPR2_HI_UP);
            }
            if self.fault == 14 && self.ticks >= 2 {
                self.regs.insert(GSP + CPU, 0x10);
            }
        }
    }

    fn flush(&mut self) {
        self.events.push([3, 0, 0]);
    }
    fn booter_failure(&mut self, failure: BooterFailure) {
        self.failures.push(failure);
    }
}

/// C permits argument evaluation order to differ within a diagnostic Print.
/// Sort only consecutive read groups. Stores, delays and cache barriers keep
/// exact order; every read (including its returned value) must still match.
pub fn canonical(events: &[Event]) -> Vec<Event> {
    let mut result = Vec::new();
    let mut index = 0;
    while index < events.len() {
        if events[index][0] == 0 {
            let start = index;
            while index < events.len() && events[index][0] == 0 {
                index += 1;
            }
            let mut group = events[start..index].to_vec();
            group.sort();
            result.extend(group);
        } else {
            result.push(events[index]);
            index += 1;
        }
    }
    result
}
