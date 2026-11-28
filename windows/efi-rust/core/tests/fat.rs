// SPDX-License-Identifier: MIT
//! Sparse disk fixtures with independent on-disk structures, not host filesystems.
use cmp40_efi_core::fat::{Error, Kind, ReadAt, Region, Volume};
use cmp40_efi_core::partition::{crc32, Signature, Table};
use std::collections::BTreeMap;

#[derive(Default)]
struct Disk {
    sectors: BTreeMap<u64, [u8; 512]>,
    bytes: u64,
    fail: Option<u64>,
    failures: usize,
    reads: Vec<(u64, usize)>,
}
impl Disk {
    fn write(&mut self, offset: u64, bytes: &[u8]) {
        for (index, &byte) in bytes.iter().enumerate() {
            let position = offset + index as u64;
            self.sectors.entry(position / 512).or_insert([0; 512])[position as usize % 512] = byte;
        }
    }
}
impl ReadAt for Disk {
    type Error = u8;
    fn capacity(&self) -> u64 {
        self.bytes
    }
    fn read_at(&mut self, offset: u64, bytes: &mut [u8]) -> Result<(), u8> {
        self.reads.push((offset, bytes.len()));
        assert!(
            offset
                .checked_add(bytes.len() as u64)
                .is_some_and(|end| end <= self.bytes),
            "reader must reject out-of-range I/O before backend calls"
        );
        if self
            .fail
            .is_some_and(|fail| offset <= fail && fail < offset + bytes.len() as u64)
        {
            self.failures += 1;
            return Err(7);
        }
        for (index, byte) in bytes.iter_mut().enumerate() {
            let position = offset + index as u64;
            *byte = self
                .sectors
                .get(&(position / 512))
                .map_or(0, |sector| sector[position as usize % 512]);
        }
        Ok(())
    }
}
fn put16(bytes: &mut [u8], offset: usize, value: u16) {
    bytes[offset..offset + 2].copy_from_slice(&value.to_le_bytes());
}
fn put32(bytes: &mut [u8], offset: usize, value: u32) {
    bytes[offset..offset + 4].copy_from_slice(&value.to_le_bytes());
}
fn put64(bytes: &mut [u8], offset: usize, value: u64) {
    bytes[offset..offset + 8].copy_from_slice(&value.to_le_bytes());
}

