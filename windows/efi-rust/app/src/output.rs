// SPDX-License-Identifier: MIT
//! Preopened log on the image volume; transaction tracing never calls firmware.
use crate::{
    image::protocol,
    log_store::{self, Store},
};
use alloc::vec::Vec;
use cmp40_efi_core::log_write::{self, Sink};
use cmp40_efi_core::{
    log_store::{checksum, Kind, FNV_INITIAL, FOOTER_LEN},
    trace::{self, Event, Record},
};
use core::fmt::{self, Write};
use uefi::boot;
use uefi::proto::device_path::{DevicePath, DeviceSubType, DeviceType};
use uefi::proto::loaded_image::LoadedImage;
use uefi::proto::media::file::{File, FileAttribute, FileMode, RegularFile};
use uefi::{CString16, Handle, Status};

pub struct Output {
    file: Option<RegularFile>,
    volume: Handle,
    name: CString16,
    length: u64,
    checksum: u64,
    store: Store,
    pending: [u8; 4096],
    pending_used: usize,
    writer: log_write::State<Status>,
    full: bool,
    detail_decided: bool,
    sealing: bool,
    traces: [Option<(&'static str, Trace)>; 3],
    trace_requested: bool,
    diagnostic: bool,
}
impl Output {
    /// Preserve previous logs. A physical run requires a writable image volume.
    pub fn open(control: &str) -> Result<Self, Status> {
        let volume = protocol::<LoadedImage>(boot::image_handle())?
            .device()
            .ok_or(Status::NOT_FOUND)?;
        if !matches!(control, "full" | "zero" | "logcheck" | "entrycheck") {
            return Err(Status::INVALID_PARAMETER);
        }
        let store = Store::open(volume)?;
        let name = log_store::name(store.generation)?;
        let mut root = log_store::directory(volume, false)?;
        match root.open(&name, FileMode::Read, FileAttribute::empty()) {
            Err(error) if error.status() == Status::NOT_FOUND => (),
            _ => return Err(Status::DEVICE_ERROR),
        }
        let file = root
            .open(&name, FileMode::CreateReadWrite, FileAttribute::empty())
            .map_err(|e| e.status())?
            .into_regular_file()
            .ok_or(Status::UNSUPPORTED)?;
        uefi::println!("CMP40-CPU-RM: LOG \\EFI\\40HX\\LOGS\\{}", name);
        uefi::println!("CMP40-CPU-RM: LOG_VOLUME handle={:?}", volume);
        if let Ok(path) = protocol::<DevicePath>(volume) {
            for node in path.node_iter() {
                let bytes = node.data();
                if bytes.len() == 38
                    && node.full_type() == (DeviceType::MEDIA, DeviceSubType::MEDIA_HARD_DRIVE)
                {
                    let partition = u32::from_le_bytes(bytes[..4].try_into().unwrap());
                    let start = u64::from_le_bytes(bytes[4..12].try_into().unwrap());
                    uefi::println!(
                        "CMP40-CPU-RM: LOG_PARTITION number={} start_lba={} signature={:02x?}",
                        partition,
                        start,
                        &bytes[20..36]
                    );
                }
            }
        }
        let generation = store.generation;
        let mut output = Self {
            file: Some(file),
            volume,
            name,
            length: 0,
            checksum: FNV_INITIAL,
            store,
            pending: [0; 4096],
            pending_used: 0,
            writer: log_write::State::new(),
            full: false,
            detail_decided: false,
            sealing: false,
            traces: [None, None, None],
            trace_requested: false,
            diagnostic: false,
        };
        output.write(format_args!(
            "CMP40_LOG_V3 generation={:07} control={}\n",
            generation, control
        ))?;
        output.verify_and_reopen()?;
        Ok(output)
    }
    pub fn write(&mut self, args: fmt::Arguments<'_>) -> Result<(), Status> {
        let mut line = Line {
            bytes: [0; 1024],
            used: 0,
        };
        line.write_fmt(args).map_err(|_| Status::BAD_BUFFER_SIZE)?;
        self.append(&line.bytes[..line.used])
    }
    pub fn hex_line(&mut self, prefix: fmt::Arguments<'_>, bytes: &[u8]) -> Result<(), Status> {
        let mut line = Line {
            bytes: [0; 1024],
            used: 0,
        };
        line.write_fmt(prefix)
            .map_err(|_| Status::BAD_BUFFER_SIZE)?;
        for byte in bytes {
            write!(line, "{:02x}", byte).map_err(|_| Status::BAD_BUFFER_SIZE)?;
        }
        line.write_str("\n").map_err(|_| Status::BAD_BUFFER_SIZE)?;
        self.append(&line.bytes[..line.used])
    }
    fn append(&mut self, mut bytes: &[u8]) -> Result<(), Status> {
        if let Some(error) = self.writer.failure() {
            return Err(write_status(error));
        }
        let projected = self
            .length
            .checked_add(bytes.len() as u64)
            .and_then(|n| n.checked_add(if self.sealing { 0 } else { FOOTER_LEN as u64 }))
            .ok_or(Status::VOLUME_FULL)?;
        let kind = if self.full || self.trace_requested || self.diagnostic {
            Some(Kind::Trace)
        } else if self.detail_decided || self.sealing {
            Some(Kind::Summary)
        } else {
            None
        };
        if let Err(status) = self.store.room(projected, kind) {
            self.writer.poison(log_write::Error::Io(status));
            return Err(status);
        }
        while !bytes.is_empty() {
            let take = bytes.len().min(self.pending.len() - self.pending_used);
            self.pending[self.pending_used..self.pending_used + take]
                .copy_from_slice(&bytes[..take]);
            self.pending_used += take;
            self.length += take as u64;
            self.checksum = checksum(self.checksum, &bytes[..take]);
            bytes = &bytes[take..];
            if self.pending_used == self.pending.len() {
                self.flush_pending()?;
            }
        }
        Ok(())
    }
    fn flush_pending(&mut self) -> Result<(), Status> {
        if let Some(error) = self.writer.failure() {
            return Err(write_status(error));
        }
        if self.pending_used == 0 {
            return Ok(());
        }
        let file = self.file.as_mut().ok_or(Status::NOT_READY)?;
        self.writer
            .commit(&mut FileSink(file), &self.pending[..self.pending_used])
            .map_err(write_status)?;
        self.pending_used = 0;
        Ok(())
    }
    pub fn physical_writes(&self) -> u64 {
        self.writer.batches()
    }
    pub fn checkpoint(&mut self, args: fmt::Arguments<'_>) -> Result<(), Status> {
        uefi::print!("{}", args);
        self.write(args)?;
        self.verify_and_reopen()?;
        uefi::println!(
            "CMP40-CPU-RM: LOG_VERIFIED {:?} bytes={}",
            self.name,
            self.length
        );
        Ok(())
    }
    pub fn seal(mut self) -> Result<(), Status> {
        let (length, digest) = (self.length, self.checksum);
        let full = self.full || self.diagnostic || self.trace_requested;
        self.sealing = true;
        self.write(format_args!(
            "CMP40_END_V3 kind={} bytes={:020} fnv={:016x}\n",
            if full { 'T' } else { 'S' },
            length,
            digest
        ))?;
        self.verify_and_reopen()?;
        drop(self.file.take());
        self.store
            .finish(self.length, if full { Kind::Trace } else { Kind::Summary })
    }

    pub fn request_trace(&mut self, requested: bool) {
        self.trace_requested = requested;
    }
    pub fn diagnostic(&mut self) {
        self.diagnostic = true;
    }
    /// Fixed slots retain every phase until the overall result is known.
    pub fn retain_trace(&mut self, phase: &'static str, trace: Trace) -> Result<(), Status> {
        self.diagnostic |= trace.dropped != 0;
        let slot = self
            .traces
            .iter_mut()
            .find(|slot| slot.is_none())
            .ok_or(Status::OUT_OF_RESOURCES)?;
        *slot = Some((phase, trace));
        Ok(())
    }
    pub fn finish_traces(&mut self, failed: bool) -> Result<(), Status> {
        let (requested, diagnostic) = (self.trace_requested, self.diagnostic);
        let full = trace::full_log(requested, failed, diagnostic);
        self.full = full;
        self.detail_decided = true;
        self.write(format_args!(
            "LOG_DETAIL mode={} requested={} failed={} diagnostic={}\n",
            if full { "trace" } else { "summary" },
            requested,
            failed,
            diagnostic
        ))?;
        // Take one slot at a time: output IO cannot alias retained trace RAM.
        for index in 0..self.traces.len() {
            if let Some((phase, trace)) = self.traces[index].take() {
                if full {
                    trace.dump(self, phase)?;
                } else {
                    trace.summary(self, phase)?;
                }
            }
        }
        Ok(())
    }

    fn verify_and_reopen(&mut self) -> Result<(), Status> {
        let result = self.verify_inner();
        if let Err(status) = result {
            self.writer.poison(log_write::Error::Io(status));
        }
        result
    }
    fn verify_inner(&mut self) -> Result<(), Status> {
        self.flush_pending()?;
        self.file
            .as_mut()
            .ok_or(Status::NOT_READY)?
            .flush()
            .map_err(|e| e.status())?;
        drop(self.file.take());
        let mut root = log_store::directory(self.volume, false)?;
        let file = root
            .open(&self.name, FileMode::ReadWrite, FileAttribute::empty())
            .map_err(|e| e.status())?
            .into_regular_file()
            .ok_or(Status::UNSUPPORTED)?;
        self.file = Some(file);
        let file = self.file.as_mut().ok_or(Status::NOT_READY)?;
        file.set_position(0).map_err(|e| e.status())?;
        let mut buffer = [0u8; 4096];
        let mut remaining = self.length;
        let mut digest = 0xcbf29ce484222325;
        while remaining != 0 {
            let requested = remaining.min(buffer.len() as u64) as usize;
            let read = file
                .read(&mut buffer[..requested])
                .map_err(|e| e.status())?;
            if read == 0 || read > requested {
                return Err(Status::DEVICE_ERROR);
            }
            digest = checksum(digest, &buffer[..read]);
            remaining -= read as u64;
        }
        if digest != self.checksum || file.read(&mut buffer[..1]).map_err(|e| e.status())? != 0 {
            return Err(Status::DEVICE_ERROR);
        }
        file.set_position(self.length).map_err(|e| e.status())
    }
}

struct FileSink<'a>(&'a mut RegularFile);
impl Sink for FileSink<'_> {
    type Error = Status;
    fn write(&mut self, bytes: &[u8]) -> Result<(), Status> {
        self.0.write(bytes).map_err(|e| e.status())
    }
    fn position(&mut self) -> Result<u64, Status> {
        self.0.get_position().map_err(|e| e.status())
    }
}
fn write_status(error: log_write::Error<Status>) -> Status {
    match error {
        log_write::Error::Io(status) => status,
        log_write::Error::ShortWrite => Status::DEVICE_ERROR,
        log_write::Error::Overflow => Status::VOLUME_FULL,
    }
}

