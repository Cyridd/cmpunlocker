// SPDX-License-Identifier: MIT
//! Host policy/PCI models. No driver, MMIO or firmware instruction is executed.
use cmp40_efi_core::{
    gen2::{self, Action, Device, Error, Io, LinkState, Outcome, ReadIo},
    pci::{Bdf, CMP40_ID},
    rebar::Bridge,
    transaction::reg,
};
use std::{
    collections::BTreeMap,
    fmt::Write as _,
    io::Write as _,
    process::{Command, Stdio},
};

const GPU: Bdf = Bdf {
    bus: 2,
    device: 0,
    function: 0,
};
const PORT: Bridge = Bridge {
    root: 0,
    bdf: Bdf {
        bus: 0,
        device: 1,
        function: 1,
    },
};
const GC: u8 = 0x70;
const PC: u8 = 0x40;
#[derive(Default)]
struct ConfigMap(BTreeMap<(u16, u8), u32>);
impl ConfigMap {
    fn key((bdf, offset): (Bdf, u8)) -> (u16, u8) {
        (
            (u16::from(bdf.bus) << 8) | (u16::from(bdf.device) << 3) | u16::from(bdf.function),
            offset,
        )
    }
    fn insert(&mut self, key: (Bdf, u8), value: u32) {
        self.0.insert(Self::key(key), value);
    }
    fn get(&self, key: &(Bdf, u8)) -> Option<&u32> {
        self.0.get(&Self::key(*key))
    }
}
impl std::ops::Index<&(Bdf, u8)> for ConfigMap {
    type Output = u32;
    fn index(&self, key: &(Bdf, u8)) -> &Self::Output {
        &self.0[&Self::key(*key)]
    }
}
struct Model {
    config: ConfigMap,
    mmio: BTreeMap<u32, u32>,
    writes: Vec<(Device, u8, u16)>,
    policy: Vec<(u32, u32)>,
    reads: Vec<(Device, u8)>,
    delays: Vec<u32>,
    fault: u8,
    requests: u8,
    polls: u32,
}
impl Model {
    fn new(fault: u8) -> Self {
        let mut m = Self {
            config: ConfigMap::default(),
            mmio: BTreeMap::new(),
            writes: vec![],
            policy: vec![],
            reads: vec![],
            delays: vec![],
            fault,
            requests: 0,
            polls: 0,
        };
        let zero = Bdf {
            function: 0,
            ..PORT.bdf
        };
        for (b, r, v) in [
            (GPU, 0, CMP40_ID),
            (GPU, 0xc, 0),
            (GPU, 8, 0x03020000),
            (GPU, 0x34, u32::from(GC)),
            (GPU, GC, 0x00010010),
            (GPU, GC + 12, 0x00453d01),
            (GPU, GC + 0x10, 0x11010140),
            (GPU, GC + 0x2c, 2),
            (GPU, GC + 0x30, 0x8001),
            (zero, 0, 0x12341022),
            (zero, 0xc, 0x00800000),
            (PORT.bdf, 0, 0x12351022),
            (PORT.bdf, 0xc, 0x00810000),
            (PORT.bdf, 8, 0x06040000),
            (PORT.bdf, 0x18, 0x00020200),
            (PORT.bdf, 0x34, u32::from(PC)),
            (PORT.bdf, PC, 0x00420010),
            (PORT.bdf, PC + 12, 0x00453d04),
            (PORT.bdf, PC + 0x10, 0x31010040),
            (PORT.bdf, PC + 0x2c, 0x1e),
            (PORT.bdf, PC + 0x30, 0x8004),
        ] {
            m.config.insert((b, r), v);
        }
        for (r, v) in [
            (0, 0x166000a1),
            (reg::SS0, 0x88888888),
            (reg::SS1, 8),
            (reg::WPR_LO, 0x1ffffe00),
            (reg::WPR_HI, 0),
            (reg::FEAT_PLM, 0xffffff8f),
            (reg::SEC2 + reg::CPU, 0x10),
            (reg::SEC2 + reg::MBOX0, 0),
            (reg::GSP + reg::CPU, 0x10),
            (gen2::MISC, 0x20342500),
            (gen2::CYA, 0x068711b7),
            (gen2::CFG, 0x80045800),
            (gen2::PL, 0x00120036),
            (gen2::OVR, 0),
        ] {
            m.mmio.insert(r, v);
        }
        match fault {
            1 => {
                m.config.insert((Bdf { device: 1, ..GPU }, 0), 0x12348086);
            }
            2 => {
                m.config.insert((PORT.bdf, 0x18), 0x00030200);
            }
            3 => {
                m.mmio.insert(reg::SS0, 0);
            }
            4 => {
                m.mmio.insert(gen2::PL, 0xbadf0000);
            }
            5 => {
                m.config.insert((GPU, 0x34), 0x40);
                m.config.insert((GPU, 0x40), 0x00004001);
            }
            6 => {
                m.config.insert((PORT.bdf, PC + 0x2c), 2);
            }
            7 => {
                m.config.insert((PORT.bdf, PC + 0x10), 0x31010050);
            }
            17 => {
                m.config.insert((PORT.bdf, PC + 0x30), 0x8414);
            }
            18 => {
                m.config.insert((PORT.bdf, 0x18), 0x00020300);
            }
            _ => (),
        }
        m
    }
    fn bdf(device: Device) -> Bdf {
        match device {
            Device::Gpu => GPU,
            Device::Bus(bdf) => bdf,
            Device::Port(p) => p.bdf,
        }
    }
    fn promote(&mut self) {
        for (r, v) in [(GC, 0x00020010), (GC + 12, 0x00453d02), (GC + 0x2c, 6)] {
            self.config.insert((GPU, r), v);
        }
    }
}
impl ReadIo for Model {
    fn gpu_bdf(&self) -> Bdf {
        GPU
    }
    fn config_read(&mut self, d: Device, r: u8) -> u32 {
        self.reads.push((d, r));
        if d == Device::Gpu && [GC + 0x2c, GC + 0x30].contains(&r) {
            assert!(
                self.config[&(GPU, GC)] >> 16 & 15 >= 2,
                "v2 register accessed before adoption"
            );
        }
        if self.fault == 23 && d == Device::Port(Bridge { root: 1, ..PORT }) && r == 0 {
            return 0x12361022;
        }
        *self.config.get(&(Self::bdf(d), r)).unwrap_or(&u32::MAX)
    }
    fn mmio_read(&mut self, r: u32) -> u32 {
        *self.mmio.get(&r).unwrap_or(&0)
    }
}
impl Io for Model {
    fn root_count(&self) -> usize {
        if [22, 23].contains(&self.fault) {
            2
        } else {
            1
        }
    }
    fn config_write16(&mut self, d: Device, r: u8, v: u16) -> bool {
        assert!(
            matches!(
                (d, r),
                (Device::Gpu, 0xa0) | (Device::Port(PORT), 0x70) | (Device::Port(PORT), 0x50)
            ),
            "write outside TLS/port RL WORD"
        );
        self.writes.push((d, r, v));
        let b = Self::bdf(d);
        let old = self.config[&(b, r)];
        let mask = if r == PC + 0x10 && b == PORT.bdf {
            0x20
        } else {
            15
        };
        assert_eq!(
            u32::from(v) & !mask,
            old & 0xffff & !mask,
            "unrelated control bits changed"
        );
        if self.fault == 9 && d == Device::Gpu && v & 15 == 2 {
            return false;
        }
        if self.fault == 10 && d == Device::Gpu && v & 15 == 2 {
            return true;
        }
        if self.fault == 24 && d == Device::Gpu && v & 15 == 1 {
            return false;
        }
        if r == PC + 0x10 && b == PORT.bdf && v & 0x20 != 0 {
            self.requests += 1;
            if self.fault == 11 {
                return false;
            }
        }
        self.config.insert(
            (b, r),
            old & 0xffff0000
                | u32::from(v)
                    & !if r == PC + 0x10 && b == PORT.bdf {
                        0x20
                    } else {
                        0
                    },
        );
        if self.fault == 21 && d == Device::Port(PORT) && r == PC + 0x30 && v & 15 == 2 {
            self.config.insert((GPU, GC + 0x10), 0x19010140);
        }
        true
    }
    fn mmio_write(&mut self, r: u32, v: u32) {
        assert!(gen2::POLICY.iter().any(|&(offset, _, _)| offset == r));
        self.policy.push((r, v));
        if self.fault == 8 && r == gen2::CFG {
            return;
        }
        self.mmio.insert(r, v);
        if r == gen2::OVR && v == 6 && self.fault != 15 {
            self.promote();
        }
    }
    fn delay_us(&mut self, us: u32) {
        self.delays.push(us);
        if self.fault == 20 && us == 50000 {
            self.config
                .insert((Bdf { device: 1, ..GPU }, 0), 0x12348086);
        }
        if self.requests != 0 && us == 10000 {
            self.polls += 1;
            if self.fault == 12 {
                return;
            }
            for (b, r, v) in [
                (GPU, GC + 0x10, 0x11020140),
                (PORT.bdf, PC + 0x10, 0x31020040),
            ] {
                self.config.insert((b, r), v);
            }
            match self.fault {
                13 | 24 => {
                    self.config.insert((GPU, GC + 0x10), 0x10820140);
                }
                14 => {
                    self.config.insert((PORT.bdf, PC + 0x10), u32::MAX);
                }
                16 => {
                    self.config.insert((GPU, GC + 0x10), 0x11010140);
                    self.config.insert((PORT.bdf, PC + 0x10), 0x39010040);
                }
                19 => {
                    self.mmio.insert(reg::SS1, 0);
                }
                25 => {
                    self.config.insert((GPU, GC + 0x30), 0x8003);
                    self.config.insert((GPU, GC + 0x10), 0x10820140);
                }
                26 => {
                    self.config.insert((GPU, GC + 0x30), 0x8003);
                }
                _ => (),
            }
        }
    }
}

