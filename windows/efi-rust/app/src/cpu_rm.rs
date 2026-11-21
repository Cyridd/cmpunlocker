// SPDX-License-Identifier: MIT
//! Experimental FULL/ZERO integration of the existing CPU-RM transaction.
use crate::{
    assets,
    dma::Dma,
    handoff,
    hardware::{Mmio, Pci},
    output::{Output, Trace},
    policy, terminal,
};
use cmp40_efi_core::{
    allocation::HighSlots,
    metadata::{self, DmaAddresses, SignatureKind, WprMetadata},
    options::CpuRmOptions,
    radix::RadixPlan,
    state::{Control, Snapshot},
    timer::{self, Rtc},
    transaction::{self, reg, Io, Transaction},
};
use uefi::{boot, Handle, Status};

struct Backend {
    mmio: Mmio,
    trace: Trace,
    booter_failure: Option<transaction::BooterFailure>,
    fwsec_result: Option<transaction::FwsecResult>,
}
impl Io for Backend {
    fn read32(&mut self, offset: u32) -> u32 {
        let value = self.mmio.read32(offset);
        self.trace.record(b'R', offset, value);
        value
    }
    fn write32(&mut self, offset: u32, value: u32) {
        self.mmio.write32(offset, value);
        self.trace.record(b'W', offset, value);
    }
    fn stall(&mut self, microseconds: u32) {
        boot::stall(core::time::Duration::from_micros(u64::from(microseconds)));
        self.trace.record(b'S', 0, microseconds);
    }
    fn flush(&mut self) {
        Mmio::flush();
        self.trace.record(b'C', 0, 0);
    }
    fn booter_failure(&mut self, failure: transaction::BooterFailure) {
        self.booter_failure = Some(failure);
    }
    fn fwsec_result(&mut self, result: transaction::FwsecResult) {
        self.fwsec_result = Some(result);
    }
}

pub fn entry(control: Control) -> Status {
    if let Err(error) = uefi::helpers::init() {
        return error.status();
    }
    let name = match control {
        Control::Full => "full",
        Control::Zero => "zero",
    };
    uefi::println!(
        "CMP40-CPU-RM: BEGIN control={} version={}",
        name,
        env!("CARGO_PKG_VERSION")
    );
    if let Err(error) = boot::set_watchdog_timer(0, 0x10000, None) {
        return error.status();
    }
    let options = match policy::options() {
        Ok(options) => options,
        Err(status) => {
            uefi::println!("CMP40-CPU-RM: OPTIONS_REFUSED {:?} GPU_WRITES=0", status);
            return status;
        }
    };
    // Resolve the target without any writes. No-device QEMU exits here.
    let mut pci = match Pci::discover() {
        Ok(pci) => pci,
        Err(status) => {
            uefi::println!("CMP40-CPU-RM: GPU_NOT_FOUND {:?} GPU_WRITES=0", status);
            return status;
        }
    };
    uefi::println!(
        "CMP40-CPU-RM: TARGET {:04x}:{:02x}:{:02x}.{} {:?}",
        pci.segment,
        pci.bdf.bus,
        pci.bdf.device,
        pci.bdf.function,
        pci.encoding
    );
    let mut output = match Output::open(name) {
        Ok(output) => output,
        Err(status) => {
            uefi::println!("CMP40-CPU-RM: LOG_OPEN_FAILED {:?} GPU_WRITES=0", status);
            return status;
        }
    };
    let mut windows = None;
    let mut gpu_attempted = false;
    let result = run(
        &mut pci,
        &mut output,
        &mut windows,
        &mut gpu_attempted,
        options,
        control,
    );
    let mut status = result.err().unwrap_or(Status::SUCCESS);
    if status.is_error() {
        let _ = output.checkpoint(format_args!(
            "CMP40-CPU-RM: ABORT {:?}; no retry; power off before another attempt\n",
            status
        ));
    }
    if let Err(error) = output.seal() {
        uefi::println!("CMP40-CPU-RM: LOG_SEAL_FAILED {:?}; handoff refused", error);
        if !status.is_error() {
            status = error;
        }
    }
    if status.is_error() {
        if let Some(image) = windows {
            handoff::release(image);
        }
        return terminal::failure(status, gpu_attempted);
    }
    if let Some(image) = windows {
        // Logs and scoped hardware protocols are closed before entering Windows.
        drop(pci);
        boot::start_image(image).map_or_else(|error| error.status(), |()| Status::SUCCESS)
    } else {
        Status::SUCCESS
    }
}

