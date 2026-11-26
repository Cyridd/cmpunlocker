// SPDX-License-Identifier: MIT
//! Read-only BlockIo adapter. Firmware filesystem methods are never used here.
use crate::image::protocol;
use alloc::vec::Vec;
use cmp40_efi_core::fat::{Error, ReadAt, Region, Volume};
use cmp40_efi_core::partition::{Partition, Signature, Table};
use core::mem::MaybeUninit;
use uefi::boot::{self, ScopedProtocol, SearchType};
use uefi::proto::device_path::{
    build,
    media::{PartitionFormat, PartitionSignature},
    DevicePath, DeviceType,
};
use uefi::proto::media::block::BlockIO;
use uefi::proto::media::fs::SimpleFileSystem;
use uefi::{Guid, Handle, Identify, Status};

struct Reader {
    io: ScopedProtocol<BlockIO>,
    media: u32,
    block: usize,
    bytes: u64,
    cache: Vec<u8>,
    aligned: usize,
    cached: Option<u64>,
    calls: u64,
    blocks_read: u64,
}
impl Reader {
    fn open(handle: Handle) -> Result<Self, Status> {
        let io = protocol::<BlockIO>(handle)?;
        let media = io.media();
        if !media.is_media_present() {
            return Err(Status::NO_MEDIA);
        }
        let block = media.block_size() as usize;
        let alignment = (media.io_align() as usize).max(1);
        if !(512..=4096).contains(&block)
            || !block.is_power_of_two()
            || !alignment.is_power_of_two()
            || alignment > 65536
        {
            return Err(Status::UNSUPPORTED);
        }
        let bytes = media
            .last_block()
            .checked_add(1)
            .and_then(|blocks| blocks.checked_mul(block as u64))
            .ok_or(Status::UNSUPPORTED)?;
        let media = media.media_id();
        let mut cache = Vec::new();
        cache
            .try_reserve_exact(65536 + alignment - 1)
            .map_err(|_| Status::OUT_OF_RESOURCES)?;
        cache.resize(65536 + alignment - 1, 0);
        let aligned = (alignment - (cache.as_ptr() as usize & (alignment - 1))) & (alignment - 1);
        Ok(Self {
            io,
            media,
            block,
            bytes,
            cache,
            aligned,
            cached: None,
            calls: 0,
            blocks_read: 0,
        })
    }
}
impl ReadAt for Reader {
    type Error = Status;
    fn capacity(&self) -> u64 {
        self.bytes
    }
    fn read_at(&mut self, mut offset: u64, mut bytes: &mut [u8]) -> Result<(), Status> {
        if offset
            .checked_add(bytes.len() as u64)
            .is_none_or(|end| end > self.bytes)
        {
            return Err(Status::INVALID_PARAMETER);
        }
        while !bytes.is_empty() {
            let lba = offset / self.block as u64;
            let within = offset as usize % self.block;
            if within == 0 && bytes.len() >= self.block * 2 {
                let take = (bytes.len().min(65536) / self.block) * self.block;
                self.cached = None;
                self.io
                    .read_blocks(
                        self.media,
                        lba,
                        &mut self.cache[self.aligned..self.aligned + take],
                    )
                    .map_err(|e| e.status())?;
                self.calls += 1;
                self.blocks_read += (take / self.block) as u64;
                bytes[..take].copy_from_slice(&self.cache[self.aligned..self.aligned + take]);
                bytes = &mut bytes[take..];
                offset += take as u64;
                continue;
            }
            if self.cached != Some(lba) {
                self.cached = None;
                self.io
                    .read_blocks(
                        self.media,
                        lba,
                        &mut self.cache[self.aligned..self.aligned + self.block],
                    )
                    .map_err(|e| e.status())?;
                self.cached = Some(lba);
                self.calls += 1;
                self.blocks_read += 1;
            }
            let take = (self.block - within).min(bytes.len());
            bytes[..take]
                .copy_from_slice(&self.cache[self.aligned + within..self.aligned + within + take]);
            bytes = &mut bytes[take..];
            offset += take as u64;
        }
        Ok(())
    }
}