struct Fat {
    disk: Disk,
    kind: Kind,
    region: Region,
    fat: u64,
    data: u64,
    root: u64,
    sector: u64,
}
impl Fat {
    fn new(kind: Kind, sector: u16, embedded: u64) -> Self {
        let reserved = if kind == Kind::Fat16 { 1 } else { 32 };
        let total = if kind == Kind::Fat16 { 20000 } else { 70000 };
        let fat_sectors = if kind == Kind::Fat16 { 80 } else { 600 };
        let fat_sectors = fat_sectors * 512 / u32::from(sector);
        let roots = if kind == Kind::Fat16 { 512 } else { 0 };
        let region = Region {
            offset: embedded,
            bytes: u64::from(total) * u64::from(sector),
        };
        let mut disk = Disk {
            bytes: region.offset + region.bytes + 4096,
            ..Disk::default()
        };
        let mut boot = [0; 512];
        boot[..3].copy_from_slice(&[0xeb, 0x58, 0x90]);
        put16(&mut boot, 11, sector);
        boot[13] = 1;
        put16(&mut boot, 14, reserved);
        boot[16] = 2;
        put16(&mut boot, 17, roots);
        put32(&mut boot, 32, total);
        boot[21] = 0xf8;
        if kind == Kind::Fat16 {
            put16(&mut boot, 22, fat_sectors as u16);
            boot[54..62].copy_from_slice(b"FAT16   ");
        } else {
            put32(&mut boot, 36, fat_sectors);
            put32(&mut boot, 44, 2);
            boot[82..90].copy_from_slice(b"FAT32   ");
        }
        boot[510..512].copy_from_slice(&[0x55, 0xaa]);
        disk.write(embedded, &boot);
        let root_sector = u64::from(reserved) + 2 * u64::from(fat_sectors);
        let data_sector = root_sector + (u64::from(roots) * 32).div_ceil(u64::from(sector));
        let mut result = Self {
            disk,
            kind,
            region,
            fat: embedded + u64::from(reserved) * u64::from(sector),
            data: embedded + data_sector * u64::from(sector),
            root: embedded + root_sector * u64::from(sector),
            sector: u64::from(sector),
        };
        if kind == Kind::Fat32 {
            result.link(2, None);
            result.root = result.cluster(2);
        }
        result
    }
    fn cluster(&self, cluster: u32) -> u64 {
        self.data + u64::from(cluster - 2) * self.sector
    }
    fn link(&mut self, cluster: u32, next: Option<u32>) {
        if self.kind == Kind::Fat16 {
            self.disk.write(
                self.fat + u64::from(cluster) * 2,
                &(next.unwrap_or(0xffff) as u16).to_le_bytes(),
            );
        } else {
            self.disk.write(
                self.fat + u64::from(cluster) * 4,
                &next.unwrap_or(0x0fff_ffff).to_le_bytes(),
            );
        }
    }
    fn entry(&mut self, offset: u64, short: &[u8; 11], cluster: u32, bytes: u32, directory: bool) {
        let mut raw = [0; 32];
        raw[..11].copy_from_slice(short);
        raw[11] = if directory { 0x10 } else { 0x20 };
        put16(&mut raw, 26, cluster as u16);
        put16(&mut raw, 20, (cluster >> 16) as u16);
        put32(&mut raw, 28, bytes);
        self.disk.write(offset, &raw);
    }
    fn long(&mut self, offset: u64, name: &str, short: &[u8; 11]) -> usize {
        let records = long_records(name, short);
        for (index, record) in records.iter().enumerate() {
            self.disk.write(offset + index as u64 * 32, record);
        }
        records.len() * 32
    }
    fn boot_file(&mut self) -> Vec<u8> {
        self.entry(self.root, b"EFI        ", 3, 0, true);
        self.link(3, None);
        let efi = self.cluster(3);
        let used = self.long(efi, "Microsoft", b"MICROS~1   ");
        self.entry(efi + used as u64, b"MICROS~1   ", 4, 0, true);
        self.link(4, None);
        self.entry(self.cluster(4), b"BOOT       ", 5, 0, true);
        self.link(5, None);
        let payload: Vec<_> = (0..1300).map(|index| (index % 251) as u8).collect();
        self.entry(
            self.cluster(5),
            b"BOOTMGFWEFI",
            9,
            payload.len() as u32,
            false,
        );
        self.link(9, Some(13));
        self.link(13, Some(6));
        self.link(6, None);
        for (cluster, bytes) in [9, 13, 6].into_iter().zip(payload.chunks(512)) {
            self.disk.write(self.cluster(cluster), bytes);
        }
        payload
    }
}
fn long_records(name: &str, short: &[u8; 11]) -> Vec<[u8; 32]> {
    let mut units: Vec<_> = name.encode_utf16().collect();
    units.push(0);
    while units.len() % 13 != 0 {
        units.push(0xffff);
    }
    let checksum = short
        .iter()
        .fold(0u8, |sum, &byte| sum.rotate_right(1).wrapping_add(byte));
    let mut records = Vec::new();
    for part in (0..units.len() / 13).rev() {
        let mut raw = [0; 32];
        raw[0] = (part + 1) as u8
            | if part + 1 == units.len() / 13 {
                0x40
            } else {
                0
            };
        raw[11] = 0x0f;
        raw[13] = checksum;
        for (index, offset) in [1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30]
            .into_iter()
            .enumerate()
        {
            put16(&mut raw, offset, units[part * 13 + index]);
        }
        records.push(raw);
    }
    records
}

#[test]
fn fat16_and_fat32_read_fragmented_windows_path_with_lfn_and_partial_tail() {
    for kind in [Kind::Fat16, Kind::Fat32] {
        let mut fixture = Fat::new(kind, 512, 4096);
        let expected = fixture.boot_file();
        let volume = Volume::mount(&mut fixture.disk, fixture.region).unwrap();
        assert_eq!(volume.kind, kind);
        let file = volume
            .lookup(
                &mut fixture.disk,
                &["efi", "mICROSOFT", "Boot", "bootmgfw.efi"],
            )
            .unwrap();
        let mut output = vec![0xa5; expected.len()];
        volume
            .read_file(&mut fixture.disk, file, &mut output)
            .unwrap();
        assert_eq!(output, expected);
    }
}

