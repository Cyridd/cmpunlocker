// SPDX-License-Identifier: MIT
//! Rotate only numbered files with the managed logger's ownership header.
use crate::image::protocol;
use alloc::{format, vec::Vec};
use cmp40_efi_core::log_store::{self, Kind, Record, BUDGET, FNV_INITIAL, FOOTER_LEN};
use uefi::proto::media::{
    file::{Directory, File, FileAttribute, FileInfo, FileMode},
    fs::SimpleFileSystem,
};
use uefi::{cstr16, CString16, Handle, Status};

#[repr(align(8))]
struct EntryBuffer([u8; 4096]);
pub struct Store {
    volume: Handle,
    records: Vec<Record>,
    pub generation: u32,
}
pub fn directory(volume: Handle, create: bool) -> Result<Directory, Status> {
    let mut fs = protocol::<SimpleFileSystem>(volume)?;
    let mut root = fs.open_volume().map_err(|e| e.status())?;
    let mode = if create {
        FileMode::CreateReadWrite
    } else {
        FileMode::ReadWrite
    };
    for path in [
        cstr16!("\\EFI"),
        cstr16!("\\EFI\\40HX"),
        cstr16!("\\EFI\\40HX\\LOGS"),
    ] {
        let file = root
            .open(path, mode, FileAttribute::DIRECTORY)
            .map_err(|e| e.status())?;
        if !file.is_directory().map_err(|e| e.status())? {
            return Err(Status::UNSUPPORTED);
        }
    }
    root.open(
        cstr16!("\\EFI\\40HX\\LOGS"),
        FileMode::ReadWrite,
        FileAttribute::DIRECTORY,
    )
    .map_err(|e| e.status())?
    .into_directory()
    .ok_or(Status::UNSUPPORTED)
}
pub fn name(generation: u32) -> Result<CString16, Status> {
    CString16::try_from(format!("L{generation:07}.LOG").as_str())
        .map_err(|_| Status::INVALID_PARAMETER)
}
fn inspect(dir: &mut Directory, generation: u32) -> Result<Option<Record>, Status> {
    let mut file = dir
        .open(&name(generation)?, FileMode::Read, FileAttribute::empty())
        .map_err(|e| e.status())?
        .into_regular_file()
        .ok_or(Status::UNSUPPORTED)?;
    let mut info_buffer = EntryBuffer([0; 4096]);
    let size = file
        .get_info::<FileInfo>(&mut info_buffer.0)
        .map_err(|e| e.status())?
        .file_size();
    let mut header = [0; 96];
    let read = file.read(&mut header).map_err(|e| e.status())?;
    let Some(footer_len) = log_store::owned_footer_len(&header[..read], generation) else {
        return Ok(None);
    };
    if size > BUDGET {
        return Err(Status::VOLUME_FULL);
    }
    let mut record = Record {
        generation,
        bytes: size,
        kind: Kind::Trace,
        complete: false,
    };
    if size < footer_len as u64 {
        return Ok(Some(record));
    }
    let body = size - footer_len as u64;
    file.set_position(0).map_err(|e| e.status())?;
    let (mut remaining, mut digest) = (body, FNV_INITIAL);
    let mut buffer = [0u8; 4096];
    while remaining != 0 {
        let requested = remaining.min(buffer.len() as u64) as usize;
        let read = file
            .read(&mut buffer[..requested])
            .map_err(|e| e.status())?;
        if read == 0 || read > requested {
            return Err(Status::DEVICE_ERROR);
        }
        digest = log_store::checksum(digest, &buffer[..read]);
        remaining -= read as u64;
    }
    let mut tail = [0; FOOTER_LEN];
    let tail = &mut tail[..footer_len];
    let mut done = 0;
    while done < tail.len() {
        let read = file.read(&mut tail[done..]).map_err(|e| e.status())?;
        if read == 0 {
            return Err(Status::DEVICE_ERROR);
        }
        done += read;
    }
    if file.read(&mut buffer[..1]).map_err(|e| e.status())? != 0 {
        return Err(Status::DEVICE_ERROR);
    }
    if let Some(kind) = log_store::footer(tail, body, digest) {
        record.kind = kind;
        record.complete = true;
    }
    Ok(Some(record))
}
impl Store {
    pub fn open(volume: Handle) -> Result<Self, Status> {
        let mut dir = directory(volume, true)?;
        let mut records = Vec::new();
        let mut maximum = 0;
        let mut buffer = EntryBuffer([0; 4096]);
        let mut ended = false;
        for _ in 0..4096 {
            let Some(info) = dir.read_entry(&mut buffer.0).map_err(|e| e.status())? else {
                ended = true;
                break;
            };
            let filename = format!("{}", info.file_name());
            if let Some(generation) = log_store::filename_generation(&filename) {
                maximum = maximum.max(generation);
                if !info.attribute().contains(FileAttribute::DIRECTORY) {
                    if let Some(record) = inspect(&mut dir, generation)? {
                        records
                            .try_reserve(1)
                            .map_err(|_| Status::OUT_OF_RESOURCES)?;
                        records.push(record);
                    }
                }
            }
        }
        if !ended || maximum >= 9_999_999 {
            return Err(Status::OUT_OF_RESOURCES);
        }
        records
            .try_reserve(1)
            .map_err(|_| Status::OUT_OF_RESOURCES)?;
        let mut store = Self {
            volume,
            records,
            generation: maximum + 1,
        };
        store.prune(None)?;
        Ok(store)
    }
    pub fn room(&mut self, bytes: u64, kind: Option<Kind>) -> Result<(), Status> {
        self.prune(Some((kind, bytes)))
    }
    pub fn finish(&mut self, bytes: u64, kind: Kind) -> Result<(), Status> {
        self.records.push(Record {
            generation: self.generation,
            bytes,
            kind,
            complete: true,
        });
        self.prune(None)
    }
    fn prune(&mut self, active: Option<(Option<Kind>, u64)>) -> Result<(), Status> {
        while let Some(index) =
            log_store::victim(&self.records, active).map_err(|_| Status::VOLUME_FULL)?
        {
            let candidate = self.records[index];
            let mut dir = directory(self.volume, false)?;
            // Recheck ownership and content immediately before deleting a file.
            if inspect(&mut dir, candidate.generation)? != Some(candidate) {
                return Err(Status::DEVICE_ERROR);
            }
            let filename = name(candidate.generation)?;
            dir.open(&filename, FileMode::ReadWrite, FileAttribute::empty())
                .map_err(|e| e.status())?
                .delete()
                .map_err(|e| e.status())?;
            match dir.open(&filename, FileMode::Read, FileAttribute::empty()) {
                Err(error) if error.status() == Status::NOT_FOUND => (),
                _ => return Err(Status::DEVICE_ERROR),
            }
            self.records.remove(index);
        }
        Ok(())
    }
}
