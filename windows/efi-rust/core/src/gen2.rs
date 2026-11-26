// SPDX-License-Identifier: MIT
//! Experimental pre-driver Gen2 policy and ordinary immediate-port retrain.
//! Register masks follow the project's documented EFI and Linux pre-driver policy.
use crate::{
    pci::{Bdf, CMP40_ID},
    rebar::Bridge,
    state::{Control, Snapshot},
    transaction::reg,
};

pub const MISC: u32 = 0x8841c;
pub const CYA: u32 = 0x8c2c0;
pub const CFG: u32 = 0x8c040;
pub const PL: u32 = 0x8c1c0;
pub const OVR: u32 = 0x8872c;
pub const POLICY: [(u32, u32, u32); 5] = [
    (MISC, 0x7800, 0x2800),
    (CYA, 4, 0),
    (CFG, 0xc0000, 0x80000),
    (PL, 0x60000, 0x40000),
    (OVR, u32::MAX, 6),
];

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Action {
    Off,
    Prepare,
    Targets,
    Retrain,
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Device {
    Gpu,
    Port(Bridge),
    Bus(Bdf),
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Error {
    Unreadable,
    Capability,
    Topology,
    SharedBus,
    NoPort,
    NativeState,
    PolicyReadback,
    TargetReadback,
    RequestRejected,
    LinkChanged,
    RestoreFailed,
}

pub trait ReadIo {
    fn gpu_bdf(&self) -> Bdf;
    fn config_read(&mut self, device: Device, offset: u8) -> u32;
    fn mmio_read(&mut self, offset: u32) -> u32;
}
pub trait Io: ReadIo {
    fn root_count(&self) -> usize;
    /// A true control WORD store: never write the adjacent W1C status WORD.
    fn config_write16(&mut self, device: Device, offset: u8, value: u16) -> bool;
    fn mmio_write(&mut self, offset: u32, value: u32);
    fn delay_us(&mut self, microseconds: u32);
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum LinkState {
    Gen1,
    Gen2,
    Faster,
}
#[derive(Clone, Copy, Debug)]
pub struct Observation {
    pub links: [Link; 2],
    pub policy: [u32; 5],
    pub policy_matches: bool,
    pub result: Result<LinkState, Error>,
}
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct Link {
    pub flags: u16,
    pub cap: u32,
    pub cap2: u32,
    pub control: u16,
    pub control2: u16,
    pub status: u16,
}
impl Link {
    pub fn speed(self) -> u8 {
        (self.status & 15) as u8
    }
    pub fn width(self) -> u8 {
        ((self.status >> 4) & 0x3f) as u8
    }
    pub fn capable(self) -> bool {
        self.flags & 15 >= 2
            && self.cap & 15 >= 2
            && (self.cap2 & 4 != 0 || (self.cap2 & 0xfe == 0 && self.cap & 15 == 2))
    }
    fn up(self) -> bool {
        (1..=6).contains(&self.speed())
            && (1..=32).contains(&self.width())
            && self.status & 0x800 == 0
            && self.control & 0x10 == 0
            && u32::from(self.width()) <= (self.cap >> 4) & 0x3f
    }
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Port {
    pub bridge: Bridge,
    pub identity: u32,
    pub gpu_cap: u8,
    pub port_cap: u8,
    pub port_type: u8,
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Outcome {
    Off,
    Prepared,
    TargetsSeeded,
    Gen2,
    AlreadyFast,
    Gen1Timeout,
}
#[derive(Clone, Copy, Debug)]
pub struct Report {
    pub result: Result<Outcome, Error>,
    pub port: Option<Port>,
    pub before: [Link; 2],
    pub after: [Link; 2],
    pub policy_before: [u32; 5],
    pub policy_after: [u32; 5],
    pub mutated: bool,
    pub requested_retrains: u8,
    pub waited_us: u32,
    pub targets_restored: bool,
    pub saved_tls: [u16; 2],
}

fn read(io: &mut impl ReadIo, device: Device, offset: u8) -> Result<u32, Error> {
    let value = io.config_read(device, offset);
    if value == u32::MAX {
        Err(Error::Unreadable)
    } else {
        Ok(value)
    }
}
fn capability(io: &mut impl ReadIo, device: Device) -> Result<(u8, u8), Error> {
    let mut offset = (read(io, device, 0x34)? & 0xff) as u8;
    let mut seen = 0u64;
    for _ in 0..48 {
        if offset == 0 {
            break;
        }
        if !(0x40..=0xfc).contains(&offset) || offset & 3 != 0 {
            return Err(Error::Capability);
        }
        let bit = 1u64 << (offset / 4);
        if seen & bit != 0 {
            return Err(Error::Capability);
        }
        seen |= bit;
        let header = read(io, device, offset)?;
        if header & 255 == 0x10 {
            if offset > 0xcc || (header >> 16) & 15 == 0 {
                return Err(Error::Capability);
            }
            return Ok((offset, ((header >> 20) & 15) as u8));
        }
        offset = ((header >> 8) & 255) as u8;
    }
    Err(Error::Capability)
}
fn leaf_bus(io: &mut impl ReadIo) -> Result<(), Error> {
    let gpu = io.gpu_bdf();
    if read(io, Device::Gpu, 0)? != CMP40_ID {
        return Err(Error::Topology);
    }
    // Use the selected GPU route for its bus; no config writes during discovery.
    for device in 0..32 {
        for function in 0..8 {
            let bdf = Bdf {
                bus: gpu.bus,
                device,
                function,
            };
            let route = Device::Bus(bdf);
            let id = io.config_read(route, 0);
            if id == 0 || id == u32::MAX {
                continue;
            }
            if device != gpu.device || id & 0xffff != 0x10de {
                return Err(Error::SharedBus);
            }
            let header = read(io, route, 0x0c)?;
            let class = read(io, route, 8)?;
            if (header >> 16) & 0x7f != 0
                || (function != gpu.function && [1, 6].contains(&(class >> 24)))
            {
                return Err(Error::SharedBus);
            }
        }
    }
    Ok(())
}
pub fn discover(io: &mut impl Io) -> Result<Port, Error> {
    let gpu = io.gpu_bdf();
    if gpu.bus == 0 {
        return Err(Error::NoPort);
    }
    leaf_bus(io)?;
    let (gpu_cap, gpu_type) = capability(io, Device::Gpu)?;
    if gpu_type != 0 {
        return Err(Error::Capability);
    }
    let mut found: Option<Port> = None;
    for root in 0..io.root_count() {
        for bus in 0..gpu.bus {
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
                    let route = Device::Port(bridge);
                    let id = io.config_read(route, 0);
                    if id == 0 || id == u32::MAX {
                        if function == 0 {
                            break;
                        }
                        continue;
                    }
                    let header = read(io, route, 0x0c)?;
                    if function == 0 && header & 0x00800000 == 0 {
                        multi = false;
                    }
                    if (header >> 16) & 0x7f != 1 {
                        continue;
                    }
                    let buses = read(io, route, 0x18)?;
                    if (buses >> 8) & 255 != u32::from(gpu.bus) {
                        continue;
                    }
                    if buses & 255 != u32::from(bus) || (buses >> 16) & 255 != u32::from(gpu.bus) {
                        return Err(Error::SharedBus);
                    }
                    if (read(io, route, 8)? >> 16) & 0xffff != 0x0604 {
                        return Err(Error::Topology);
                    }
                    let (port_cap, port_type) = capability(io, route)?;
                    if port_type != 4 && port_type != 6 {
                        return Err(Error::Capability);
                    }
                    let port = Port {
                        bridge,
                        identity: id,
                        gpu_cap,
                        port_cap,
                        port_type,
                    };
                    if let Some(old) = found {
                        // Overlapping RootBridgeIo protocols can expose the same port.
                        if old.bridge.bdf != bridge.bdf
                            || old.identity != id
                            || old.port_cap != port_cap
                            || old.port_type != port_type
                        {
                            return Err(Error::Topology);
                        }
                    } else {
                        found = Some(port);
                    }
                }
            }
        }
    }
    found.ok_or(Error::NoPort)
}
fn validate(io: &mut impl ReadIo, port: Port) -> Result<(), Error> {
    let gpu = io.gpu_bdf();
    if read(io, Device::Gpu, 0)? != CMP40_ID
        || read(io, Device::Port(port.bridge), 0)? != port.identity
    {
        return Err(Error::Topology);
    }
    let buses = read(io, Device::Port(port.bridge), 0x18)?;
    if buses & 0xffffff
        != u32::from(port.bridge.bdf.bus) | (u32::from(gpu.bus) << 8) | (u32::from(gpu.bus) << 16)
    {
        return Err(Error::Topology);
    }
    if capability(io, Device::Gpu)? != (port.gpu_cap, 0)
        || capability(io, Device::Port(port.bridge))? != (port.port_cap, port.port_type)
        || (read(io, Device::Port(port.bridge), 0x0c)? >> 16) & 0x7f != 1
        || (read(io, Device::Port(port.bridge), 8)? >> 16) & 0xffff != 0x0604
    {
        return Err(Error::Topology);
    }
    Ok(())
}
fn native(io: &mut impl ReadIo) -> Result<(), Error> {
    if io.mmio_read(0) & 0xffff0000 != 0x16600000 {
        return Err(Error::NativeState);
    }
    let snapshot = Snapshot {
        ss0: io.mmio_read(reg::SS0),
        ss1: io.mmio_read(reg::SS1),
        wpr2_hi: io.mmio_read(reg::WPR_HI),
        wpr2_lo: io.mmio_read(reg::WPR_LO),
        sec2_cpu: io.mmio_read(reg::SEC2 + reg::CPU),
        mailbox0: io.mmio_read(reg::SEC2 + reg::MBOX0),
        feat_plm: io.mmio_read(reg::FEAT_PLM),
    };
    snapshot
        .check_booter_completion(Control::Full)
        .map_err(|_| Error::NativeState)?;
    let gsp = io.mmio_read(reg::GSP + reg::CPU);
    if gsp == u32::MAX || gsp & 0xffff0000 == 0xbadf0000 || gsp & 0x10 == 0 {
        return Err(Error::NativeState);
    }
    Ok(())
}
fn link(io: &mut impl ReadIo, device: Device, cap: u8, typ: u8) -> Result<Link, Error> {
    let header = read(io, device, cap)?;
    if header & 255 != 0x10 || (header >> 16) & 15 == 0 || (header >> 20) & 15 != u32::from(typ) {
        return Err(Error::Capability);
    }
    let flags = (header >> 16) as u16;
    let capability = read(io, device, cap + 12)?;
    if !(1..=6).contains(&(capability & 15)) || !(1..=32).contains(&((capability >> 4) & 0x3f)) {
        return Err(Error::Capability);
    }
    let control_status = read(io, device, cap + 0x10)?;
    let (cap2, control2) = if flags & 15 >= 2 {
        (
            read(io, device, cap + 0x2c)?,
            read(io, device, cap + 0x30)? as u16,
        )
    } else {
        (0, 0)
    };
    Ok(Link {
        flags,
        cap: capability,
        cap2,
        control: control_status as u16,
        control2,
        status: (control_status >> 16) as u16,
    })
}
fn pair(io: &mut impl ReadIo, port: Port) -> Result<[Link; 2], Error> {
    Ok([
        link(io, Device::Gpu, port.gpu_cap, 0)?,
        link(io, Device::Port(port.bridge), port.port_cap, port.port_type)?,
    ])
}
fn healthy(links: [Link; 2], width: u8) -> bool {
    links.iter().all(|l| l.up() && l.width() == width) && links[0].speed() == links[1].speed()
}
/// Read the completed experiment again after later EFI work. This interface
/// cannot write policy, targets or retrain, and cannot stall or retry.
pub fn observe(io: &mut impl ReadIo, port: Port, width: u8) -> Observation {
    let mut observation = Observation {
        links: [Link::default(); 2],
        policy: [0; 5],
        policy_matches: false,
        result: Err(Error::Unreadable),
    };
    observation.result = (|| {
        validate(io, port)?;
        leaf_bus(io)?;
        observation.links = pair(io, port)?;
        for (i, &(offset, _, _)) in POLICY.iter().enumerate() {
            let value = io.mmio_read(offset);
            observation.policy[i] = value;
            if value == u32::MAX || value & 0xffff0000 == 0xbadf0000 {
                return Err(Error::Unreadable);
            }
        }
        observation.policy_matches = POLICY
            .iter()
            .zip(observation.policy)
            .all(|(&(_, mask, bits), value)| (value ^ bits) & mask == 0);
        native(io)?;
        if !healthy(observation.links, width) {
            return Err(Error::LinkChanged);
        }
        Ok(match observation.links[0].speed() {
            1 => LinkState::Gen1,
            2 => LinkState::Gen2,
            _ => LinkState::Faster,
        })
    })();
    observation
}
fn target(io: &mut impl Io, device: Device, cap: u8, tls: u16) -> Result<(), Error> {
    let header = read(io, device, cap)?;
    let typ = ((header >> 20) & 15) as u8;
    let wrong_type = match device {
        Device::Gpu => typ != 0,
        Device::Port(_) => typ != 4 && typ != 6,
        Device::Bus(_) => return Err(Error::Topology),
    };
    if header & 255 != 0x10 || (header >> 16) & 15 < 2 || wrong_type {
        return Err(Error::Capability);
    }
    let old = read(io, device, cap + 0x30)? as u16;
    if old & 0x410 != 0 || !(1..=6).contains(&(old & 15)) {
        return Err(Error::Capability);
    }
    if old & 15 == tls & 15 {
        return Ok(());
    }
    if !io.config_write16(device, cap + 0x30, (old & !15) | (tls & 15)) {
        return Err(Error::TargetReadback);
    }
    if read(io, device, cap + 0x30)? & 15 != u32::from(tls & 15) {
        return Err(Error::TargetReadback);
    }
    Ok(())
}

pub fn run(io: &mut impl Io, action: Action) -> Report {
    let mut report = Report {
        result: Ok(Outcome::Off),
        port: None,
        before: [Link::default(); 2],
        after: [Link::default(); 2],
        policy_before: [0; 5],
        policy_after: [0; 5],
        mutated: false,
        requested_retrains: 0,
        waited_us: 0,
        targets_restored: false,
        saved_tls: [0; 2],
    };
    if action == Action::Off {
        return report;
    }
    let mut targets_attempted = false;
    report.result = execute(io, action, &mut report, &mut targets_attempted);
    if report.result.is_err() && targets_attempted {
        if let Some(port) = report.port {
            // Revalidate identity and owned target fields; do not overwrite an
            // externally changed TLS or perform another retrain/reset.
            let restore = (|| {
                validate(io, port)?;
                for (device, cap, old) in [
                    (Device::Gpu, port.gpu_cap, report.saved_tls[0]),
                    (
                        Device::Port(port.bridge),
                        port.port_cap,
                        report.saved_tls[1],
                    ),
                ] {
                    let now = read(io, device, cap + 0x30)? as u16;
                    if now & 15 != old & 15 {
                        if now & 15 != 2 {
                            return Err(Error::RestoreFailed);
                        }
                        target(io, device, cap, old)?;
                    }
                }
                Ok::<_, Error>(())
            })();
            report.targets_restored = restore.is_ok();
            if restore.is_err() {
                report.result = Err(Error::RestoreFailed);
            }
        }
    }
    report
}
fn execute(
    io: &mut impl Io,
    action: Action,
    report: &mut Report,
    targets_attempted: &mut bool,
) -> Result<Outcome, Error> {
    let port = discover(io)?;
    report.port = Some(port);
    native(io)?;
    report.before = pair(io, port)?;
    let width = report.before[0].width();
    if !healthy(report.before, width) || !report.before[1].capable() {
        return Err(Error::Capability);
    }
    for (i, &(offset, _, _)) in POLICY.iter().enumerate() {
        let value = io.mmio_read(offset);
        report.policy_before[i] = value;
        if value == u32::MAX || value & 0xffff0000 == 0xbadf0000 {
            return Err(Error::Unreadable);
        }
    }
    validate(io, port)?;
    leaf_bus(io)?;
    let adopt = !report.before[0].capable();
    for (i, &(offset, mask, bits)) in POLICY.iter().enumerate() {
        if offset == OVR && !adopt {
            report.policy_after[i] = report.policy_before[i];
            continue;
        }
        let old = io.mmio_read(offset);
        if old == u32::MAX || old & 0xffff0000 == 0xbadf0000 {
            return Err(Error::Unreadable);
        }
        report.policy_before[i] = old;
        let wanted = (old & !mask) | (bits & mask);
        report.mutated = true;
        io.mmio_write(offset, wanted);
        let after = io.mmio_read(offset);
        report.policy_after[i] = after;
        if after == u32::MAX || after & 0xffff0000 == 0xbadf0000 || (after ^ wanted) & mask != 0 {
            return Err(Error::PolicyReadback);
        }
    }
    io.delay_us(50000);
    report.after = pair(io, port)?;
    native(io)?;
    validate(io, port)?;
    if !healthy(report.after, width) || !report.after.iter().all(|l| l.capable()) {
        return Err(Error::Capability);
    }
    if action == Action::Prepare {
        return Ok(Outcome::Prepared);
    }
    if report.after[0].speed() > 2 {
        return Ok(Outcome::AlreadyFast);
    }
    for current in report.after {
        if current.control2 & 0x410 != 0 || !(1..=6).contains(&(current.control2 & 15)) {
            return Err(Error::Capability);
        }
    }
    // Capture TLS after adoption, which may have exposed the live v2 fields.
    report.saved_tls = [report.after[0].control2, report.after[1].control2];
    validate(io, port)?;
    leaf_bus(io)?;
    *targets_attempted = true;
    target(io, Device::Gpu, port.gpu_cap, 2)?;
    target(io, Device::Port(port.bridge), port.port_cap, 2)?;
    report.after = pair(io, port)?;
    native(io)?;
    validate(io, port)?;
    if !healthy(report.after, width)
        || !report
            .after
            .iter()
            .all(|l| l.capable() && l.control2 & 15 == 2)
    {
        return Err(Error::LinkChanged);
    }
    if action == Action::Targets {
        return Ok(Outcome::TargetsSeeded);
    }
    if report.after[0].speed() == 2 && healthy(report.after, width) {
        native(io)?;
        return Ok(Outcome::Gen2);
    }
    validate(io, port)?;
    let control = read(io, Device::Port(port.bridge), port.port_cap + 0x10)? as u16;
    if control & 0x10 != 0 {
        return Err(Error::LinkChanged);
    }
    report.requested_retrains = 1;
    if !io.config_write16(
        Device::Port(port.bridge),
        port.port_cap + 0x10,
        control | 0x20,
    ) {
        return Err(Error::RequestRejected);
    }
    for _ in 0..200 {
        io.delay_us(10000);
        report.waited_us += 10000;
        report.after = pair(io, port)?;
        if healthy(report.after, width) && report.after[0].speed() == 2 {
            native(io)?;
            validate(io, port)?;
            if !report
                .after
                .iter()
                .all(|l| l.capable() && l.control2 & 0x41f == 2)
            {
                return Err(Error::LinkChanged);
            }
            return Ok(Outcome::Gen2);
        }
    }
    native(io)?;
    validate(io, port)?;
    if healthy(report.after, width)
        && report.after[0].speed() == 1
        && report
            .after
            .iter()
            .all(|l| l.capable() && l.control2 & 15 == 2)
    {
        // A healthy pre-driver timeout keeps the TLS seed for OS initialization.
        Ok(Outcome::Gen1Timeout)
    } else {
        Err(Error::LinkChanged)
    }
}