#[test]
fn contiguous_clusters_batch_data_and_stop_at_a_failed_batch() {
    for (kind, sector) in [(Kind::Fat16, 512), (Kind::Fat32, 512), (Kind::Fat32, 4096)] {
        let mut fixture = Fat::new(kind, sector, 4096);
        let payload: Vec<_> = (0..70013).map(|i| (i % 251) as u8).collect();
        fixture.entry(
            fixture.root,
            b"TEST    EFI",
            10,
            payload.len() as u32,
            false,
        );
        let count = payload.len().div_ceil(sector as usize);
        for (i, bytes) in payload.chunks(sector as usize).enumerate() {
            let cluster = 10 + i as u32;
            fixture.link(cluster, (i + 1 < count).then_some(cluster + 1));
            fixture.disk.write(fixture.cluster(cluster), bytes);
        }
        let volume = Volume::mount(&mut fixture.disk, fixture.region).unwrap();
        let entry = volume.lookup(&mut fixture.disk, &["TEST.EFI"]).unwrap();
        let first = fixture.cluster(10);
        fixture.disk.reads.clear();
        let mut output = vec![0xa5; payload.len()];
        volume
            .read_file(&mut fixture.disk, entry, &mut output)
            .unwrap();
        assert_eq!(output, payload);
        let data_reads: Vec<_> = fixture
            .disk
            .reads
            .iter()
            .filter(|(offset, _)| *offset >= first)
            .collect();
        assert_eq!(data_reads.len(), 2);
        assert_eq!(data_reads[0].1, 65536);
        fixture.disk.fail = Some(first + 65536);
        fixture.disk.failures = 0;
        output.fill(0xa5);
        assert_eq!(
            volume.read_file(&mut fixture.disk, entry, &mut output),
            Err(Error::Io(7))
        );
        assert_eq!(fixture.disk.failures, 1);
        assert_eq!(&output[..65536], &payload[..65536]);
        assert!(output[65536..].iter().all(|&byte| byte == 0xa5));
    }
}

#[test]
fn fat32_lfn_survives_directory_cluster_boundary_and_requires_checksum() {
    let mut fixture = Fat::new(Kind::Fat32, 512, 0);
    let records = long_records("MicrosoftLongName", b"MICROS~1   ");
    assert_eq!(records.len(), 2);
    for index in 0..15 {
        fixture.disk.write(fixture.root + index * 32, &[0xe5]);
    }
    fixture.disk.write(fixture.root + 15 * 32, &records[0]);
    fixture.link(2, Some(20));
    fixture.link(20, None);
    fixture.disk.write(fixture.cluster(20), &records[1]);
    fixture.entry(fixture.cluster(20) + 32, b"MICROS~1   ", 8, 1, false);
    fixture.link(8, None);
    let volume = Volume::mount(&mut fixture.disk, fixture.region).unwrap();
    assert_eq!(
        volume
            .lookup(&mut fixture.disk, &["MicrosoftLongName"])
            .unwrap()
            .cluster,
        8
    );
    fixture.disk.write(fixture.cluster(20) + 13, &[0]);
    assert_eq!(
        volume.lookup(&mut fixture.disk, &["MicrosoftLongName"]),
        Err(Error::NotFound)
    );
    assert!(volume.lookup(&mut fixture.disk, &["MICROS~1"]).is_ok());
}

#[test]
fn corrupt_file_chains_refuse_before_content_stores() {
    for next in [None, Some(9), Some(0x200000), Some(0)] {
        let mut fixture = Fat::new(Kind::Fat32, 512, 0);
        fixture.boot_file();
        fixture.link(9, next);
        let volume = Volume::mount(&mut fixture.disk, fixture.region).unwrap();
        let file = volume
            .lookup(
                &mut fixture.disk,
                &["EFI", "Microsoft", "Boot", "bootmgfw.efi"],
            )
            .unwrap();
        let mut output = vec![0xa5; 1300];
        assert_eq!(
            volume.read_file(&mut fixture.disk, file, &mut output),
            Err(Error::Corrupt)
        );
        assert!(output.iter().all(|&byte| byte == 0xa5));
    }
}