#[test]
fn separated_modes_expose_capability_targets_and_only_one_upstream_retrain() {
    for (action, outcome) in [
        (Action::Off, Outcome::Off),
        (Action::Prepare, Outcome::Prepared),
        (Action::Targets, Outcome::TargetsSeeded),
        (Action::Retrain, Outcome::Gen2),
    ] {
        let mut m = Model::new(0);
        let r = gen2::run(&mut m, action);
        assert_eq!(r.result, Ok(outcome));
        if action == Action::Off {
            assert!(m.reads.is_empty() && m.policy.is_empty() && m.writes.is_empty());
            continue;
        }
        assert_eq!(
            m.policy,
            vec![
                (gen2::MISC, 0x20342d00),
                (gen2::CYA, 0x068711b3),
                (gen2::CFG, 0x80085800),
                (gen2::PL, 0x00140036),
                (gen2::OVR, 6)
            ]
        );
        assert_eq!(r.after[0].width(), 16);
        assert_eq!(m.requests, u8::from(action == Action::Retrain));
        if action == Action::Prepare {
            assert!(m.writes.is_empty());
        } else {
            assert_eq!(m.config[&(GPU, GC + 0x30)] & 15, 2);
            assert_eq!(m.config[&(PORT.bdf, PC + 0x30)] & 15, 2);
        }
        // GPU v2-only fields were read only after the adoption write.
        assert_eq!(r.before[0].flags & 15, 1);
    }
}
#[test]
fn invalid_identity_topology_or_registers_refuse_before_gen2_writes() {
    for fault in [1, 2, 3, 4, 5, 6, 7, 18] {
        let mut m = Model::new(fault);
        let r = gen2::run(&mut m, Action::Retrain);
        assert!(r.result.is_err(), "fault {fault}");
        assert!(
            !r.mutated && m.policy.is_empty() && m.writes.is_empty(),
            "fault {fault}"
        );
    }
}
#[test]
fn failures_stop_and_restore_owned_tls_without_resets_or_retries() {
    for fault in [8, 9, 10, 11, 13, 14, 15, 16, 17, 19] {
        let mut m = Model::new(fault);
        let r = gen2::run(&mut m, Action::Retrain);
        assert!(r.result.is_err(), "fault {fault}");
        assert!(m.requests <= 1);
        if m.requests > 0 || [9, 10].contains(&fault) {
            assert!(r.targets_restored, "fault {fault}: {:?}", r.result);
            assert_eq!(m.config[&(GPU, GC + 0x30)] & 15, 1);
            assert_eq!(m.config[&(PORT.bdf, PC + 0x30)] & 15, 4);
        }
        if fault == 8 {
            assert_eq!(r.result, Err(Error::PolicyReadback));
            assert!(m.writes.is_empty());
        }
    }
}
#[test]
fn healthy_gen1_timeout_preserves_the_early_seed_and_is_not_gen2_success() {
    let mut m = Model::new(12);
    let r = gen2::run(&mut m, Action::Retrain);
    assert_eq!(r.result, Ok(Outcome::Gen1Timeout));
    assert_eq!(r.waited_us, 2000000);
    assert_eq!(m.requests, 1);
    assert_eq!(r.after[0].speed(), 1);
    assert_eq!(r.after[1].speed(), 1);
    assert_eq!(r.saved_tls, [0x8001, 0x8004]);
    assert_eq!(r.after[0].control2 & 15, 2);
    assert!(!r.targets_restored);
}
#[test]
fn retained_capability_does_not_skip_policy_and_faster_links_are_preserved() {
    for speed in [1, 2, 3] {
        let mut m = Model::new(0);
        m.promote();
        for (b, r) in [(GPU, GC + 0x10), (PORT.bdf, PC + 0x10)] {
            let old = m.config[&(b, r)];
            m.config.insert((b, r), (old & !0xf0000) | (speed << 16));
        }
        let result = gen2::run(&mut m, Action::Retrain);
        assert!(result.result.is_ok());
        assert_eq!(m.policy.len(), 4);
        assert!(!m.policy.iter().any(|&(off, _)| off == gen2::OVR));
        if speed >= 2 {
            assert_eq!(m.requests, 0);
        }
        if speed == 3 {
            assert!(m.writes.is_empty());
            assert_eq!(result.result, Ok(Outcome::AlreadyFast));
        }
    }
}

