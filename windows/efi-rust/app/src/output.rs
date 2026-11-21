// SPDX-License-Identifier: MIT
//! Preopened log on the image volume; transaction tracing never calls firmware.
use crate::image::protocol;
use alloc::{format, vec::Vec};
use core::fmt::{self, Write};
use uefi::boot;
use uefi::proto::device_path::{DevicePath, DeviceSubType, DeviceType};
use uefi::proto::loaded_image::LoadedImage;
use uefi::proto::media::file::{File, FileAttribute, FileMode, RegularFile};
use uefi::proto::media::fs::SimpleFileSystem;
use uefi::{CString16, Handle, Status};

pub struct Output {
    file: Option<RegularFile>,
    volume: Handle,
    name: CString16,
    length: u64,
    checksum: u64,
}
impl Output {
    /// Preserve previous logs. A physical run requires a writable image volume.
    pub fn open(control: &str) -> Result<Self, Status> {
        let volume = protocol::<LoadedImage>(boot::image_handle())?
            .device()
            .ok_or(Status::NOT_FOUND)?;
        let mut fs = protocol::<SimpleFileSystem>(volume)?;
        let mut root = fs.open_volume().map_err(|e| e.status())?;
        let prefix = match control {
            "full" => "RF",
            "zero" => "RZ",
            "logcheck" => "RL",
            "entrycheck" => "RE",
            _ => return Err(Status::INVALID_PARAMETER),
        };
        for index in 1..=999 {
            // Use 8.3 names on the older FAT firmware used by the C reference.
            let name = CString16::try_from(format!("\\{prefix}{index:06}.LOG").as_str())
                .map_err(|_| Status::INVALID_PARAMETER)?;
            match root.open(&name, FileMode::Read, FileAttribute::empty()) {
                Ok(_) => continue,
                Err(error) if error.status() == Status::NOT_FOUND => (),
                Err(error) => return Err(error.status()),
            }
            let mut file = root
                .open(&name, FileMode::CreateReadWrite, FileAttribute::empty())
                .map_err(|e| e.status())?
                .into_regular_file()
                .ok_or(Status::UNSUPPORTED)?;
            uefi::println!("CMP40-CPU-RM: LOG {:?}", name);
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
            if file.get_position().map_err(|e| e.status())? != 0 {
                return Err(Status::DEVICE_ERROR);
            }
            return Ok(Self {
                file: Some(file),
                volume,
                name,
                length: 0,
                checksum: 0xcbf29ce484222325,
            });
        }
        Err(Status::OUT_OF_RESOURCES)
    }
    pub fn write(&mut self, args: fmt::Arguments<'_>) -> Result<(), Status> {
        let mut line = Line {
            bytes: [0; 1024],
            used: 0,
        };
        line.write_fmt(args).map_err(|_| Status::BAD_BUFFER_SIZE)?;
        let file = self.file.as_mut().ok_or(Status::NOT_READY)?;
        let expected = self
            .length
            .checked_add(line.used as u64)
            .ok_or(Status::VOLUME_FULL)?;
        file.write(&line.bytes[..line.used])
            .map_err(|_| Status::DEVICE_ERROR)?;
        // uefi-rs 0.41's write wrapper discards the actual success byte count.
        // A short successful firmware write must not count as a saved log.
        if file.get_position().map_err(|e| e.status())? != expected {
            return Err(Status::DEVICE_ERROR);
        }
        self.length = expected;
        self.checksum = checksum(self.checksum, &line.bytes[..line.used]);
        Ok(())
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
        self.verify_and_reopen()
        // RegularFile closes on Drop, before StartImage or parent return.
    }

    fn verify_and_reopen(&mut self) -> Result<(), Status> {
        self.file
            .as_mut()
            .ok_or(Status::NOT_READY)?
            .flush()
            .map_err(|e| e.status())?;
        drop(self.file.take());
        let mut fs = protocol::<SimpleFileSystem>(self.volume)?;
        let mut root = fs.open_volume().map_err(|e| e.status())?;
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

fn checksum(mut digest: u64, bytes: &[u8]) -> u64 {
    for byte in bytes {
        digest = (digest ^ u64::from(*byte)).wrapping_mul(0x100000001b3);
    }
    digest
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

#[derive(Clone, Copy)]
struct Event {
    kind: u8,
    offset: u32,
    value: u32,
    count: u32,
}
pub struct Trace {
    events: Vec<Event>,
    omitted_ports: u64,
    dropped: u64,
}
impl Trace {
    pub fn new() -> Result<Self, Status> {
        let mut events = Vec::new();
        events
            .try_reserve_exact(131072)
            .map_err(|_| Status::OUT_OF_RESOURCES)?;
        Ok(Self {
            events,
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
        if self.events.len() == self.events.capacity() {
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
    pub fn dump(&self, output: &mut Output) -> Result<(), Status> {
        output.write(format_args!(
            "TRACE_BEGIN records={} omitted_data_ports={} dropped={}\n",
            self.events.len(),
            self.omitted_ports,
            self.dropped
        ))?;
        for event in &self.events {
            output.write(format_args!(
                "{} {:08x} {:08x} count={}\n",
                event.kind as char, event.offset, event.value, event.count
            ))?;
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