fn run(
    pci: &mut Pci,
    output: &mut Output,
    windows: &mut Option<Handle>,
    gpu_attempted: &mut bool,
    options: CpuRmOptions,
    control: Control,
) -> Result<(), Status> {
    output.checkpoint(format_args!("CMP40-CPU-RM: BEGIN {:?}; FLR=off\n", control))?;
    output.write(format_args!(
        "TARGET {:04x}:{:02x}:{:02x}.{} route={:?}\n",
        pci.segment, pci.bdf.bus, pci.bdf.device, pci.bdf.function, pci.encoding
    ))?;
    output.write(format_args!("PINNED_ASSETS\n"))?;
    for line in assets::MANIFEST.lines() {
        output.write(format_args!("{}\n", line))?;
    }
    // Allocate trace RAM and prepare the OS before PCI/MMIO mutation.
    let trace = Trace::new()?;
    let source = if !options.return_to_bootloader {
        let prepared = handoff::windows_prepared()?;
        *windows = Some(prepared.image);
        Some(prepared.source)
    } else {
        None
    };
    output.checkpoint(format_args!(
        "CMP40-CPU-RM: HANDOFF_PREPARED parent={} source={:?}\n",
        options.return_to_bootloader, source
    ))?;
    // Conservatively own the failure tail before a config operation that may
    // modify the GPU even if its command readback later fails.
    *gpu_attempted = true;
    let base = pci.enable_bar()?;
    // SAFETY: enable_bar rechecks CMP40 identity, rejects unsupported BARs,
    // and verifies memory/bus-master enable on the existing assigned window.
    let mmio = unsafe { Mmio::new(base) }?;
    let mut io = Backend {
        mmio,
        trace,
        booter_failure: None,
        fwsec_result: None,
    };
    let mut txn = Transaction::new();
    let result = prepare_and_execute(&mut io, &mut txn, output, control);
    if let Some(fwsec) = io.fwsec_result {
        output.checkpoint(format_args!(
            "FWSEC_COMPLETION CPUCTL={:08x} waited_us={} registers={:08x?}\n",
            fwsec.cpuctl, fwsec.waited_us, fwsec.registers
        ))?;
    }
    if let Some(failure) = io.booter_failure {
        // This detail remains visible even if dumping or sealing the log fails.
        output.checkpoint(format_args!(
            "BOOTER_FAILURE check={:?} reg={:08x} observed={:08x} expected={:08x}\n",
            failure.check, failure.register, failure.observed, failure.expected
        ))?;
    }
    // No GPU read/write/reset is introduced in either success or error tail.
    let trace_result = io.trace.dump(output);
    output.checkpoint(format_args!(
        "CMP40-CPU-RM: TRANSACTION {:?} failed_at={:?} result={:?}\n",
        txn.phase(),
        txn.failed_at(),
        result
    ))?;
    result?;
    trace_result?;
    output.write(format_args!(
        "NATIVE_STATE SS0={:08x?} SS1={:08x?} WPR2_HI={:08x?} WPR2_LO={:08x?} PLM={:08x?} SEC2_CPU={:08x?} MBOX0={:08x?} GSP_CPU={:08x?}\n",
        io.trace.last_read(reg::SS0), io.trace.last_read(reg::SS1),
        io.trace.last_read(reg::WPR_HI), io.trace.last_read(reg::WPR_LO),
        io.trace.last_read(reg::FEAT_PLM), io.trace.last_read(reg::SEC2 + reg::CPU),
        io.trace.last_read(reg::SEC2 + reg::MBOX0), io.trace.last_read(reg::GSP + reg::CPU),
    ))?;
    output.checkpoint(format_args!(
        "CMP40-CPU-RM: NATIVE_READY {:?}; terminal handoff\n",
        control
    ))
}