#[test]
fn directory_cycle_is_bounded_and_not_reported_as_missing_file() {
    let mut fixture = Fat::new(Kind::Fat32, 512, 0);
    fixture.link(2, Some(20));
    fixture.link(20, Some(2));
    for cluster in [2, 20] {
        for index in 0..16 {
            fixture
                .disk
                .write(fixture.cluster(cluster) + index * 32, &[0xe5]);
        }
    }
    let volume = Volume::mount(&mut fixture.disk, fixture.region).unwrap();
    assert_eq!(
        volume.lookup(&mut fixture.disk, &["absent"]),
        Err(Error::Corrupt)
    );
}

#[test]
fn fat32_active_fat_selection_and_4096_byte_sectors() {
    let mut fixture = Fat::new(Kind::Fat32, 4096, 4096);
    fixture.link(2, Some(2));
    fixture
        .disk
        .write(fixture.region.offset + 40, &0x81u16.to_le_bytes());
    let second_fat = fixture.fat + 75 * 4096;
    fixture
        .disk
        .write(second_fat + 8, &0x0fff_ffffu32.to_le_bytes());
    fixture.entry(fixture.root, b"FILE    TXT", 8, 5, false);
    fixture
        .disk
        .write(second_fat + 32, &0x0fff_ffffu32.to_le_bytes());
    fixture.disk.write(fixture.cluster(8), b"hello");
    let volume = Volume::mount(&mut fixture.disk, fixture.region).unwrap();
    let file = volume.lookup(&mut fixture.disk, &["file.txt"]).unwrap();
    let mut bytes = [0; 5];
    volume
        .read_file(&mut fixture.disk, file, &mut bytes)
        .unwrap();
    assert_eq!(&bytes, b"hello");
}

#[test]
fn fat_geometry_and_region_bounds_are_checked_before_backend_reads() {
    for (offset, bytes) in [
        (11, vec![0, 0]),
        (13, vec![3]),
        (36, vec![0, 0, 0, 0]),
        (44, vec![1, 0, 0, 0]),
    ] {
        let mut fixture = Fat::new(Kind::Fat32, 512, 0);
        fixture.disk.write(offset, &bytes);
        assert!(matches!(
            Volume::mount(&mut fixture.disk, fixture.region),
            Err(Error::Corrupt)
        ));
    }
    let mut fixture = Fat::new(Kind::Fat32, 512, 0);
    let region = Region {
        bytes: 4096,
        ..fixture.region
    };
    assert!(matches!(
        Volume::mount(&mut fixture.disk, region),
        Err(Error::Corrupt)
    ));
    let mut untouched = [0xa5; 4];
    assert_eq!(
        Region {
            offset: u64::MAX - 2,
            bytes: 8
        }
        .read(&mut fixture.disk, 0, &mut untouched),
        Err(Error::Corrupt)
    );
    assert_eq!(untouched, [0xa5; 4]);
}

#[test]
fn file_io_errors_are_propagated_once_without_retry() {
    let mut fixture = Fat::new(Kind::Fat32, 512, 0);
    fixture.boot_file();
    let volume = Volume::mount(&mut fixture.disk, fixture.region).unwrap();
    let file = volume
        .lookup(
            &mut fixture.disk,
            &["EFI", "Microsoft", "Boot", "bootmgfw.efi"],
        )
        .unwrap();
    fixture.disk.fail = Some(fixture.cluster(9));
    assert_eq!(
        volume.read_file(&mut fixture.disk, file, &mut vec![0; 1300]),
        Err(Error::Io(7))
    );
    assert_eq!(fixture.disk.failures, 1);
}

