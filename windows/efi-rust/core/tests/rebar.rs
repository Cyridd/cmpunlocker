// SPDX-License-Identifier: MIT
//! Exact PCI/MMIO traces against the C EFI, including every rollback path.
use cmp40_efi_core::{
    pci::Bdf,
    rebar::{self, Bridge, Io, Outcome},
};
use std::{
    fmt::Write as _,
    io::Write as _,
    process::{Command, Stdio},
};

type Event = [u32; 5];
struct Model {
    fault: u32,
    size3: u64,
    top: u64,
    gpu: [u32; 64],
    bridge: [[[u32; 64]; 2]; 2],
    xve: [u32; 4],
    map_calls: usize,
    events: Vec<Event>,
}
impl Model {
    fn new(fault: u32, size3: u64, top: u64) -> Self {
        let mut model = Self {
            fault,
            size3,
            top,
            gpu: [0; 64],
            bridge: [[[u32::MAX; 64]; 2]; 2],
            xve: [0x12, 0x12340000, 0x400, 0x6a5],
            map_calls: 0,
            events: Vec::new(),
        };
        model.gpu[1] = 7;
        model.gpu[0x14 / 4] = if fault == 1 { 8 } else { 0xf400000c };
        model.gpu[0x18 / 4] = 0x7f;
        model.gpu[0x1c / 4] = 0xf800000c;
        model.gpu[0x20 / 4] = 0x7f;
        let root = usize::from(fault == 22);
        let parent = &mut model.bridge[root][0];
        *parent = [0; 64];
        parent[0] = 0x12348086;
        parent[3] = 0x00010000;
        parent[6] = if fault == 2 { 0x30300 } else { 0x20200 };
        parent[1] = 7;
        parent[9] = 0xf9f1f401;
        parent[10] = 0x7f;
        parent[11] = 0x7f;
        match fault {
            9 => parent[9] = u32::MAX,
            10 | 24 => parent[9] = 0xffff,
            11 => {
                parent[9] = 0xfff1f401;
                parent[11] = u32::MAX;
            }
            20 => parent[6] = 0x30200,
            _ => (),
        }
        if fault == 12 || fault == 20 {
            let other = &mut model.bridge[0][1];
            *other = [0; 64];
            for (i, v) in [
                (0, 0x43218086),
                (3, 0x00010000),
                (6, 0x20100),
                (1, 7),
                (9, 0x00110001),
                (10, 8),
                (11, 9),
            ] {
                other[i] = v;
            }
        }
        model
    }
    fn xve_index(offset: u32) -> usize {
        match offset {
            rebar::CYA => 0,
            rebar::CFG => 1,
            rebar::CAP => 2,
            rebar::CONTROL => 3,
            _ => panic!("unexpected XVE register"),
        }
    }
    fn key(bridge: Bridge) -> u32 {
        ((bridge.root as u32) << 24)
            | (u32::from(bridge.bdf.bus) << 16)
            | (u32::from(bridge.bdf.device) << 8)
            | u32::from(bridge.bdf.function)
    }
}
impl Io for Model {
    fn gpu_read(&mut self, offset: u8) -> u32 {
        let mut value = self.gpu[usize::from(offset) / 4];
        if value == u32::MAX && [0x14, 0x18, 0x1c, 0x20].contains(&offset) {
            let size = if offset < 0x1c {
                if self.fault == 4 {
                    0x4000000
                } else {
                    rebar::BAR1_BYTES
                }
            } else {
                self.size3
            };
            let mask = if size == 0 { 0 } else { !(size - 1) };
            value = if offset == 0x14 || offset == 0x1c {
                mask as u32 | 12
            } else {
                (mask >> 32) as u32
            };
        }
        if self.fault == 18 && offset == 0x18 && value != u32::MAX && self.gpu[0x14 / 4] == 12 {
            value ^= 1;
        }
        self.events.push([2, u32::from(offset), value, 0, 0]);
        value
    }
    fn gpu_write(&mut self, offset: u8, value: u32) {
        self.events.push([3, u32::from(offset), value, 0, 0]);
        self.gpu[usize::from(offset) / 4] = value;
    }
    fn root_count(&self) -> usize {
        2
    }
    fn bridge_read(&mut self, bridge: Bridge, offset: u8) -> u32 {
        let bdf = bridge.bdf;
        let mut value = u32::MAX;
        if bridge.root < 2
            && bdf.bus == 0
            && bdf.device < 2
            && bdf.function == u8::from(self.fault == 21 && bdf.device == 0)
        {
            value = self.bridge[bridge.root][usize::from(bdf.device)][usize::from(offset) / 4];
        }
        if self.fault == 21 && bdf.bus == 0 && bdf.device == 0 && bdf.function == 0 {
            value = match offset {
                0 => 0x12348086,
                0x0c => 0x00800000,
                _ => 0,
            };
        }
        self.events
            .push([4, Self::key(bridge), u32::from(offset), value, 0]);
        value
    }
    fn bridge_write(&mut self, bridge: Bridge, offset: u8, value: u32) {
        self.events
            .push([5, Self::key(bridge), u32::from(offset), value, 0]);
        if bridge.root < 2 && bridge.bdf.bus == 0 && bridge.bdf.device < 2 {
            self.bridge[bridge.root][usize::from(bridge.bdf.device)][usize::from(offset) / 4] =
                value;
        }
    }
    fn mmio_read(&mut self, offset: u32) -> u32 {
        let value = self.xve[Self::xve_index(offset)];
        self.events.push([0, offset, value, 0, 0]);
        value
    }
    fn mmio_write(&mut self, offset: u32, value: u32) {
        self.events.push([1, offset, value, 0, 0]);
        if self.fault == 3 && offset == rebar::CFG {
            return;
        }
        self.xve[Self::xve_index(offset)] = value;
        if offset == rebar::CFG {
            self.xve[2] = if value & 0x80000000 != 0 {
                0x7fc00
            } else {
                0x400
            };
        }
    }
    fn memory_top(&mut self) -> u64 {
        self.events
            .push([6, self.top as u32, (self.top >> 32) as u32, 0, 0]);
        self.top
    }
    fn memory_conflict(&mut self, base: u64, bytes: u64) -> bool {
        let hit = self.fault == 14 || (self.fault == 13 && self.map_calls == 0);
        self.map_calls += 1;
        self.events.push([
            7,
            base as u32,
            (base >> 32) as u32,
            bytes as u32,
            (bytes >> 32) as u32,
        ]);
        self.events.push([8, u32::from(hit), 0, 0, 0]);
        hit
    }
    fn vram_read(&mut self, address: u64) -> u32 {
        let value = if self.fault == 17 || (self.fault == 19 && address % rebar::BAR1_BYTES == 0) {
            u32::MAX
        } else {
            0x13579bdf
        };
        self.events
            .push([9, address as u32, (address >> 32) as u32, value, 0]);
        value
    }
}

