// SPDX-License-Identifier: MIT
//! Best-effort ReBAR after native completion; filesystem calls stay outside IO.
use crate::{
    hardware::{Mmio, Pci, RebarRoots},
    output::{Output, Trace},
};
use alloc::vec::Vec;
use cmp40_efi_core::{
    pci::Config,
    rebar::{self, Bridge, Io, Range},
};
use uefi::Status;

/// Fallible GetMemoryMap boundary matching C's two calls and four-descriptor
/// allocation headroom. The high-level uefi-rs helper can panic on map growth.
fn inspect_memory_map(mut visit: impl FnMut(u64, u64) -> Result<(), Status>) -> Result<(), Status> {
    let table = uefi::table::system_table_raw().ok_or(Status::NOT_READY)?;
    // SAFETY: Entry initialized this table and Boot Services remain live.
    let services = unsafe { (*table.as_ptr()).boot_services.as_ref() }.ok_or(Status::NOT_READY)?;
    let (mut size, mut key, mut stride, mut version) = (0, 0, 0, 0);
    // SAFETY: The sizing call passes no buffer and valid out-parameters.
    let status = unsafe {
        (services.get_memory_map)(
            &mut size,
            core::ptr::null_mut(),
            &mut key,
            &mut stride,
            &mut version,
        )
    };
    if status != Status::BUFFER_TOO_SMALL || stride < 40 || stride % 8 != 0 {
        return Err(Status::DEVICE_ERROR);
    }
    let capacity = stride
        .checked_mul(4)
        .and_then(|n| size.checked_add(n))
        .ok_or(Status::OUT_OF_RESOURCES)?;
    let words = capacity.div_ceil(8);
    let mut storage = Vec::<u64>::new();
    storage
        .try_reserve_exact(words)
        .map_err(|_| Status::OUT_OF_RESOURCES)?;
    storage.resize(words, 0);
    size = capacity;
    // SAFETY: u64 storage supplies the required eight-byte alignment and at
    // least size initialized bytes; firmware receives live output pointers.
    let status = unsafe {
        (services.get_memory_map)(
            &mut size,
            storage.as_mut_ptr().cast(),
            &mut key,
            &mut stride,
            &mut version,
        )
    };
    if status.is_error() {
        return Err(status);
    }
    if size > capacity || stride < 40 || stride % 8 != 0 || size % stride != 0 {
        return Err(Status::BAD_BUFFER_SIZE);
    }
    // SAFETY: The firmware reported size within the initialized u64 buffer.
    // Byte interpretation avoids forming references to extended descriptors.
    let bytes = unsafe { core::slice::from_raw_parts(storage.as_ptr().cast::<u8>(), size) };
    for entry in bytes.chunks_exact(stride) {
        let start = u64::from_le_bytes(
            entry[8..16]
                .try_into()
                .map_err(|_| Status::BAD_BUFFER_SIZE)?,
        );
        let pages = u64::from_le_bytes(
            entry[24..32]
                .try_into()
                .map_err(|_| Status::BAD_BUFFER_SIZE)?,
        );
        visit(start, pages)?;
    }
    Ok(())
}

/// Read-only diagnostic for the QEMU fixture; no PCI or MMIO access.
pub fn check_memory_map() -> Result<(), Status> {
    inspect_memory_map(|start, pages| {
        pages
            .checked_mul(4096)
            .and_then(|n| start.checked_add(n))
            .ok_or(Status::DEVICE_ERROR)
            .map(|_| ())
    })
}