fn gpt(block: u32) -> Disk {
    let mut disk = Disk {
        bytes: u64::from(block) * 100000,
        ..Disk::default()
    };
    let mut mbr = [0; 512];
    mbr[450] = 0xee;
    mbr[510..512].copy_from_slice(&[0x55, 0xaa]);
    disk.write(0, &mbr);
    let mut entries = [0; 512];
    entries[..16].copy_from_slice(&[
        0x28, 0x73, 0x2a, 0xc1, 0x1f, 0xf8, 0xd2, 0x11, 0xba, 0x4b, 0, 0xa0, 0xc9, 0x3e, 0xc9, 0x3b,
    ]);
    entries[16..32].fill(0x42);
    put64(&mut entries, 32, 2048);
    put64(&mut entries, 40, 4095);
    disk.write(u64::from(block) * 2, &entries);
    let mut header = [0; 512];
    header[..8].copy_from_slice(b"EFI PART");
    put32(&mut header, 8, 0x10000);
    put32(&mut header, 12, 92);
    put64(&mut header, 24, 1);
    put64(&mut header, 32, 99999);
    put64(&mut header, 40, 34);
    put64(&mut header, 48, 99966);
    put64(&mut header, 72, 2);
    put32(&mut header, 80, 4);
    put32(&mut header, 84, 128);
    put32(&mut header, 88, crc32(&entries));
    let digest = crc32(&header[..92]);
    put32(&mut header, 16, digest);
    disk.write(u64::from(block), &header);
    disk
}

#[test]
fn gpt_esp_crc_and_native_block_size_are_preserved() {
    assert_eq!(crc32(b"123456789"), 0xcbf4_3926);
    for block in [512, 4096] {
        let mut disk = gpt(block);
        let table = Table::read(&mut disk, block).unwrap();
        let part = table.get(&mut disk, 0).unwrap().unwrap();
        assert_eq!(part.signature, Signature::Gpt([0x42; 16]));
        assert_eq!((part.number, part.first_lba, part.blocks), (1, 2048, 2048));
        assert_eq!(part.region(block).unwrap().offset, 2048 * u64::from(block));
        assert_eq!(table.get(&mut disk, 1).unwrap(), None);
    }
}

#[test]
fn gpt_header_and_entry_corruption_never_bypass_crc_validation() {
    for offset in [512 + 56, 1024 + 16] {
        let mut disk = gpt(512);
        disk.write(offset, &[0x77]);
        assert!(matches!(Table::read(&mut disk, 512), Err(Error::Corrupt)));
    }
}

#[test]
fn larger_gpt_layouts_are_unsupported_without_reading_unbounded_tables() {
    for (entries, stride) in [(129, 128), (4, 2048)] {
        let mut disk = gpt(512);
        let mut header = [0; 512];
        disk.read_at(512, &mut header).unwrap();
        put32(&mut header, 80, entries);
        put32(&mut header, 84, stride);
        put32(&mut header, 16, 0);
        let digest = crc32(&header[..92]);
        put32(&mut header, 16, digest);
        disk.write(512, &header);
        disk.fail = Some(1024);
        assert!(matches!(
            Table::read(&mut disk, 512),
            Err(Error::Unsupported)
        ));
        assert_eq!(disk.failures, 0);
    }
}

#[test]
fn mbr_primary_fat_partition_and_bounds() {
    let mut disk = Disk {
        bytes: 512 * 100000,
        ..Disk::default()
    };
    let mut mbr = [0; 512];
    put32(&mut mbr, 440, 0x12345678);
    mbr[450] = 0x0c;
    put32(&mut mbr, 454, 2048);
    put32(&mut mbr, 458, 20000);
    mbr[510..512].copy_from_slice(&[0x55, 0xaa]);
    disk.write(0, &mbr);
    let table = Table::read(&mut disk, 512).unwrap();
    let part = table.get(&mut disk, 0).unwrap().unwrap();
    assert_eq!(part.signature, Signature::Mbr(0x12345678));
    assert_eq!(part.blocks, 20000);
    disk.write(458, &100000u32.to_le_bytes());
    assert_eq!(table.get(&mut disk, 0), Err(Error::Corrupt));
    assert_eq!(table.get(&mut disk, 4), Err(Error::InvalidInput));
}
