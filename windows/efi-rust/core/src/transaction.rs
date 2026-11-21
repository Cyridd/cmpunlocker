// SPDX-License-Identifier: MIT
//! CPU-RM execution after PCI discovery, Windows preload, PTIMER seeding and
//! reserved-buffer preparation. The same code can run against an EFI adapter
//! or a host model. This module supplies neither adapter nor firmware assets.
use crate::state::{Control, Snapshot, StateError, WPR2_HI_UP, WPR2_LO_UP};
use crate::valid_dma_region;

pub mod reg {
    pub const SS0: u32 = 0x409664;
    pub const SS1: u32 = 0x40966c;
    pub const FEAT_PLM: u32 = 0x409650;
    pub const WPR_LO: u32 = 0x1fa824;
    pub const WPR_HI: u32 = 0x1fa828;
    pub const GFW: u32 = 0x118234;
    pub const GSP: u32 = 0x110000;
    pub const SEC2: u32 = 0x840000;
    pub const GSP_BCR: u32 = 0x111668;
    pub const SEC2_BCR: u32 = 0x841668;
    pub const CPU: u32 = 0x100;
    pub const ENGINE: u32 = 0x3c0;
    pub const DMA: u32 = 0x10c;
    pub const HWCFG2: u32 = 0xf4;
    pub const MBOX0: u32 = 0x40;
    pub const MBOX1: u32 = 0x44;
    pub const IMEMC: u32 = 0x180;
    pub const IMEMD: u32 = 0x184;
    pub const IMEMT: u32 = 0x188;
    pub const DMEMC: u32 = 0x1c0;
    pub const DMEMD: u32 = 0x1c4;
    pub const FBIF_CTL: u32 = 0x624;
    pub const BOOTVEC: u32 = 0x104;
}
use reg::*;

pub const FWSEC_BYTES: usize = 0x9df0;
pub const BOOTER_BYTES: usize = 0xe700;
pub const LOADER_CODE_BYTES: usize = 0x200;
pub const LOADER_BYTES: usize = 0x300;
pub const BOOTER_SIGNATURE_OFFSET: usize = 0x8700;
const FWSEC_DATA: usize = 0x9a00;
const START: u32 = 2;
const HALT: u32 = 0x10;
const AINCW: u32 = 1 << 24;
const SECURE: u32 = 1 << 28;