struct Backend<'a> {
    pci: &'a mut Pci,
    mmio: &'a mut Mmio,
    roots: RebarRoots,
    trace: Trace,
}
impl Io for Backend<'_> {
    fn gpu_read(&mut self, offset: u8) -> u32 {
        let value = self.pci.read32(self.pci.bdf, offset);
        self.trace.record(b'P', u32::from(offset), value);
        value
    }
    fn gpu_write(&mut self, offset: u8, value: u32) {
        self.pci.write32(self.pci.bdf, offset, value);
        self.trace.record(b'Q', u32::from(offset), value);
    }
    fn root_count(&self) -> usize {
        self.roots.count()
    }
    fn bridge_read(&mut self, bridge: Bridge, offset: u8) -> u32 {
        let value = self.roots.read(bridge, offset);
        // Empty scan slots carry no transaction state; omit them on hardware.
        if value != u32::MAX {
            self.trace.record(b'B', key(bridge, offset), value);
        }
        value
    }
    fn bridge_write(&mut self, bridge: Bridge, offset: u8, value: u32) {
        self.roots.write(bridge, offset, value);
        self.trace.record(b'J', key(bridge, offset), value);
    }
    fn mmio_read(&mut self, offset: u32) -> u32 {
        let value = self.mmio.read32(offset);
        self.trace.record(b'R', offset, value);
        value
    }
    fn mmio_write(&mut self, offset: u32, value: u32) {
        self.mmio.write32(offset, value);
        self.trace.record(b'W', offset, value);
    }
    fn memory_top(&mut self) -> u64 {
        let mut top = 0;
        if inspect_memory_map(|start, pages| {
            let end = pages
                .checked_mul(4096)
                .and_then(|n| start.checked_add(n))
                .ok_or(Status::DEVICE_ERROR)?;
            top = top.max(end);
            Ok(())
        })
        .is_err()
        {
            top = 0;
        }
        self.trace.record(b'T', (top >> 32) as u32, top as u32);
        top
    }
    fn memory_conflict(&mut self, base: u64, bytes: u64) -> bool {
        let mut conflict = false;
        if inspect_memory_map(|start, pages| {
            let end = pages
                .checked_mul(4096)
                .and_then(|n| n.checked_sub(1))
                .and_then(|n| start.checked_add(n));
            // Preserve every memory type; malformed ranges refuse placement.
            conflict |= end.is_none_or(|max| Range { min: start, max }.overlaps(base, bytes));
            Ok(())
        })
        .is_err()
        {
            conflict = true;
        }
        self.trace.record(b'M', (base >> 32) as u32, base as u32);
        self.trace.record(b'I', (bytes >> 32) as u32, bytes as u32);
        self.trace.record(b'O', 0, u32::from(conflict));
        conflict
    }
    fn vram_read(&mut self, address: u64) -> u32 {
        // SAFETY: Called only after the core enables GPU/bridge decode for a
        // checked, aligned 8 GiB BAR1 window. Like C this assumes identity
        // mapping and working root routing; no RAM reference is constructed.
        let value = unsafe { core::ptr::read_volatile(address as *const u32) };
        self.trace
            .record(b'V', (address >> 32) as u32, address as u32);
        self.trace.record(b'D', 0, value);
        value
    }
    fn apertures(&mut self, _: usize) {
        for root in 0..self.roots.count() {
            if let Some(range) = self.roots.aperture(root) {
                self.trace
                    .record(b'A', (range.min >> 32) as u32, range.min as u32);
                self.trace
                    .record(b'E', (range.max >> 32) as u32, range.max as u32);
            }
        }
    }
}
fn key(bridge: Bridge, offset: u8) -> u32 {
    ((bridge.root as u32) << 28)
        | (u32::from(bridge.bdf.bus) << 20)
        | (u32::from(bridge.bdf.device) << 15)
        | (u32::from(bridge.bdf.function) << 12)
        | u32::from(offset)
}

pub fn execute(pci: &mut Pci, mmio: &mut Mmio, output: &mut Output) -> Result<(), Status> {
    // Allocation/discovery failure here skips ReBAR without touching XVE/BARs.
    let prepared = RebarRoots::collect(pci).and_then(|roots| Ok((roots, Trace::new()?)));
    let (roots, trace) = match prepared {
        Ok(prepared) => prepared,
        Err(status) => {
            return output.checkpoint(format_args!(
                "CMP40-REBAR: SKIPPED preparation={:?} REBAR_WRITES=0\n",
                status
            ))
        }
    };
    output.checkpoint(format_args!(
        "CMP40-REBAR: BEGIN requested=true bytes={}\n",
        rebar::BAR1_BYTES
    ))?;
    let bus = pci.bdf.bus;
    let mut backend = Backend {
        pci,
        mmio,
        roots,
        trace,
    };
    let report = rebar::relocate(&mut backend, bus);
    backend.trace.dump(output)?;
    if let Some(saved) = report.saved {
        output.write(format_args!("REBAR_SAVED XVE={:08x?} CMD={:08x} BARS={:08x?} BRIDGE={:?} BR_CMD={:08x} WINDOW={:08x?}\n",
            saved.xve, saved.command, saved.bars, saved.bridge, saved.bridge_command, saved.window))?;
    }
    output.write(format_args!(
        "REBAR_PROBE BAR1={:08x?} BAR3_BYTES={:016x} READBACK={:08x?} VRAM={:08x?}\n",
        report.bar1_probe, report.bar3_bytes, report.readback, report.vram
    ))?;
    output.checkpoint(format_args!(
        "CMP40-REBAR: RESULT {:?} BAR1={:016x} BAR3={:016x} LIMIT={:016x} attempt={}\n",
        report.outcome, report.base, report.bar3_base, report.limit, report.attempt
    ))
}
