// SPDX-License-Identifier: MIT
//! Optional FULL-only Gen2 adapter. Firmware sequence and ReBAR are independent.
use crate::{
    hardware::{Mmio, Pci, RebarRoots},
    output::{Output, Trace},
};
use cmp40_efi_core::{
    gen2::{self, Action, Device, Error, Io, Port, ReadIo},
    pci::{Bdf, Config},
};
use uefi::{boot, Status};

struct Backend<'a> {
    pci: &'a mut Pci,
    mmio: &'a mut Mmio,
    roots: RebarRoots,
    trace: Trace,
}
impl ReadIo for Backend<'_> {
    fn gpu_bdf(&self) -> Bdf {
        self.pci.bdf
    }
    fn config_read(&mut self, device: Device, offset: u8) -> u32 {
        let value = match device {
            Device::Gpu => self.pci.read32(self.pci.bdf, offset),
            Device::Bus(bdf) => self.pci.read32(bdf, offset),
            Device::Port(bridge) => self.roots.read(bridge, offset),
        };
        if value != u32::MAX {
            self.trace.record(b'P', key(device, offset), value);
        }
        value
    }
    fn mmio_read(&mut self, offset: u32) -> u32 {
        let value = self.mmio.read32(offset);
        self.trace.record(b'R', offset, value);
        value
    }
}
impl Io for Backend<'_> {
    fn root_count(&self) -> usize {
        self.roots.count()
    }
    fn config_write16(&mut self, device: Device, offset: u8, value: u16) -> bool {
        let ok = match device {
            Device::Gpu => self.pci.write_control16(self.pci.bdf, offset, value),
            Device::Port(bridge) => self.roots.write_control16(bridge, offset, value),
            Device::Bus(_) => false,
        };
        self.trace
            .record(b'H', key(device, offset), u32::from(value));
        self.trace.record(b'E', key(device, offset), u32::from(ok));
        ok
    }
    fn mmio_write(&mut self, offset: u32, value: u32) {
        self.mmio.write32(offset, value);
        self.trace.record(b'W', offset, value);
    }
    fn delay_us(&mut self, us: u32) {
        boot::stall(core::time::Duration::from_micros(u64::from(us)));
        self.trace.record(b'S', 0, us);
    }
}
pub struct Watch {
    roots: RebarRoots,
    port: Port,
    width: u8,
}
struct Observer<'a> {
    pci: &'a mut Pci,
    mmio: &'a mut Mmio,
    roots: RebarRoots,
}
impl ReadIo for Observer<'_> {
    fn gpu_bdf(&self) -> Bdf {
        self.pci.bdf
    }
    fn config_read(&mut self, device: Device, offset: u8) -> u32 {
        match device {
            Device::Gpu => self.pci.read32(self.pci.bdf, offset),
            Device::Bus(bdf) => self.pci.read32(bdf, offset),
            Device::Port(bridge) => self.roots.read(bridge, offset),
        }
    }
    fn mmio_read(&mut self, offset: u32) -> u32 {
        self.mmio.read32(offset)
    }
}
pub fn final_check(
    pci: &mut Pci,
    mmio: &mut Mmio,
    output: &mut Output,
    watch: Watch,
) -> Result<(), Status> {
    output.checkpoint(format_args!(
        "CMP40-GEN2: FINAL_BEGIN phase=after-rebar-before-handoff OBSERVER_WRITES=0\n"
    ))?;
    let mut io = Observer {
        pci,
        mmio,
        roots: watch.roots,
    };
    let observation = gen2::observe(&mut io, watch.port, watch.width);
    for (side, link) in [
        ("GPU", observation.links[0]),
        ("PORT", observation.links[1]),
    ] {
        output.write(format_args!("GEN2_FINAL_LINK {} FLAGS={:04x} CAP={:08x} CAP2={:08x} CTL={:04x} CTL2={:04x} STA={:04x} speed={} width={}\n",
            side, link.flags, link.cap, link.cap2, link.control, link.control2, link.status, link.speed(), link.width()))?;
    }
    output.write(format_args!(
        "GEN2_FINAL_POLICY VALUES={:08x?} order=MISC,CYA,CFG,PL,OVR matches={}\n",
        observation.policy, observation.policy_matches
    ))?;
    output.checkpoint(format_args!(
        "CMP40-GEN2: FINAL_RESULT {:?} OBSERVER_WRITES=0 OS_RETENTION=unmeasured\n",
        observation.result
    ))?;
    observation
        .result
        .map(|_| ())
        .map_err(|_| Status::DEVICE_ERROR)
}
fn key(device: Device, offset: u8) -> u32 {
    match device {
        Device::Gpu => 0xf0000000 | u32::from(offset),
        Device::Bus(bdf) => {
            0xe0000000
                | (u32::from(bdf.bus) << 20)
                | (u32::from(bdf.device) << 15)
                | (u32::from(bdf.function) << 12)
                | u32::from(offset)
        }
        Device::Port(bridge) => {
            ((bridge.root as u32) << 28)
                | (u32::from(bridge.bdf.bus) << 20)
                | (u32::from(bridge.bdf.device) << 15)
                | (u32::from(bridge.bdf.function) << 12)
                | u32::from(offset)
        }
    }
}
pub fn execute(
    pci: &mut Pci,
    mmio: &mut Mmio,
    output: &mut Output,
    action: Action,
) -> Result<Option<Watch>, Status> {
    if action == Action::Off {
        return Ok(None);
    }
    let roots = match RebarRoots::collect(pci) {
        Ok(roots) => roots,
        Err(status) => {
            output.checkpoint(format_args!(
                "CMP40-GEN2: SKIPPED roots={:?} GEN2_WRITES=0\n",
                status
            ))?;
            return Ok(None);
        }
    };
    let trace = Trace::new()?;
    output.checkpoint(format_args!(
        "CMP40-GEN2: BEGIN mode={:?}; trace buffered; no resets\n",
        action
    ))?;
    let mut backend = Backend {
        pci,
        mmio,
        roots,
        trace,
    };
    let report = gen2::run(&mut backend, action);
    backend.trace.dump(output)?;
    if let Some(port) = report.port {
        output.write(format_args!("GEN2_PORT {:?}\n", port))?;
    }
    for (side, before, after) in [
        ("GPU", report.before[0], report.after[0]),
        ("PORT", report.before[1], report.after[1]),
    ] {
        output.write(format_args!("GEN2_LINK {} BEFORE FLAGS={:04x} CAP={:08x} CAP2={:08x} CTL={:04x} CTL2={:04x} STA={:04x}\n",
            side,before.flags,before.cap,before.cap2,before.control,before.control2,before.status))?;
        output.write(format_args!("GEN2_LINK {} AFTER FLAGS={:04x} CAP={:08x} CAP2={:08x} CTL={:04x} CTL2={:04x} STA={:04x} speed={} width={}\n",
            side,after.flags,after.cap,after.cap2,after.control,after.control2,after.status,after.speed(),after.width()))?;
    }
    output.write(format_args!(
        "GEN2_POLICY BEFORE={:08x?} AFTER={:08x?} order=MISC,CYA,CFG,PL,OVR\n",
        report.policy_before, report.policy_after
    ))?;
    output.checkpoint(format_args!("CMP40-GEN2: RESULT {:?} mutated={} retrains={} waited_us={} TLS_RESTORED={} saved_tls={:04x?}\n",
        report.result,report.mutated,report.requested_retrains,report.waited_us,report.targets_restored,report.saved_tls))?;
    match report.result {
        Ok(_) => Ok(report.port.map(|port| Watch {
            roots: backend.roots,
            port,
            width: report.before[0].width(),
        })),
        // A read-only topology/capability refusal leaves completed CPU-RM intact.
        Err(Error::NoPort | Error::SharedBus | Error::Topology | Error::Capability)
            if !report.mutated =>
        {
            Ok(None)
        }
        // Partial policy, unreadable state or failed TLS restoration may not boot.
        Err(_) => Err(Status::DEVICE_ERROR),
    }
}