fn prepare_and_execute(
    io: &mut Backend,
    txn: &mut Transaction,
    output: &mut Output,
    control: Control,
) -> Result<(), Status> {
    let boot0 = io.read32(0);
    let initial = Snapshot {
        ss0: io.read32(reg::SS0),
        ss1: io.read32(reg::SS1),
        wpr2_lo: io.read32(reg::WPR_LO),
        wpr2_hi: io.read32(reg::WPR_HI),
        ..Snapshot::default()
    };
    output.checkpoint(format_args!(
        "ENTRY BOOT0={:08x} SS={:08x}:{:08x} WPR2={:08x}:{:08x}\n",
        boot0, initial.ss0, initial.ss1, initial.wpr2_hi, initial.wpr2_lo
    ))?;
    if boot0 & 0xff000000 != 0x16000000 {
        return Err(Status::DEVICE_ERROR);
    }
    initial.check_entry().map_err(|e| status(e.into()))?;
    if io.read32(reg::GFW) & 0xff != 0xff {
        for _ in 0..200 {
            io.stall(50000);
            if io.read32(reg::GFW) & 0xff == 0xff {
                break;
            }
        }
    }
    let gfw = io.read32(reg::GFW);
    if gfw == u32::MAX || gfw & 0xffff0000 == 0xbadf0000 || gfw & 0xff != 0xff {
        return Err(Status::NOT_READY);
    }
    let rtc = uefi::runtime::get_time().ok().map(|t| Rtc {
        year: t.year(),
        month: t.month(),
        day: t.day(),
        hour: t.hour(),
        minute: t.minute(),
        second: t.second(),
    });
    timer::seed(io, rtc);
    let mut slots = HighSlots::new();
    let mut payload = Dma::allocate(&mut slots, metadata::PAYLOAD_BYTES)?;
    payload.copy(assets::payload(control));
    io.flush();
    let mut booter = Dma::allocate(&mut slots, transaction::BOOTER_BYTES)?;
    booter.copy(assets::BOOTER);
    let mut boot = Dma::allocate(&mut slots, metadata::BOOTLOADER_BYTES)?;
    boot.copy(assets::BOOT);
    let plan = RadixPlan::new(metadata::IMAGE_BYTES).map_err(|_| Status::INVALID_PARAMETER)?;
    let mut radix = Dma::allocate(&mut slots, plan.total_bytes())?;
    let radix_address = radix.address();
    plan.write_zero_image(radix.bytes_mut(), radix_address)
        .map_err(|_| Status::INVALID_PARAMETER)?;
    io.flush();
    let mut meta = Dma::allocate(&mut slots, metadata::METADATA_BYTES)?;
    let wire = WprMetadata::cpu_rm(
        DmaAddresses {
            radix: radix.address(),
            signature: payload.address(),
            bootloader: boot.address(),
        },
        SignatureKind::Payload,
    )
    .map_err(|_| Status::INVALID_PARAMETER)?;
    meta.copy(wire.as_bytes());
    io.flush();
    output.write(format_args!(
        "MEMORY_LAYOUT zero_image=in_radix saved_reserved_bytes={} image_bytes={} table_bytes={} radix_bytes={}\n",
        metadata::IMAGE_BYTES.div_ceil(cmp40_efi_core::PAGE_BYTES) * cmp40_efi_core::PAGE_BYTES,
        metadata::IMAGE_BYTES, plan.table_bytes(), plan.total_bytes()
    ))?;
    output.checkpoint(format_args!(
        "BUFFERS payload={:016x} booter={:016x} boot={:016x} radix={:016x} meta={:016x}\n",
        payload.address(),
        booter.address(),
        boot.address(),
        radix.address(),
        meta.address()
    ))?;
    for (index, chunk) in wire.as_bytes().chunks(32).enumerate() {
        output.write(format_args!("META {:02x} ", index * 32))?;
        for byte in chunk {
            output.write(format_args!("{:02x}", byte))?;
        }
        output.write(format_args!("\n"))?;
    }
    output.checkpoint(format_args!(
        "CMP40-CPU-RM: TRANSACTION_BEGIN; trace buffered until completion\n"
    ))?;
    let mut allocation_failure = None;
    let result = txn.run(
        io,
        control,
        meta.address(),
        |io| {
            let mut fwsec =
                Dma::allocate(&mut slots, transaction::FWSEC_BYTES).map_err(|error| {
                    allocation_failure = Some(error);
                    transaction::Error::InvalidInput
                })?;
            fwsec.copy(assets::FWSEC);
            let dma = fwsec.address();
            io.trace.record(b'A', (dma >> 32) as u32, dma as u32);
            io.flush();
            Ok(transaction::Fwsec {
                bytes: &mut fwsec.into_bytes()[..transaction::FWSEC_BYTES],
                dma,
                loader: assets::LOADER,
            })
        },
        |io| {
            booter.copy(assets::BOOTER);
            let start = transaction::BOOTER_SIGNATURE_OFFSET;
            booter.bytes_mut()[start..start + 16].copy_from_slice(assets::BOOTER_SIG);
            io.flush();
            Ok(&booter.into_bytes()[..transaction::BOOTER_BYTES])
        },
    );
    result.map_err(|error| allocation_failure.unwrap_or_else(|| status(error)))
}
fn status(error: transaction::Error) -> Status {
    match error {
        transaction::Error::Device => Status::DEVICE_ERROR,
        transaction::Error::Timeout => Status::TIMEOUT,
        transaction::Error::InvalidInput => Status::INVALID_PARAMETER,
        transaction::Error::NotReady | transaction::Error::AlreadyAttempted => Status::NOT_READY,
    }
}