/// Implementations must preserve volatile MMIO, delays, and WBINVD's memory
/// barrier. No PCI, allocation, release or handoff methods are exposed here.
pub trait Io {
    fn read32(&mut self, offset: u32) -> u32;
    fn write32(&mut self, offset: u32, value: u32);
    fn stall(&mut self, microseconds: u32);
    fn flush(&mut self);
    /// Report the value already read by a failed gate, without extra MMIO.
    fn booter_failure(&mut self, _failure: BooterFailure) {}
    /// Observe FWSEC completion without filesystem calls or additional IO.
    fn fwsec_result(&mut self, _result: FwsecResult) {}
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct FwsecRegisters {
    pub mailbox0: u32,
    pub mailbox1: u32,
    pub scratch0e: u32,
    pub wpr2_lo: u32,
    pub wpr2_hi: u32,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct FwsecResult {
    pub cpuctl: u32,
    pub waited_us: u32,
    /// Result registers are sampled only after HALT, when FWSEC is stopped.
    pub registers: Option<FwsecRegisters>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum BooterCheck {
    PreloadHalt,
    NonSecureCode,
    ProductionSignature,
    BootVector,
    MetadataLow,
    MetadataHigh,
    MailboxStatus,
    CompletionHalt,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct BooterFailure {
    pub check: BooterCheck,
    pub register: u32,
    pub observed: u32,
    pub expected: u32,
}

fn failed_booter(
    io: &mut impl Io,
    check: BooterCheck,
    register: u32,
    observed: u32,
    expected: u32,
    error: Error,
) -> Error {
    io.booter_failure(BooterFailure {
        check,
        register,
        observed,
        expected,
    });
    error
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Error {
    InvalidInput,
    Device,
    NotReady,
    Timeout,
    AlreadyAttempted,
}

impl From<StateError> for Error {
    fn from(error: StateError) -> Self {
        match error {
            StateError::Unreadable => Self::Device,
            StateError::NotReady => Self::NotReady,
        }
    }
}

fn unavailable(value: u32) -> bool {
    value == u32::MAX || value & 0xffff_0000 == 0xbadf_0000
}

fn word(bytes: &[u8], offset: usize) -> u32 {
    u32::from_le_bytes(bytes[offset..offset + 4].try_into().unwrap())
}

/// Caller owns live reserved memory and must ensure that `fwsec` aliases its
/// DMA address, that metadata refers to the existing FULL/ZERO payload/radix
/// buffers, and that the Booter's original production signature is patched.
/// Numeric validation here does not prove mapping, asset hashes or identity.
pub struct Prepared<'a> {
    pub fwsec: &'a mut [u8],
    pub fwsec_dma: u64,
    pub loader: &'a [u8],
    pub booter: &'a [u8],
    pub metadata_dma: u64,
}

/// FWSEC allocation is obtained after GFW stop/SEC2 checks in the C entry.
/// An EFI caller supplies this from the deferred callback in Transaction::run.
pub struct Fwsec<'a> {
    pub bytes: &'a mut [u8],
    pub dma: u64,
    pub loader: &'a [u8],
}

impl Prepared<'_> {
    fn validate(&self) -> Result<(), Error> {
        if self.fwsec.len() != FWSEC_BYTES
            || self.loader.len() != LOADER_BYTES
            || self.booter.len() != BOOTER_BYTES
            || !valid_dma_region(self.fwsec_dma, FWSEC_BYTES)
            || !valid_dma_region(self.metadata_dma, 256)
        {
            return Err(Error::InvalidInput);
        }
        Ok(())
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Phase {
    Fresh,
    GfwStopped,
    Fwsec,
    FwsecCompletion,
    Booter,
    Completion,
    Ready,
    Failed,
}

/// One object per boot attempt, retained after failure. Neither error handling
/// nor Drop resets an engine, clears mailboxes or releases DMA allocations.
pub struct Transaction {
    phase: Phase,
    failed_at: Option<Phase>,
}

impl Default for Transaction {
    fn default() -> Self {
        Self::new()
    }
}

impl Transaction {
    pub const fn new() -> Self {
        Self {
            phase: Phase::Fresh,
            failed_at: None,
        }
    }

    pub const fn phase(&self) -> Phase {
        self.phase
    }

    pub const fn failed_at(&self) -> Option<Phase> {
        self.failed_at
    }

    /// Host convenience for already prepared buffers. An EFI caller must use
    /// `run` to allocate/copy FWSEC at the original point after stopping GFW.
    pub fn run_prepared(
        &mut self,
        io: &mut impl Io,
        prepared: &mut Prepared<'_>,
        control: Control,
    ) -> Result<(), Error> {
        if self.phase != Phase::Fresh {
            return Err(Error::AlreadyAttempted);
        }
        if let Err(error) = prepared.validate() {
            self.failed_at = Some(Phase::Fresh);
            self.phase = Phase::Failed;
            return Err(error);
        }
        self.run(
            io,
            control,
            prepared.metadata_dma,
            |io| {
                io.flush();
                Ok(Fwsec {
                    bytes: prepared.fwsec,
                    dma: prepared.fwsec_dma,
                    loader: prepared.loader,
                })
            },
            |io| {
                io.flush();
                Ok(prepared.booter)
            },
        )
    }

    /// Execute the production FULL/ZERO firmware segment. Prerequisites must
    /// already have been checked before GPU mutation, including the loaded
    /// Windows handle (unless returning to the parent). Success only permits
    /// the caller to seal output and hand off; this code cannot call StartImage.
    /// The FWSEC callback allocates, copies and flushes board FWSEC after the
    /// pre-FWSEC engine checks, preserving C's allocation and cache-barrier
    /// order. The Booter callback recopies the original image, applies its
    /// existing production signature and flushes after SEC2 health, immediately
    /// before port loading. Verify all input assets before invoking `run`.
    /// Callback errors stop the attempt without firmware fallback.
    /// WPR2 appearance is not completion: before SEC2 health/preparation,
    /// require GSP HALT and recheck FRTS result/WPR2. This read-only barrier
    /// is an intentional correction to the C entry's early WPR2 return.
    pub fn run<'a, 'b, I: Io>(
        &mut self,
        io: &mut I,
        control: Control,
        metadata_dma: u64,
        prepare_fwsec: impl FnOnce(&mut I) -> Result<Fwsec<'a>, Error>,
        prepare_booter: impl FnOnce(&mut I) -> Result<&'b [u8], Error>,
    ) -> Result<(), Error> {
        if self.phase != Phase::Fresh {
            return Err(Error::AlreadyAttempted);
        }
        let result = (|| {
            if !valid_dma_region(metadata_dma, 256) {
                return Err(Error::InvalidInput);
            }
            self.phase = Phase::GfwStopped;
            prepare_engines(io)?;
            self.phase = Phase::Fwsec;
            let fwsec = prepare_fwsec(io)?;
            boot_fwsec(io, fwsec.bytes, fwsec.dma, fwsec.loader)?;
            self.phase = Phase::FwsecCompletion;
            complete_fwsec(io)?;
            // These are the main entry's WPR2 diagnostic reads.
            observe(io, &[WPR_HI, WPR_LO]);
            sec2_health(io);
            self.phase = Phase::Booter;
            let booter = prepare_booter(io)?;
            boot_sec2(io, booter, metadata_dma)?;
            self.phase = Phase::Completion;
            check_completion(io, control)?;
            self.phase = Phase::Ready;
            Ok(())
        })();
        if result.is_err() {
            self.failed_at = Some(self.phase);
            self.phase = Phase::Failed;
        }
        result
    }
}

/// Wait for actual FWSEC completion after WPR2 first appears. The Linux
/// CPU-RM path requires HALT before checking scratch0e's FRTS error field and
/// the protected window. No engine writes, reset, retry or DMA release occur.
pub fn complete_fwsec(io: &mut impl Io) -> Result<(), Error> {
    let mut last_cpu = 0;
    for poll in 0..5000 {
        let cpu = io.read32(GSP + CPU);
        last_cpu = cpu;
        if unavailable(cpu) {
            io.fwsec_result(FwsecResult {
                cpuctl: cpu,
                waited_us: poll * 1000,
                registers: None,
            });
            return Err(Error::Device);
        }
        if cpu & HALT != 0 {
            let registers = FwsecRegisters {
                mailbox0: io.read32(GSP + MBOX0),
                mailbox1: io.read32(GSP + MBOX1),
                scratch0e: io.read32(0x1438),
                wpr2_lo: io.read32(WPR_LO),
                wpr2_hi: io.read32(WPR_HI),
            };
            io.fwsec_result(FwsecResult {
                cpuctl: cpu,
                waited_us: poll * 1000,
                registers: Some(registers),
            });
            if [registers.scratch0e, registers.wpr2_lo, registers.wpr2_hi]
                .into_iter()
                .any(unavailable)
            {
                return Err(Error::Device);
            }
            if registers.scratch0e >> 16 != 0
                || registers.wpr2_lo & 0xfffffff0 != WPR2_LO_UP
                || registers.wpr2_hi & 0xfffffff0 != WPR2_HI_UP
            {
                return Err(Error::NotReady);
            }
            return Ok(());
        }
        io.stall(1000);
    }
    io.fwsec_result(FwsecResult {
        cpuctl: last_cpu,
        waited_us: 5_000_000,
        registers: None,
    });
    Err(Error::Timeout)
}

fn observe(io: &mut impl Io, offsets: &[u32]) {
    for &offset in offsets {
        io.read32(offset);
    }
}

pub fn reset_gsp(io: &mut impl Io) {
    io.write32(GSP + ENGINE, 1);
    for _ in 0..16 {
        io.read32(GSP + ENGINE);
    }
    io.write32(GSP + ENGINE, 0);
    for _ in 0..16 {
        io.read32(GSP + ENGINE);
    }
}

pub fn prepare_engines(io: &mut impl Io) -> Result<(), Error> {
    reset_gsp(io);
    io.stall(200_000);
    let mut cpu = io.read32(SEC2 + CPU);
    let dma = io.read32(SEC2 + 0x118);
    observe(io, &[SEC2 + FBIF_CTL, SEC2_BCR]);
    if cpu == u32::MAX || dma == u32::MAX {
        return Err(Error::Device);
    }
    // Preserve the C entry's one conditional Falcon-core selection. This is
    // pre-FWSEC discovery recovery, not a retry of a launched transaction.
    if cpu & 0xbadf_0000 == 0xbadf_0000 || dma & 0xbadf_0000 == 0xbadf_0000 {
        io.write32(SEC2_BCR, 0);
        for _ in 0..16 {
            io.read32(SEC2_BCR);
        }
        io.stall(10_000);
        cpu = io.read32(SEC2 + CPU);
    }
    if cpu & 0xbadf_0000 == 0xbadf_0000 {
        return Err(Error::NotReady);
    }
    if unavailable(io.read32(SEC2 + CPU)) || unavailable(io.read32(SEC2 + 0x118)) {
        return Err(Error::Device);
    }
    Ok(())
}

fn wait_scrub(io: &mut impl Io) -> Result<(), Error> {
    for _ in 0..30_000 {
        if io.read32(GSP + DMA) & 6 == 0 && io.read32(GSP + HWCFG2) & (1 << 12) == 0 {
            return Ok(());
        }
        io.stall(100);
    }
    Err(Error::NotReady)
}

/// Patch only the existing FRTS command fields. Even if mapper[2] advertises
/// another offset, the current C implementation still writes at 0x3b0.
pub fn patch_frts(blob: &mut [u8]) -> Result<(), Error> {
    if blob.len() != FWSEC_BYTES {
        return Err(Error::InvalidInput);
    }
    for (index, value) in [1_u32, 24, 0, 0, 0, 2, 1, 20, 0x1ffe00, 0x100, 2]
        .into_iter()
        .enumerate()
    {
        let offset = FWSEC_DATA + 0x3b0 + index * 4;
        blob[offset..offset + 4].copy_from_slice(&value.to_le_bytes());
    }
    blob[FWSEC_DATA + 0x38c..FWSEC_DATA + 0x390].copy_from_slice(&0x15_u32.to_le_bytes());
    Ok(())
}

pub fn loader_descriptor(fwsec_dma: u64) -> Result<[u8; 0x54], Error> {
    if !valid_dma_region(fwsec_dma, FWSEC_BYTES) {
        return Err(Error::InvalidInput);
    }
    let data_dma = fwsec_dma + FWSEC_DATA as u64;
    let mut bytes = [0; 0x54];
    for (offset, value) in [
        (0x20, 4),
        (0x24, fwsec_dma as u32),
        (0x28, (fwsec_dma >> 32) as u32),
        (0x30, 0x400),
        (0x34, 0x400),
        (0x38, 0x9600),
        (0x40, data_dma as u32),
        (0x44, (data_dma >> 32) as u32),
        (0x48, 0x3f0),
    ] {
        bytes[offset..offset + 4].copy_from_slice(&value.to_le_bytes());
    }
    Ok(bytes)
}

/// Existing WITH_LOADER sequence; FALSE-like FWSEC failures map to NotReady,
/// matching the C production entry's stage-abort result.
pub fn boot_fwsec(io: &mut impl Io, blob: &mut [u8], dma: u64, loader: &[u8]) -> Result<(), Error> {
    if blob.len() != FWSEC_BYTES || loader.len() != LOADER_BYTES {
        return Err(Error::InvalidInput);
    }
    let desc = loader_descriptor(dma)?;
    io.write32(GSP + 0x80, 0);
    // C's RESET_READY pre-wait is advisory: expiry still proceeds to reset.
    for _ in 0..10_000 {
        if io.read32(GSP + HWCFG2) & (1 << 31) != 0 {
            break;
        }
        io.stall(100);
    }
    reset_gsp(io);
    wait_scrub(io)?;
    io.stall(50_000);
    io.write32(GSP_BCR, 0);
    for _ in 0..16 {
        io.read32(GSP_BCR);
    }
    io.write32(GSP + 0x84, 0x1660_00a1);
    for _ in 0..16 {
        io.read32(GSP + 0x84);
    }
    observe(io, &[GSP + CPU, GSP_BCR, GSP + HWCFG2]);
    let cpu = io.read32(GSP + CPU);
    if unavailable(cpu) || cpu & HALT == 0 {
        return Err(Error::NotReady);
    }
    if io.read32(GSP + CPU) & 0xbadf_0000 == 0xbadf_0000 {
        return Err(Error::NotReady);
    }
    patch_frts(blob)?;
    io.flush();
    let fbif = io.read32(GSP + FBIF_CTL);
    io.write32(GSP + FBIF_CTL, fbif | (1 << 7));
    io.write32(GSP + DMA, 0);
    io.write32(GSP + 0x600, 5);
    io.write32(GSP + 0x610, 5);
    dmem(io, GSP, 0, &desc);
    io.flush();
    io.write32(GSP + DMEMC, 0);
    let hwcfg = io.read32(GSP + 0x108);
    if unavailable(hwcfg) || hwcfg & 0x1ff < 2 {
        return Err(Error::NotReady);
    }
    let destination = ((hwcfg & 0x1ff) - 2) << 8;
    imem(
        io,
        GSP,
        destination,
        false,
        &loader[..LOADER_CODE_BYTES],
        0xfd,
    );
    io.flush();
    io.write32(GSP + IMEMC, 0);
    io.write32(GSP + IMEMC, destination);
    if io.read32(GSP + IMEMD) != word(loader, 0) {
        return Err(Error::NotReady);
    }
    io.write32(GSP + DMEMC, 0x20);
    observe(io, &[GSP + DMEMD, GSP + DMEMD]);
    if io.read32(GSP + DMEMD) != 4 {
        return Err(Error::NotReady);
    }
    io.write32(GSP + DMEMC, 0);
    io.write32(GSP + BOOTVEC, 0xfd00);
    io.flush();
    io.write32(GSP + CPU, START);
    for i in 0..5000 {
        let lo = io.read32(WPR_LO);
        let hi = io.read32(WPR_HI);
        if lo & 0xffff_fff0 == WPR2_LO_UP && hi & 0xffff_fff0 == WPR2_HI_UP {
            return Ok(());
        }
        if i % 500 == 0 {
            io.write32(GSP + IMEMC, 0);
            io.read32(GSP + IMEMD);
            io.write32(GSP + DMEMC, 0x10);
            io.read32(GSP + DMEMD);
            io.write32(GSP + DMEMC, 0);
            io.write32(GSP + IMEMC, 0);
            observe(io, &[GSP + CPU, GSP + 0x94, GSP + MBOX0]);
        }
        io.stall(1000);
    }
    observe(
        io,
        &[WPR_LO, WPR_HI, GSP + CPU, GSP + 0x94, GSP + MBOX0, 0x1438],
    );
    Err(Error::NotReady)
}

fn imem(io: &mut impl Io, base: u32, dst: u32, secure: bool, bytes: &[u8], mut tag: u32) {
    io.write32(
        base + IMEMC,
        (dst & 0xfffffc) | AINCW | if secure { SECURE } else { 0 },
    );
    for (index, chunk) in bytes.chunks_exact(4).enumerate() {
        if index % 64 == 0 {
            io.write32(base + IMEMT, tag & 0xffff);
            tag += 1;
        }
        io.write32(base + IMEMD, u32::from_le_bytes(chunk.try_into().unwrap()));
    }
}

fn dmem(io: &mut impl Io, base: u32, dst: u32, bytes: &[u8]) {
    io.write32(base + DMEMC, (dst & 0xfffffc) | AINCW);
    for chunk in bytes.chunks_exact(4) {
        io.write32(base + DMEMD, u32::from_le_bytes(chunk.try_into().unwrap()));
    }
}

pub fn sec2_health(io: &mut impl Io) {
    let mailbox = io.read32(SEC2 + MBOX0);
    let dma = io.read32(SEC2 + 0x110);
    io.write32(SEC2 + MBOX0, 0xa5a5_c3c3);
    io.read32(SEC2 + MBOX0);
    io.write32(SEC2 + 0x110, 0xdead_beef);
    io.read32(SEC2 + 0x110);
    io.write32(SEC2 + MBOX0, mailbox);
    io.write32(SEC2 + 0x110, dma);
    observe(
        io,
        &[SEC2 + 0x1c, SEC2 + CPU, SEC2 + 8, GSP + CPU, GSP + MBOX0],
    );
}

pub fn boot_sec2(io: &mut impl Io, image: &[u8], metadata_dma: u64) -> Result<(), Error> {
    if image.len() != BOOTER_BYTES || !valid_dma_region(metadata_dma, 256) {
        return Err(Error::InvalidInput);
    }
    observe(
        io,
        &[SEC2 + HWCFG2, SEC2 + CPU, SEC2 + 0x118, WPR_HI, WPR_LO],
    );
    // Preserve the existing host WPR writes and readback; these do not prove
    // that FWSEC established protection and do not replace its success gate.
    io.write32(WPR_LO, WPR2_LO_UP);
    io.write32(WPR_HI, WPR2_HI_UP);
    io.flush();
    io.stall(5000);
    observe(io, &[WPR_HI, WPR_LO, SEC2 + 0x3c4]);
    let cpu = io.read32(SEC2 + CPU);
    if unavailable(cpu) || cpu & HALT == 0 {
        return Err(failed_booter(
            io,
            BooterCheck::PreloadHalt,
            SEC2 + CPU,
            cpu,
            HALT,
            Error::NotReady,
        ));
    }
    let fbif = io.read32(SEC2 + FBIF_CTL);
    io.write32(SEC2 + FBIF_CTL, fbif | (1 << 7));
    io.write32(SEC2 + DMA, 0);
    imem(io, SEC2, 0, false, &image[..0x100], 0);
    imem(io, SEC2, 0x100, true, &image[0x100..0x8500], 1);
    dmem(io, SEC2, 0, &image[0x8500..]);
    io.flush();
    io.write32(SEC2 + IMEMC, 0);
    let ns = io.read32(SEC2 + IMEMD);
    io.write32(SEC2 + IMEMC, 0x100 | SECURE);
    io.read32(SEC2 + IMEMD); // Secure readback can be DEAD5EC1; C does not gate it.
    io.write32(SEC2 + DMEMC, 0x200);
    io.read32(SEC2 + DMEMD);
    if ns != word(image, 0) {
        return Err(failed_booter(
            io,
            BooterCheck::NonSecureCode,
            SEC2 + IMEMD,
            ns,
            word(image, 0),
            Error::Device,
        ));
    }
    let signature = io.read32(SEC2 + DMEMD);
    if signature != word(image, BOOTER_SIGNATURE_OFFSET) {
        return Err(failed_booter(
            io,
            BooterCheck::ProductionSignature,
            SEC2 + DMEMD,
            signature,
            word(image, BOOTER_SIGNATURE_OFFSET),
            Error::Device,
        ));
    }
    io.write32(SEC2 + BOOTVEC, 0);
    io.write32(SEC2 + MBOX0, metadata_dma as u32);
    io.write32(SEC2 + MBOX1, (metadata_dma >> 32) as u32);
    io.flush();
    let vector = io.read32(SEC2 + BOOTVEC);
    if vector != 0 {
        return Err(failed_booter(
            io,
            BooterCheck::BootVector,
            SEC2 + BOOTVEC,
            vector,
            0,
            Error::Device,
        ));
    }
    let low = io.read32(SEC2 + MBOX0);
    if low != metadata_dma as u32 {
        return Err(failed_booter(
            io,
            BooterCheck::MetadataLow,
            SEC2 + MBOX0,
            low,
            metadata_dma as u32,
            Error::Device,
        ));
    }
    let high = io.read32(SEC2 + MBOX1);
    if high != (metadata_dma >> 32) as u32 {
        return Err(failed_booter(
            io,
            BooterCheck::MetadataHigh,
            SEC2 + MBOX1,
            high,
            (metadata_dma >> 32) as u32,
            Error::Device,
        ));
    }
    io.write32(SEC2 + CPU, START);
    io.stall(3_000_000); // No reads during this hold.
    let cpu = io.read32(SEC2 + CPU);
    io.stall(1_000_000);
    observe(io, &[SEC2 + 8, SEC2 + 0x94]);
    let mailbox = io.read32(SEC2 + MBOX0);
    observe(io, &[SEC2 + MBOX1, FEAT_PLM, SS0, SS1, WPR_HI, WPR_LO]);
    if mailbox != 0 {
        return Err(failed_booter(
            io,
            BooterCheck::MailboxStatus,
            SEC2 + MBOX0,
            mailbox,
            0,
            Error::Device,
        ));
    }
    if unavailable(cpu) || cpu & HALT == 0 {
        return Err(failed_booter(
            io,
            BooterCheck::CompletionHalt,
            SEC2 + CPU,
            cpu,
            HALT,
            Error::NotReady,
        ));
    }
    Ok(())
}

pub fn wait_fwsec_halt(io: &mut impl Io) -> Result<(), Error> {
    for _ in 0..5000 {
        let cpu = io.read32(GSP + CPU);
        if unavailable(cpu) {
            return Err(Error::Device);
        }
        if cpu & HALT != 0 {
            return Ok(());
        }
        io.stall(1000);
    }
    Err(Error::Timeout)
}

fn snapshot(io: &mut impl Io) {
    observe(
        io,
        &[
            FEAT_PLM,
            SS0,
            SS1,
            WPR_HI,
            WPR_LO,
            SEC2 + CPU,
            SEC2 + MBOX0,
            SEC2 + MBOX1,
            GSP + CPU,
            GSP + MBOX0,
            GSP + MBOX1,
        ],
    );
}

/// Native completion gate, with the C finish-stage read/delay behavior. No
/// cleanup, FLR, mailbox clearing, extra waits or resource releases follow it.
pub fn check_completion(io: &mut impl Io, control: Control) -> Result<(), Error> {
    if let Err(error) = wait_fwsec_halt(io) {
        snapshot(io);
        return Err(error);
    }
    snapshot(io);
    let engine = io.read32(GSP + ENGINE);
    let cpu = io.read32(GSP + CPU);
    let dma = io.read32(GSP + DMA);
    let hwcfg = io.read32(GSP + HWCFG2);
    io.read32(GFW);
    let ss0 = io.read32(SS0);
    let ss1 = io.read32(SS1);
    let hi = io.read32(WPR_HI);
    let lo = io.read32(WPR_LO);
    let sec_cpu = io.read32(SEC2 + CPU);
    let mailbox0 = io.read32(SEC2 + MBOX0);
    // Read PLM only after the selector/WPR/CPU/mailbox checks pass, as in C.
    let preliminary = Snapshot {
        ss0,
        ss1,
        wpr2_hi: hi,
        wpr2_lo: lo,
        sec2_cpu: sec_cpu,
        mailbox0,
        feat_plm: crate::state::STOCK_PLM,
    };
    preliminary.check_booter_completion(control)?;
    Snapshot {
        feat_plm: io.read32(FEAT_PLM),
        ..preliminary
    }
    .check_booter_completion(control)?;
    if [engine, cpu, dma, hwcfg].into_iter().any(unavailable) {
        return Err(Error::Device);
    }
    if engine & 1 != 0 || cpu & HALT == 0 || dma & 6 != 0 || hwcfg & (1 << 12) != 0 {
        return Err(Error::NotReady);
    }
    let sec_cpu = io.read32(SEC2 + CPU);
    if unavailable(sec_cpu) {
        return Err(Error::Device);
    }
    if sec_cpu & HALT == 0 {
        return Err(Error::NotReady);
    }
    Ok(())
}