pub struct Source {
    pub handle: Handle,
    pub path: Vec<u8>,
    pub region: Option<Region>,
    pub firmware_filesystem: bool,
}
impl Source {
    pub fn own(handle: Handle) -> Result<Self, Status> {
        let path = copy_path(handle)?;
        let region = match Reader::open(handle) {
            Ok(reader) => Some(Region {
                offset: 0,
                bytes: reader.bytes,
            }),
            Err(Status::UNSUPPORTED) => None,
            Err(status) => return Err(status),
        };
        let firmware_filesystem = has_fs(handle)?;
        Ok(Self {
            handle,
            path,
            region,
            firmware_filesystem,
        })
    }

    pub fn read(&self, components: &[&str], limit: usize) -> Result<Vec<u8>, Status> {
        let region = self.region.ok_or(Status::UNSUPPORTED)?;
        let mut reader = Reader::open(self.handle)?;
        let volume = Volume::mount(&mut reader, region).map_err(status)?;
        let file = volume.lookup(&mut reader, components).map_err(status)?;
        if file.directory {
            return Err(Status::LOAD_ERROR);
        }
        if file.bytes as usize > limit {
            return Err(Status::BAD_BUFFER_SIZE);
        }
        let mut bytes = Vec::new();
        bytes
            .try_reserve_exact(file.bytes as usize)
            .map_err(|_| Status::OUT_OF_RESOURCES)?;
        bytes.resize(file.bytes as usize, 0);
        volume
            .read_file(&mut reader, file, &mut bytes)
            .map_err(status)?;
        uefi::println!(
            "CMP40: RAW_FILE_READ bytes={} FAT={:?} block_bytes={} SFS_CALLS=0 io_calls={} blocks_read={}",
            bytes.len(),
            volume.kind,
            reader.block, reader.calls, reader.blocks_read
        );
        Ok(bytes)
    }
    pub fn device_path(&self) -> Result<&DevicePath, Status> {
        self.path
            .as_slice()
            .try_into()
            .map_err(|_| Status::UNSUPPORTED)
    }
}

/// Enumerate raw partition handles/superfloppies, then ESPs discovered inside
/// whole disks. Exact device paths deduplicate whole-disk and partition views.
/// Firmware-only volumes retain the previous loading path for unsupported FAT.
pub fn sources() -> Result<Vec<Source>, Status> {
    let handles = match boot::locate_handle_buffer(SearchType::ByProtocol(&BlockIO::GUID)) {
        Ok(handles) => Some(handles),
        Err(error) if error.status() == Status::NOT_FOUND => None,
        Err(error) => return Err(error.status()),
    };
    let mut result = Vec::new();
    if let Some(handles) = handles.as_ref() {
        for &handle in handles.iter() {
            let reader = match Reader::open(handle) {
                Ok(reader) => reader,
                Err(Status::NO_MEDIA | Status::UNSUPPORTED) => continue,
                Err(status) => return Err(status),
            };
            let path = copy_path(handle)?;
            let region = Some(Region {
                offset: 0,
                bytes: reader.bytes,
            });
            let firmware_filesystem = has_fs(handle)?;
            insert(
                &mut result,
                Source {
                    handle,
                    path,
                    region,
                    firmware_filesystem,
                },
            )?;
        }
        for &handle in handles.iter() {
            let mut reader = match Reader::open(handle) {
                Ok(reader) => reader,
                Err(Status::NO_MEDIA | Status::UNSUPPORTED) => continue,
                Err(status) => return Err(status),
            };
            if reader.io.media().is_logical_partition() {
                continue;
            }
            let region = Region {
                offset: 0,
                bytes: reader.bytes,
            };
            match Volume::mount(&mut reader, region) {
                Ok(_) => continue,
                Err(Error::Unsupported) => (),
                Err(error) => return Err(status(error)),
            }
            let block = reader.block as u32;
            let table = match Table::read(&mut reader, block) {
                Ok(table) => table,
                Err(Error::Unsupported) => continue,
                Err(error) => return Err(status(error)),
            };
            for index in 0..table.entries {
                if let Some(partition) = table.get(&mut reader, index).map_err(status)? {
                    let path = partition_path(handle, partition)?;
                    insert(
                        &mut result,
                        Source {
                            handle,
                            path,
                            region: Some(partition.region(block).ok_or(Status::VOLUME_CORRUPTED)?),
                            firmware_filesystem: false,
                        },
                    )?;
                }
            }
        }
    }
    match boot::locate_handle_buffer(SearchType::ByProtocol(&SimpleFileSystem::GUID)) {
        Ok(handles) => {
            for &handle in handles.iter() {
                insert(
                    &mut result,
                    Source {
                        handle,
                        path: copy_path(handle)?,
                        region: None,
                        firmware_filesystem: true,
                    },
                )?;
            }
        }
        Err(error) if error.status() == Status::NOT_FOUND => (),
        Err(error) => return Err(error.status()),
    }
    Ok(result)
}