fn reference(mode: &str, input: &str) -> Vec<u8> {
    let script = concat!(env!("CARGO_MANIFEST_DIR"), "/tests/c_rebar_reference.py");
    let mut child = Command::new(std::env::var("PYTHON").unwrap_or_else(|_| "python3".into()))
        .args([script, mode])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .expect("C ReBAR comparisons require Python 3 and CC (default gcc)");
    child
        .stdin
        .take()
        .unwrap()
        .write_all(input.as_bytes())
        .unwrap();
    let result = child.wait_with_output().unwrap();
    assert!(
        result.status.success(),
        "C reference failed: {}",
        String::from_utf8_lossy(&result.stderr)
    );
    result.stdout
}
fn canonical(events: &[Event]) -> Vec<Event> {
    let mut events = events.to_vec();
    let mut index = 0;
    while index < events.len() {
        if [0, 2, 4, 9].contains(&events[index][0]) {
            let start = index;
            while index < events.len() && [0, 2, 4, 9].contains(&events[index][0]) {
                index += 1;
            }
            events[start..index].sort();
        } else {
            index += 1;
        }
    }
    events
}

#[test]
fn relocation_and_all_rollback_traces_match_actual_c() {
    let mut cases: Vec<_> = (0..25)
        .map(|fault| {
            let size = match fault {
                5 => 0,
                6 => 512,
                7 => 0x3000,
                8 => 1 << 63,
                _ => 0x2000000,
            };
            let top = match fault {
                15 => 0,
                16 => u64::MAX,
                _ => 0x800000000,
            };
            (fault, size, top)
        })
        .collect();
    for bits in 12..=36 {
        for top in [0x100000000, 0x712345000, 0x7ff4000000, 0x10000000000] {
            cases.push((0, 1 << bits, top));
        }
    }
    let mut input = String::new();
    for &(fault, size, top) in &cases {
        writeln!(input, "{fault} {size} {top}").unwrap();
    }
    let bytes = reference("relocate", &input);
    let rows: Vec<Event> = bytes
        .chunks_exact(20)
        .map(|row| {
            core::array::from_fn(|i| u32::from_le_bytes(row[i * 4..i * 4 + 4].try_into().unwrap()))
        })
        .collect();
    assert_eq!(bytes.len() % 20, 0);
    let mut cursor = 0;
    for (fault, size, top) in cases {
        let mut model = Model::new(fault, size, top);
        let before_gpu = model.gpu;
        let before_bridge = model.bridge;
        let before_xve = model.xve;
        let result = rebar::relocate(&mut model, 2);
        let expected = match fault {
            1 => Outcome::NotPrefetchable,
            2 => Outcome::NoBridge,
            3 => Outcome::XveRejected,
            4 => Outcome::Bar1SizeRejected,
            6 | 7 => Outcome::Bar3SizeRejected,
            8 | 9 | 10 | 11 | 14 | 16 | 24 => Outcome::NoWindow,
            17 | 18 => Outcome::ApertureRejected,
            _ => Outcome::Active,
        };
        assert_eq!(
            result.outcome, expected,
            "outcome fault={fault} size={size:x} top={top:x}"
        );
        let end = cursor + rows[cursor..].iter().position(|r| r[0] == 10).unwrap();
        assert_eq!(
            canonical(&model.events),
            canonical(&rows[cursor..end]),
            "fault={fault} size={size:x} top={top:x}"
        );
        cursor = end + 1;
        if result.outcome != Outcome::Active {
            assert_eq!(model.gpu, before_gpu, "GPU restored fault {fault}");
            assert_eq!(model.bridge, before_bridge, "bridge restored fault {fault}");
            assert_eq!(model.xve, before_xve, "XVE restored fault {fault}");
        } else {
            assert!(result.base >= 0x100000000 && result.base % rebar::BAR1_BYTES == 0);
            assert_ne!(result.vram, [u32::MAX; 2]);
        }
    }
    assert_eq!(cursor, rows.len());
}