#[test]
fn fresh_topology_and_link_checks_prevent_retrain_after_state_changes() {
    for fault in [20, 21, 23] {
        let mut m = Model::new(fault);
        let r = gen2::run(&mut m, Action::Retrain);
        assert!(r.result.is_err());
        assert_eq!(m.requests, 0);
        if fault == 23 {
            assert!(!r.mutated && m.writes.is_empty() && m.policy.is_empty());
        }
        if fault == 20 {
            assert!(m.writes.is_empty());
        }
    }
    let mut aliases = Model::new(22);
    assert_eq!(
        gen2::run(&mut aliases, Action::Retrain).result,
        Ok(Outcome::Gen2)
    );
    assert_eq!(aliases.requests, 1);
}

#[test]
fn restoration_failure_and_externally_changed_tls_are_reported() {
    for fault in [24, 25, 26] {
        let mut m = Model::new(fault);
        let r = gen2::run(&mut m, Action::Retrain);
        assert_eq!(r.result, Err(Error::RestoreFailed));
        assert!(!r.targets_restored);
        assert_eq!(m.requests, 1);
        if [25, 26].contains(&fault) {
            assert_eq!(m.config[&(GPU, GC + 0x30)] & 15, 3);
            assert!(!m
                .writes
                .iter()
                .any(|&(d, off, v)| d == Device::Gpu && off == GC + 0x30 && v & 15 == 1));
        }
    }
}