struct Line {
    bytes: [u8; 1024],
    used: usize,
}
impl Write for Line {
    fn write_str(&mut self, text: &str) -> fmt::Result {
        let end = self
            .used
            .checked_add(text.len())
            .filter(|&n| n <= self.bytes.len())
            .ok_or(fmt::Error)?;
        self.bytes[self.used..end].copy_from_slice(text.as_bytes());
        self.used = end;
        Ok(())
    }
}

pub struct Trace {
    events: Vec<Event>,
    capacity: usize,
    omitted_ports: u64,
    dropped: u64,
}
impl Trace {
    pub fn new() -> Result<Self, Status> {
        Self::with_capacity(trace::MAX_EVENTS)
    }
    pub fn with_capacity(capacity: usize) -> Result<Self, Status> {
        if capacity == 0 || capacity > trace::MAX_EVENTS {
            return Err(Status::INVALID_PARAMETER);
        }
        let mut events = Vec::new();
        events
            .try_reserve_exact(capacity)
            .map_err(|_| Status::OUT_OF_RESOURCES)?;
        Ok(Self {
            events,
            capacity,
            omitted_ports: 0,
            dropped: 0,
        })
    }
    /// Only records in preallocated RAM. Firmware data ports are counted but
    /// never logged as firmware bytes. All other operations retain their order.
    pub fn record(&mut self, kind: u8, offset: u32, value: u32) {
        if matches!(offset, 0x110184 | 0x1101c4 | 0x840184 | 0x8401c4)
            && matches!(kind, b'R' | b'W')
        {
            self.omitted_ports += 1;
            return;
        }
        if let Some(last) = self.events.last_mut() {
            if last.kind == kind
                && last.offset == offset
                && last.value == value
                && last.count < u32::MAX
            {
                last.count += 1;
                return;
            }
        }
        if self.events.len() == self.capacity {
            self.dropped += 1;
            return;
        }
        self.events.push(Event {
            kind,
            offset,
            value,
            count: 1,
        });
    }
    fn operations(&self) -> u64 {
        self.events.iter().map(|event| u64::from(event.count)).sum()
    }
    pub fn summary(&self, output: &mut Output, phase: &str) -> Result<(), Status> {
        output.write(format_args!(
            "TRACE_SUMMARY version=2 phase={} records={} operations={} omitted_data_ports={} dropped={}\n",
            phase,
            self.events.len(),
            self.operations(),
            self.omitted_ports,
            self.dropped
        ))
    }
    pub fn dump(&self, output: &mut Output, phase: &str) -> Result<(), Status> {
        output.write(format_args!(
            "TRACE_BEGIN version=2 phase={} records={} encoded_records={} operations={} omitted_data_ports={} dropped={}\n",
            phase, self.events.len(), trace::encode(&self.events).count(),
            self.operations(), self.omitted_ports, self.dropped
        ))?;
        for record in trace::encode(&self.events) {
            match record {
                Record::Event(event) => output.write(format_args!(
                    "{} {:08x} {:08x} count={}\n",
                    event.kind as char, event.offset, event.value, event.count
                ))?,
                Record::RepeatPair { additional } => {
                    output.write(format_args!("REPEAT_PAIR additional={}\n", additional))?
                }
            }
        }
        output.write(format_args!("TRACE_END\n"))
    }
    pub fn last_read(&self, offset: u32) -> Option<u32> {
        self.events
            .iter()
            .rev()
            .find(|event| event.kind == b'R' && event.offset == offset)
            .map(|event| event.value)
    }
}