fn byte_rows(cases: &[Vec<u8>]) -> String {
    let mut input = String::new();
    for bytes in cases {
        write!(input, "{}", bytes.len()).unwrap();
        for b in bytes {
            write!(input, " {b}").unwrap();
        }
        input.push('\n');
    }
    input
}

#[test]
fn policy_bytes_match_c_and_norebar_overrides_both_opt_ins() {
    let mut cases = vec![
        vec![],
        b"CMP40HX_REBAR_V1=1\n".to_vec(),
        b"CMP40HX_REBAR_V1=0\n".to_vec(),
    ];
    for bytes in [
        b"CMP40HX_REBAR_V1=1\r\n".as_slice(),
        b"CMP40HX_REBAR_V1=1",
        b"CMP40HX_REBAR_V1=1\n\0",
        b" CMP40HX_REBAR_V1=1\n",
    ] {
        cases.push(bytes.to_vec());
    }
    let output = reference("policy", &byte_rows(&cases));
    for (bytes, row) in cases.iter().zip(output.chunks_exact(4)) {
        assert_eq!(
            rebar::file_policy(bytes).map_or(-1, i32::from),
            i32::from_le_bytes(row.try_into().unwrap())
        );
    }
    assert_eq!(output.len(), cases.len() * 4);
    for (text, enabled) in [
        ("", false),
        ("--experimental-rebar", true),
        ("norebar --experimental-rebar", false),
        ("--experimental-rebar-other", false),
        ("--EXPERIMENTAL-REBAR", false),
        ("\0--experimental-rebar", false),
    ] {
        let bytes: Vec<u8> = text.encode_utf16().flat_map(u16::to_le_bytes).collect();
        assert_eq!(rebar::requested(&bytes, false), enabled);
        assert_eq!(rebar::requested(&bytes, true), !text.starts_with("norebar"));
    }
}