#[test]
fn masked_policy_writes_match_the_actual_c_reference_over_varied_inputs() {
    let mut cases = vec![];
    let mut seed = 0x31415926u32;
    for adopt in [false, true] {
        for _ in 0..64 {
            let values = core::array::from_fn::<_, 5, _>(|_| {
                seed = seed.wrapping_mul(1664525).wrapping_add(1013904223);
                seed & 0x7fffffff
            });
            cases.push((adopt, values));
        }
    }
    let mut input = String::new();
    let mut expected = vec![];
    for &(adopt, values) in &cases {
        let mut m = Model::new(0);
        if !adopt {
            m.promote();
        }
        for ((off, _, _), value) in gen2::POLICY.iter().zip(values) {
            m.mmio.insert(*off, value);
        }
        let r = gen2::run(&mut m, Action::Prepare);
        assert_eq!(r.result, Ok(Outcome::Prepared));
        write!(input, "{}", u8::from(adopt)).unwrap();
        for v in values {
            write!(input, " {v}").unwrap();
        }
        input.push('\n');
        for (off, value) in m.policy {
            expected.extend_from_slice(&off.to_le_bytes());
            expected.extend_from_slice(&value.to_le_bytes());
        }
        expected.extend_from_slice(&u32::MAX.to_le_bytes());
    }
    let mut child = Command::new(std::env::var("PYTHON").unwrap_or_else(|_| "python3".into()))
        .args([concat!(
            env!("CARGO_MANIFEST_DIR"),
            "/tests/c_gen2_reference.py"
        )])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    child
        .stdin
        .take()
        .unwrap()
        .write_all(input.as_bytes())
        .unwrap();
    let output = child.wait_with_output().unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(output.stdout, expected);
}