fn insert(sources: &mut Vec<Source>, source: Source) -> Result<(), Status> {
    if let Some(existing) = sources
        .iter_mut()
        .find(|existing| existing.path == source.path)
    {
        existing.firmware_filesystem |= source.firmware_filesystem;
        return Ok(());
    }
    sources
        .try_reserve(1)
        .map_err(|_| Status::OUT_OF_RESOURCES)?;
    sources.push(source);
    Ok(())
}

fn copy_path(handle: Handle) -> Result<Vec<u8>, Status> {
    let path = protocol::<DevicePath>(handle)?;
    let path = path.get().ok_or(Status::UNSUPPORTED)?;
    if path.as_bytes().len() > 4096
        || path
            .node_iter()
            .any(|node| node.device_type() == DeviceType::END)
    {
        return Err(Status::UNSUPPORTED);
    }
    copy_bytes(path.as_bytes())
}
fn copy_bytes(bytes: &[u8]) -> Result<Vec<u8>, Status> {
    let mut result = Vec::new();
    result
        .try_reserve_exact(bytes.len())
        .map_err(|_| Status::OUT_OF_RESOURCES)?;
    result.extend_from_slice(bytes);
    Ok(result)
}
fn partition_path(handle: Handle, partition: Partition) -> Result<Vec<u8>, Status> {
    let path = protocol::<DevicePath>(handle)?;
    let path = path.get().ok_or(Status::UNSUPPORTED)?;
    let mut storage = [MaybeUninit::uninit(); 4096];
    let mut builder = build::DevicePathBuilder::with_buf(&mut storage);
    for node in path.node_iter() {
        if node.device_type() == DeviceType::END {
            return Err(Status::UNSUPPORTED);
        }
        builder = builder.push(&node).map_err(|_| Status::BAD_BUFFER_SIZE)?;
    }
    let (partition_format, partition_signature) = match partition.signature {
        Signature::Mbr(value) => (
            PartitionFormat::MBR,
            PartitionSignature::Mbr(value.to_le_bytes()),
        ),
        Signature::Gpt(bytes) => (
            PartitionFormat::GPT,
            PartitionSignature::Guid(Guid::from_bytes(bytes)),
        ),
    };
    let path = builder
        .push(&build::media::HardDrive {
            partition_number: partition.number,
            partition_start: partition.first_lba,
            partition_size: partition.blocks,
            partition_format,
            partition_signature,
        })
        .and_then(build::DevicePathBuilder::finalize)
        .map_err(|_| Status::BAD_BUFFER_SIZE)?;
    copy_bytes(path.as_bytes())
}
fn has_fs(handle: Handle) -> Result<bool, Status> {
    boot::test_protocol::<SimpleFileSystem>(boot::OpenProtocolParams {
        handle,
        agent: boot::image_handle(),
        controller: None,
    })
    .map_err(|error| error.status())
}
fn status(error: Error<Status>) -> Status {
    match error {
        Error::Io(status) => status,
        Error::Unsupported => Status::UNSUPPORTED,
        Error::NotFound => Status::NOT_FOUND,
        Error::Corrupt => Status::VOLUME_CORRUPTED,
        Error::InvalidInput => Status::INVALID_PARAMETER,
        Error::WrongSize => Status::BAD_BUFFER_SIZE,
    }
}
