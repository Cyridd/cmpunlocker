// SPDX-License-Identifier: MIT
//! The C EFI's opt-in 8 GiB BAR1 relocation, driven by a caller's PCI/MMIO IO.
//! Placement checks are not an ACPI resource grant or proof of root routing.
use crate::{options::has_load_option, pci::Bdf};

pub const BAR1_BYTES: u64 = 0x200000000;
pub const CYA: u32 = 0x88724;
pub const CAP: u32 = 0x88bbc;
pub const CFG: u32 = 0x88dcc;
pub const CONTROL: u32 = 0x88bc0;
const SELECTOR: u32 = 7;
const ENABLE: u32 = 0x80000000;

pub fn file_policy(bytes: &[u8]) -> Option<bool> {
    match bytes {
        b"CMP40HX_REBAR_V1=1\n" => Some(true),
        b"CMP40HX_REBAR_V1=0\n" => Some(false),
        _ => None,
    }
}

pub fn requested(options: &[u8], file_enabled: bool) -> bool {
    !has_load_option(options, "norebar")
        && (has_load_option(options, "--experimental-rebar") || file_enabled)
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Range {
    pub min: u64,
    pub max: u64,
}
impl Range {
    pub fn overlaps(self, base: u64, bytes: u64) -> bool {
        bytes
            .checked_sub(1)
            .and_then(|n| base.checked_add(n))
            .is_none_or(|end| base <= self.max && end >= self.min)
    }
}

/// ACPI resource-template descriptor; length always bounds the slice reads.
/// The returned size also advances past descriptors that are not memory.
pub fn parse_range(bytes: &[u8]) -> (Option<Range>, usize) {
    if bytes.len() < 3 {
        return (None, 0);
    }
    if bytes[0] == 0x79 {
        return (None, 2);
    }
    if bytes[0] & 0x80 == 0 {
        return (None, usize::from(bytes[0] & 7) + 1);
    }
    let len = usize::from(u16::from_le_bytes([bytes[1], bytes[2]]));
    let size = len + 3;
    if size > bytes.len() {
        return (None, size);
    }
    let read = |offset: usize, width: usize| {
        bytes[offset..offset + width]
            .iter()
            .enumerate()
            .fold(0, |v, (i, &b)| v | (u64::from(b) << (i * 8)))
    };
    let (min, mut max, span) = match bytes[0] & 0x7f {
        0x0a if len >= 0x2b => (read(14, 8), read(22, 8), read(38, 8)),
        0x07 if len >= 0x1b => (read(10, 4), read(14, 4), read(22, 4)),
        _ => return (None, size),
    };
    if max < min && span != 0 {
        if let Some(end) = min.checked_add(span - 1) {
            max = end;
        }
    }
    if bytes[3] != 0 || min > max {
        return (None, size);
    }
    (Some(Range { min, max }), size)
}

pub fn root_range(bytes: &[u8]) -> Option<Range> {
    let mut remaining = bytes;
    let mut best: Option<Range> = None;
    for _ in 0..128 {
        if remaining.first() == Some(&0x79) {
            break;
        }
        let (range, size) = parse_range(remaining);
        if let Some(cur) = range {
            if best.is_none_or(|old| {
                let high = cur.max >= 0x100000000;
                let old_high = old.max >= 0x100000000;
                (high && !old_high) || (high == old_high && cur.max - cur.min > old.max - old.min)
            }) {
                best = Some(cur);
            }
        }
        if size == 0 || size > remaining.len() {
            break;
        }
        remaining = &remaining[size..];
    }
    best
}

pub fn align_up(value: u64, alignment: u64) -> Option<u64> {
    if !alignment.is_power_of_two() {
        return None;
    }
    value
        .checked_add(alignment - 1)
        .map(|v| v & !(alignment - 1))
        .filter(|&v| v != 0)
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Bridge {
    pub root: usize,
    pub bdf: Bdf,
}

pub trait Io {
    fn gpu_read(&mut self, offset: u8) -> u32;
    fn gpu_write(&mut self, offset: u8, value: u32);
    fn root_count(&self) -> usize;
    fn bridge_read(&mut self, bridge: Bridge, offset: u8) -> u32;
    fn bridge_write(&mut self, bridge: Bridge, offset: u8, value: u32);
    fn mmio_read(&mut self, offset: u32) -> u32;
    fn mmio_write(&mut self, offset: u32, value: u32);
    // Fresh snapshots like C; failure returns zero/top or true/overlap.
    fn memory_top(&mut self) -> u64;
    fn memory_conflict(&mut self, base: u64, bytes: u64) -> bool;
    fn vram_read(&mut self, address: u64) -> u32;
    fn apertures(&mut self, _selected: usize) {}
}

fn bridges(
    io: &mut impl Io,
    target_bus: u8,
    mut visit: impl FnMut(&mut dyn Io, Bridge) -> bool,
) -> bool {
    for root in 0..io.root_count() {
        for bus in 0..target_bus {
            for device in 0..32 {
                let mut multi = true;
                for function in 0..8 {
                    if !multi {
                        break;
                    }
                    let bridge = Bridge {
                        root,
                        bdf: Bdf {
                            bus,
                            device,
                            function,
                        },
                    };
                    let id = io.bridge_read(bridge, 0);
                    if id == u32::MAX || id == 0 {
                        if function == 0 {
                            break;
                        }
                        continue;
                    }
                    let header = io.bridge_read(bridge, 0x0c);
                    if function == 0 && header & 0x00800000 == 0 {
                        multi = false;
                    }
                    if (header >> 16) & 0x7f == 1 && visit(io, bridge) {
                        return true;
                    }
                }
            }
        }
    }
    false
}

pub fn find_bridge(io: &mut impl Io, target_bus: u8) -> Option<Bridge> {
    let mut found = None;
    bridges(io, target_bus, |io, bridge| {
        if (io.bridge_read(bridge, 0x18) >> 8) & 0xff == u32::from(target_bus) {
            found = Some(bridge);
            true
        } else {
            false
        }
    });
    found
}

fn window(registers: [u32; 3]) -> Option<Range> {
    let [low, base_hi, limit_hi] = registers;
    if registers.contains(&u32::MAX) {
        return None;
    }
    let min = (u64::from((low >> 4) & 0xfff) << 20) | (u64::from(base_hi) << 32);
    let max = (u64::from((low >> 20) & 0xfff) << 20) | (u64::from(limit_hi) << 32) | 0xfffff;
    (min <= max).then_some(Range { min, max })
}

pub fn window_conflicts(
    io: &mut impl Io,
    target_bus: u8,
    selected: Bridge,
    base: u64,
    bytes: u64,
) -> bool {
    if bytes
        .checked_sub(1)
        .and_then(|n| base.checked_add(n))
        .is_none()
    {
        return true;
    }
    bridges(io, target_bus, |io, bridge| {
        if bridge == selected {
            return false;
        }
        let command = io.bridge_read(bridge, 4);
        if command == u32::MAX || command & 2 == 0 {
            return false;
        }
        let registers = [
            io.bridge_read(bridge, 0x24),
            io.bridge_read(bridge, 0x28),
            io.bridge_read(bridge, 0x2c),
        ];
        if registers[0] == 0 || registers[0] == 0xffff {
            return false;
        }
        window(registers).is_some_and(|range| range.overlaps(base, bytes))
    })
}

pub fn choose_window(
    io: &mut impl Io,
    target_bus: u8,
    bridge: Bridge,
    registers: [u32; 3],
    required: u64,
    alignment: u64,
) -> Option<(u64, u8)> {
    let old = window(registers)?;
    if old.max == u64::MAX || required == 0 {
        return None;
    }
    io.apertures(bridge.root);
    let anchor = io.memory_top().max(0x100000000);
    let step = align_up(required, alignment).unwrap_or(alignment);
    let first = align_up(anchor, alignment)?;
    for attempt in 0..12 {
        let gap = step.checked_mul(1 << attempt)?;
        let candidate = if attempt == 0 {
            first
        } else {
            first.checked_add(gap)?
        };
        candidate.checked_add(required - 1)?;
        if window_conflicts(io, target_bus, bridge, candidate, required)
            || io.memory_conflict(candidate, required)
        {
            continue;
        }
        return Some((candidate, attempt));
    }
    None
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Saved {
    pub xve: [u32; 4], // CYA, CFG, CAP, Control
    pub command: u32,
    pub bars: [u32; 4],
    pub bridge: Bridge,
    pub bridge_command: u32,
    pub window: [u32; 3],
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Outcome {
    NotPrefetchable,
    NoBridge,
    XveRejected,
    Bar1SizeRejected,
    Bar3SizeRejected,
    NoWindow,
    ApertureRejected,
    Active,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Report {
    pub outcome: Outcome,
    pub saved: Option<Saved>,
    pub bar1_probe: [u32; 2],
    pub bar3_bytes: u64,
    pub base: u64,
    pub bar3_base: u64,
    pub limit: u64,
    pub attempt: u8,
    pub readback: [u32; 2],
    pub vram: [u32; 2],
}

fn revert(io: &mut impl Io, saved: Saved) {
    io.bridge_write(saved.bridge, 4, saved.bridge_command & !2);
    for (offset, value) in [0x24, 0x28, 0x2c].into_iter().zip(saved.window) {
        io.bridge_write(saved.bridge, offset, value);
    }
    for (offset, value) in [0x14, 0x18, 0x1c, 0x20].into_iter().zip(saved.bars) {
        io.gpu_write(offset, value);
    }
    io.gpu_write(4, saved.command);
    io.bridge_write(saved.bridge, 4, saved.bridge_command);
    io.mmio_write(CONTROL, saved.xve[3]);
    io.mmio_write(CFG, saved.xve[1]);
    io.mmio_write(CYA, saved.xve[0]);
    io.mmio_read(CYA);
}

/// Call only after native CPU-RM completion and effective opt-in. Outcomes
/// after mutation attempt the C rollback and permit its best-effort handoff.
pub fn relocate(io: &mut impl Io, target_bus: u8) -> Report {
    let mut report = Report {
        outcome: Outcome::NotPrefetchable,
        saved: None,
        bar1_probe: [0; 2],
        bar3_bytes: 0,
        base: 0,
        bar3_base: 0,
        limit: 0,
        attempt: 0,
        readback: [0; 2],
        vram: [0; 2],
    };
    let xve = [
        io.mmio_read(CYA),
        io.mmio_read(CFG),
        io.mmio_read(CAP),
        io.mmio_read(CONTROL),
    ];
    let command = io.gpu_read(4);
    let bars = [
        io.gpu_read(0x14),
        io.gpu_read(0x18),
        io.gpu_read(0x1c),
        io.gpu_read(0x20),
    ];
    if bars[0] & 15 != 12 {
        return report;
    }
    report.outcome = Outcome::NoBridge;
    let Some(bridge) = find_bridge(io, target_bus) else {
        return report;
    };
    let saved = Saved {
        xve,
        command,
        bars,
        bridge,
        bridge_command: io.bridge_read(bridge, 4),
        window: [
            io.bridge_read(bridge, 0x24),
            io.bridge_read(bridge, 0x28),
            io.bridge_read(bridge, 0x2c),
        ],
    };
    report.saved = Some(saved);
    io.mmio_write(CYA, 0x30);
    io.mmio_write(CFG, (xve[1] & !15) | ENABLE | SELECTOR);
    if io.mmio_read(CFG) & (ENABLE | 15) != ENABLE | SELECTOR {
        io.mmio_read(CFG); // C's failure diagnostic read.
        io.mmio_write(CFG, xve[1]);
        io.mmio_write(CYA, xve[0]);
        report.outcome = Outcome::XveRejected;
        return report;
    }
    io.mmio_write(CONTROL, (xve[3] & !0x3f00) | (13 << 8));
    for offset in [CYA, CFG, CAP, CONTROL] {
        io.mmio_read(offset);
    }
    io.gpu_write(4, command & !2);
    io.bridge_write(bridge, 4, saved.bridge_command & !2);
    io.gpu_write(0x14, u32::MAX);
    io.gpu_write(0x18, u32::MAX);
    report.bar1_probe = [io.gpu_read(0x14), io.gpu_read(0x18)];
    let failure = resize(io, target_bus, saved, &mut report);
    if let Some(outcome) = failure {
        report.outcome = outcome;
        revert(io, saved);
    } else {
        report.outcome = Outcome::Active;
    }
    report
}

fn resize(io: &mut impl Io, target_bus: u8, saved: Saved, report: &mut Report) -> Option<Outcome> {
    if report.bar1_probe[0] & !15 != 0 || report.bar1_probe[1] != 0xfffffffe {
        return Some(Outcome::Bar1SizeRejected);
    }
    io.gpu_write(0x1c, u32::MAX);
    io.gpu_write(0x20, u32::MAX);
    let mask = u64::from(io.gpu_read(0x1c) & !15) | (u64::from(io.gpu_read(0x20)) << 32);
    let size = if mask == 0 {
        0x2000000
    } else {
        (!mask).wrapping_add(1)
    };
    report.bar3_bytes = size;
    if !size.is_power_of_two() || size < 0x1000 || BAR1_BYTES.checked_add(size).is_none() {
        return Some(Outcome::Bar3SizeRejected);
    }
    let Some(required) = align_up(BAR1_BYTES, size).and_then(|n| n.checked_add(size)) else {
        return Some(Outcome::NoWindow);
    };
    let Some((base, attempt)) = choose_window(
        io,
        target_bus,
        saved.bridge,
        saved.window,
        required,
        BAR1_BYTES,
    ) else {
        return Some(Outcome::NoWindow);
    };
    report.base = base;
    report.attempt = attempt;
    // The chosen window has already been checked for this complete span.
    report.bar3_base = (base + BAR1_BYTES + size - 1) & !(size - 1);
    report.limit = report.bar3_base + size - 1;
    for (offset, value) in [
        (0x14, base as u32 | 12),
        (0x18, (base >> 32) as u32),
        (0x1c, report.bar3_base as u32 | 12),
        (0x20, (report.bar3_base >> 32) as u32),
    ] {
        io.gpu_write(offset, value);
    }
    let low = 0x00010001
        | (((base >> 20) & 0xfff) << 4) as u32
        | (((report.limit >> 20) & 0xfff) << 20) as u32;
    io.bridge_write(saved.bridge, 0x24, low);
    io.bridge_write(saved.bridge, 0x28, (base >> 32) as u32);
    io.bridge_write(saved.bridge, 0x2c, (report.limit >> 32) as u32);
    io.bridge_write(saved.bridge, 4, saved.bridge_command);
    io.gpu_write(4, saved.command);
    report.readback = [io.gpu_read(0x14), io.gpu_read(0x18)];
    report.vram = [io.vram_read(base), io.vram_read(base + BAR1_BYTES / 2)];
    let assigned = u64::from(report.readback[0] & !15) | (u64::from(report.readback[1]) << 32);
    (assigned != base || report.vram == [u32::MAX; 2]).then_some(Outcome::ApertureRejected)
}