struct ReadOnly<'a>(&'a mut Model);
impl ReadIo for ReadOnly<'_> {
    fn gpu_bdf(&self) -> Bdf {
        self.0.gpu_bdf()
    }
    fn config_read(&mut self, d: Device, r: u8) -> u32 {
        self.0.config_read(d, r)
    }
    fn mmio_read(&mut self, r: u32) -> u32 {
        self.0.mmio_read(r)
    }
}

#[test]
fn final_observer_records_link_and_policy_without_write_or_delay_access() {
    for action in [Action::Prepare, Action::Targets, Action::Retrain] {
        let mut model = Model::new(0);
        let report = gen2::run(&mut model, action);
        let config = model.config.0.clone();
        let mmio = model.mmio.clone();
        let writes = model.writes.clone();
        let policy = model.policy.clone();
        let delays = model.delays.clone();
        let observed = gen2::observe(&mut ReadOnly(&mut model), report.port.unwrap(), 16);
        assert_eq!(
            observed.result,
            Ok(if action == Action::Retrain {
                LinkState::Gen2
            } else {
                LinkState::Gen1
            })
        );
        assert!(observed.policy_matches);
        assert_eq!(observed.links, report.after);
        assert_eq!(model.config.0, config);
        assert_eq!(model.mmio, mmio);
        assert_eq!(model.writes, writes);
        assert_eq!(model.policy, policy);
        assert_eq!(model.delays, delays);
    }
}

#[test]
fn final_observer_distinguishes_policy_changes_from_unhealthy_native_state() {
    for fault in 0..5 {
        let mut model = Model::new(0);
        let report = gen2::run(&mut model, Action::Retrain);
        match fault {
            0 => {
                model.mmio.insert(gen2::MISC, 0xe0b40d00);
                model.mmio.insert(gen2::CFG, 0x800c5800);
                model.mmio.insert(gen2::PL, 0x00220036);
                model.config.insert((GPU, GC + 0x10), 0x11010140);
                model.config.insert((PORT.bdf, PC + 0x10), 0x31010040);
            }
            1 => {
                model.mmio.insert(reg::SS1, 0);
            }
            2 => {
                model.config.insert((GPU, GC + 0x10), 0x10820140);
            }
            3 => {
                model.mmio.insert(gen2::CFG, u32::MAX);
            }
            4 => {
                model.config.insert((PORT.bdf, 0x18), 0x00030300);
            }
            _ => unreachable!(),
        }
        let mmio = model.mmio.clone();
        let config = model.config.0.clone();
        let observed = gen2::observe(&mut ReadOnly(&mut model), report.port.unwrap(), 16);
        assert_eq!(
            observed.result,
            match fault {
                0 => Ok(LinkState::Gen1),
                1 => Err(Error::NativeState),
                2 => Err(Error::LinkChanged),
                3 => Err(Error::Unreadable),
                4 => Err(Error::Topology),
                _ => unreachable!(),
            }
        );
        if fault == 0 {
            assert!(!observed.policy_matches);
        }
        assert_eq!(model.config.0, config);
        assert_eq!(model.mmio, mmio);
        assert_eq!(model.requests, 1);
    }
}