#[test]
fn acpi_descriptors_match_c_and_truncated_templates_are_bounded() {
    let mut cases = vec![
        vec![],
        vec![0x79, 0, 0],
        vec![0x22, 0, 0],
        vec![0x8a, 0xff, 0xff],
    ];
    for qword in [false, true] {
        for (min, max, span) in [
            (0, 0xfff, 0x1000),
            (0x100000000, 0x7fffffffff, 0x7f00000000),
            (0x100000000, 0, 0x200000000),
            (u64::MAX - 16, 0, 32),
        ] {
            let mut bytes = vec![0; if qword { 46 } else { 30 }];
            bytes[0] = if qword { 0x8a } else { 0x87 };
            bytes[1] = (bytes.len() - 3) as u8;
            if qword {
                for (offset, value) in [(14, min), (22, max), (38, span)] {
                    bytes[offset..offset + 8].copy_from_slice(&value.to_le_bytes());
                }
            } else {
                for (offset, value) in [(10, min), (14, max), (22, span)] {
                    bytes[offset..offset + 4].copy_from_slice(&(value as u32).to_le_bytes());
                }
            }
            for len in 0..=bytes.len() {
                cases.push(bytes[..len].to_vec());
            }
            bytes[3] = 1;
            cases.push(bytes);
        }
    }
    let output = reference("range", &byte_rows(&cases));
    assert_eq!(output.len(), cases.len() * 32);
    for (bytes, row) in cases.iter().zip(output.chunks_exact(32)) {
        let fields: [u64; 4] =
            core::array::from_fn(|i| u64::from_le_bytes(row[i * 8..i * 8 + 8].try_into().unwrap()));
        let (range, size) = rebar::parse_range(bytes);
        assert_eq!(range.is_some(), fields[0] != 0);
        assert_eq!(size as u64, fields[1]);
        if let Some(range) = range {
            assert_eq!([range.min, range.max], [fields[2], fields[3]]);
        }
    }
    assert_eq!(rebar::align_up(u64::MAX, rebar::BAR1_BYTES), None);
    assert_eq!(rebar::align_up(16, 3), None);
    let selected = Bridge {
        root: 0,
        bdf: Bdf {
            bus: 0,
            device: 0,
            function: 0,
        },
    };
    let mut model = Model::new(0, 0x2000000, 0x800000000);
    assert!(rebar::window_conflicts(
        &mut model,
        2,
        selected,
        u64::MAX,
        2
    ));
    assert!(model.events.is_empty());
}

#[test]
fn diagnostic_root_range_prefers_high_aperture_without_constraining_placement() {
    fn qword(min: u64, max: u64) -> Vec<u8> {
        let mut descriptor = vec![0; 46];
        descriptor[0] = 0x8a;
        descriptor[1] = 43;
        descriptor[14..22].copy_from_slice(&min.to_le_bytes());
        descriptor[22..30].copy_from_slice(&max.to_le_bytes());
        descriptor[38..46].copy_from_slice(&(max - min + 1).to_le_bytes());
        descriptor
    }
    let low = qword(0, 0xffffffff);
    let high = qword(0x7fe0000000, 0x7ff9ffffff);
    let mut list = low;
    list.extend_from_slice(&high);
    list.extend_from_slice(&[0x79, 0]);
    assert_eq!(
        rebar::root_range(&list),
        Some(rebar::Range {
            min: 0x7fe0000000,
            max: 0x7ff9ffffff
        })
    );
    assert_eq!(rebar::root_range(&[0x8a, 0xff, 0xff]), None);
    let mut model = Model::new(0, 0x2000000, 0x800000000);
    let result = rebar::relocate(&mut model, 2);
    assert_eq!(result.outcome, Outcome::Active);
    assert!(result.base < 0x7fe0000000);
}
