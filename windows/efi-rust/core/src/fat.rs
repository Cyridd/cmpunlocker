// SPDX-License-Identifier: MIT
//! Bounded, read-only FAT16/FAT32 access. No allocation, firmware or disk writes.

pub trait ReadAt {
    type Error;
    fn capacity(&self) -> u64;
    fn read_at(&mut self, offset: u64, bytes: &mut [u8]) -> Result<(), Self::Error>;
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Error<E> {
    Io(E),
    Unsupported,
    Corrupt,
    NotFound,
    InvalidInput,
    WrongSize,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Region {
    pub offset: u64,
    pub bytes: u64,
}

impl Region {
    pub fn read<R: ReadAt>(
        self,
        io: &mut R,
        offset: u64,
        bytes: &mut [u8],
    ) -> Result<(), Error<R::Error>> {
        let end = offset
            .checked_add(bytes.len() as u64)
            .ok_or(Error::Corrupt)?;
        let absolute = self.offset.checked_add(offset).ok_or(Error::Corrupt)?;
        if end > self.bytes
            || self
                .offset
                .checked_add(self.bytes)
                .is_none_or(|end| end > io.capacity())
        {
            return Err(Error::Corrupt);
        }
        io.read_at(absolute, bytes).map_err(Error::Io)
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Kind {
    Fat16,
    Fat32,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct Entry {
    pub cluster: u32,
    pub bytes: u32,
    pub directory: bool,
}

#[derive(Clone, Copy, Debug)]
pub struct Volume {
    region: Region,
    pub kind: Kind,
    fat_offset: u64,
    data_offset: u64,
    cluster_bytes: u64,
    clusters: u32,
    root: u32,
    root_offset: u64,
    root_bytes: u64,
}

impl Volume {
    pub fn mount<R: ReadAt>(io: &mut R, region: Region) -> Result<Self, Error<R::Error>> {
        let mut bpb = [0; 512];
        region.read(io, 0, &mut bpb)?;
        let claims_fat = &bpb[54..62] == b"FAT16   " || &bpb[82..90] == b"FAT32   ";
        let invalid = || {
            if claims_fat {
                Error::Corrupt
            } else {
                Error::Unsupported
            }
        };
        let sector = u64::from(le16(&bpb, 11));
        let per_cluster = u64::from(bpb[13]);
        let reserved = u64::from(le16(&bpb, 14));
        let fats = u64::from(bpb[16]);
        let root_entries = u64::from(le16(&bpb, 17));
        let total = if le16(&bpb, 19) != 0 {
            u64::from(le16(&bpb, 19))
        } else {
            u64::from(le32(&bpb, 32))
        };
        let fat16 = le16(&bpb, 22);
        let fat_sectors = if fat16 != 0 {
            u64::from(fat16)
        } else {
            u64::from(le32(&bpb, 36))
        };
        if bpb[510..512] != [0x55, 0xaa]
            || !(512..=4096).contains(&sector)
            || !sector.is_power_of_two()
            || per_cluster == 0
            || per_cluster > 128
            || !per_cluster.is_power_of_two()
            || reserved == 0
            || !(1..=4).contains(&fats)
            || fat_sectors == 0
            || total == 0
        {
            return Err(invalid());
        }
        let root_sectors = (root_entries * 32).div_ceil(sector);
        let first_data = reserved + fats * fat_sectors + root_sectors;
        if first_data >= total || total * sector > region.bytes {
            return Err(Error::Corrupt);
        }
        let clusters = (total - first_data) / per_cluster;
        if clusters < 4085 {
            return Err(Error::Unsupported);
        } // FAT12 is not part of the C carrier.
        let kind = if clusters < 65525 {
            Kind::Fat16
        } else {
            Kind::Fat32
        };
        let width = if kind == Kind::Fat16 { 2 } else { 4 };
        if clusters > 0x0fff_ffed || (clusters + 2) * width > fat_sectors * sector {
            return Err(Error::Corrupt);
        }
        let mut active = 0;
        let root = if kind == Kind::Fat32 {
            if fat16 != 0 || root_entries != 0 || le16(&bpb, 42) != 0 {
                return Err(Error::Corrupt);
            }
            let flags = le16(&bpb, 40);
            if flags & 0x80 != 0 {
                active = u64::from(flags & 15);
                if active >= fats {
                    return Err(Error::Corrupt);
                }
            }
            le32(&bpb, 44) & 0x0fff_ffff
        } else {
            if fat16 == 0 || root_entries == 0 || (root_entries * 32) % sector != 0 {
                return Err(Error::Corrupt);
            }
            0
        };
        let volume = Self {
            region: Region {
                offset: region.offset,
                bytes: total * sector,
            },
            kind,
            fat_offset: (reserved + active * fat_sectors) * sector,
            data_offset: first_data * sector,
            cluster_bytes: per_cluster * sector,
            clusters: clusters as u32,
            root,
            root_offset: (reserved + fats * fat_sectors) * sector,
            root_bytes: root_entries * 32,
        };
        if kind == Kind::Fat32 && !volume.valid_cluster(root) {
            return Err(Error::Corrupt);
        }
        Ok(volume)
    }

    pub fn lookup<R: ReadAt>(
        self,
        io: &mut R,
        components: &[&str],
    ) -> Result<Entry, Error<R::Error>> {
        if components.is_empty()
            || components.iter().any(|name| {
                name.is_empty()
                    || *name == "."
                    || *name == ".."
                    || name.contains(['/', '\\', '\0'])
                    || name.encode_utf16().count() > 255
            })
        {
            return Err(Error::InvalidInput);
        }
        let mut directory = self.root;
        for (index, name) in components.iter().enumerate() {
            let entry = self.find(io, directory, name)?;
            if index + 1 == components.len() {
                return Ok(entry);
            }
            if !entry.directory {
                return Err(Error::NotFound);
            }
            directory = entry.cluster;
        }
        Err(Error::NotFound)
    }

    fn valid_cluster(self, cluster: u32) -> bool {
        let reserved = if self.kind == Kind::Fat16 {
            0xfff0
        } else {
            0x0fff_fff0
        };
        cluster >= 2 && cluster < self.clusters + 2 && cluster < reserved
    }

    fn cluster_offset<E>(self, cluster: u32) -> Result<u64, Error<E>> {
        if !self.valid_cluster(cluster) {
            return Err(Error::Corrupt);
        }
        Ok(self.data_offset + u64::from(cluster - 2) * self.cluster_bytes)
    }

    fn next<R: ReadAt>(self, io: &mut R, cluster: u32) -> Result<Option<u32>, Error<R::Error>> {
        self.cluster_offset::<R::Error>(cluster)?;
        let mut entry = [0; 4];
        let (width, end) = if self.kind == Kind::Fat16 {
            (2, 0xfff8)
        } else {
            (4, 0x0fff_fff8)
        };
        self.region.read(
            io,
            self.fat_offset + u64::from(cluster) * width,
            &mut entry[..width as usize],
        )?;
        let value = if width == 2 {
            u32::from(le16(&entry, 0))
        } else {
            le32(&entry, 0) & 0x0fff_ffff
        };
        if value >= end {
            Ok(None)
        } else if self.valid_cluster(value) {
            Ok(Some(value))
        } else {
            Err(Error::Corrupt)
        }
    }

    fn find<R: ReadAt>(
        self,
        io: &mut R,
        directory: u32,
        name: &str,
    ) -> Result<Entry, Error<R::Error>> {
        let fixed = self.kind == Kind::Fat16 && directory == 0;
        let mut cluster = directory;
        let mut cycle = Cycle::new(directory);
        let mut lfn = LongName::new();
        loop {
            let (base, bytes) = if fixed {
                (self.root_offset, self.root_bytes)
            } else {
                (
                    self.cluster_offset::<R::Error>(cluster)?,
                    self.cluster_bytes,
                )
            };
            let mut offset = 0;
            let mut sector = [0; 512];
            while offset < bytes {
                self.region.read(io, base + offset, &mut sector)?;
                for raw in sector.chunks_exact(32) {
                    if raw[0] == 0 {
                        return Err(Error::NotFound);
                    }
                    if raw[0] == 0xe5 {
                        lfn.reset();
                        continue;
                    }
                    if raw[11] == 0x0f {
                        lfn.push(raw);
                        continue;
                    }
                    let matches =
                        raw[11] & 0x08 == 0 && (lfn.matches(raw, name) || short_matches(raw, name));
                    lfn.reset();
                    if !matches {
                        continue;
                    }
                    let cluster = u32::from(le16(raw, 26))
                        | if self.kind == Kind::Fat32 {
                            u32::from(le16(raw, 20)) << 16
                        } else {
                            0
                        };
                    let entry = Entry {
                        cluster,
                        bytes: le32(raw, 28),
                        directory: raw[11] & 0x10 != 0,
                    };
                    if (entry.directory || entry.bytes != 0) && !self.valid_cluster(entry.cluster) {
                        return Err(Error::Corrupt);
                    }
                    return Ok(entry);
                }
                offset += 512;
            }
            if fixed {
                return Err(Error::NotFound);
            }
            let Some(next) = self.next(io, cluster)? else {
                return Err(Error::NotFound);
            };
            cycle.advance(next)?;
            cluster = next;
        }
    }

    /// Validate the complete, size-consistent cluster chain before reading
    /// file contents. An I/O failure during the second pass may leave partial
    /// output; callers must discard it. No same-call retry is performed.
    pub fn read_file<R: ReadAt>(
        self,
        io: &mut R,
        entry: Entry,
        output: &mut [u8],
    ) -> Result<(), Error<R::Error>> {
        if entry.directory || output.len() != entry.bytes as usize {
            return Err(Error::WrongSize);
        }
        if entry.bytes == 0 {
            return Ok(());
        }
        let count = u64::from(entry.bytes).div_ceil(self.cluster_bytes);
        if count > u64::from(self.clusters) {
            return Err(Error::Corrupt);
        }
        let mut cluster = entry.cluster;
        let mut cycle = Cycle::new(cluster);
        for index in 0..count {
            let next = self.next(io, cluster)?;
            if index + 1 == count {
                if next.is_some() {
                    return Err(Error::Corrupt);
                }
            } else {
                cluster = next.ok_or(Error::Corrupt)?;
                cycle.advance(cluster)?;
            }
        }
        cluster = entry.cluster;
        let mut done = 0;
        while done < output.len() {
            let take = (self.cluster_bytes as usize).min(output.len() - done);
            self.region.read(
                io,
                self.cluster_offset::<R::Error>(cluster)?,
                &mut output[done..done + take],
            )?;
            done += take;
            if done < output.len() {
                cluster = self.next(io, cluster)?.ok_or(Error::Corrupt)?;
            }
        }
        Ok(())
    }
}

struct Cycle {
    anchor: u32,
    power: u32,
    span: u32,
    steps: u32,
}
impl Cycle {
    fn new(anchor: u32) -> Self {
        Self {
            anchor,
            power: 1,
            span: 0,
            steps: 0,
        }
    }
    fn advance<E>(&mut self, next: u32) -> Result<(), Error<E>> {
        self.steps += 1;
        if next == self.anchor || self.steps > 65536 {
            return Err(Error::Corrupt);
        }
        self.span += 1;
        if self.span == self.power {
            self.anchor = next;
            self.span = 0;
            self.power *= 2;
        }
        Ok(())
    }
}

struct LongName {
    units: [u16; 260],
    expected: u8,
    checksum: u8,
    active: bool,
}
impl LongName {
    fn new() -> Self {
        Self {
            units: [0xffff; 260],
            expected: 0,
            checksum: 0,
            active: false,
        }
    }
    fn reset(&mut self) {
        self.active = false;
    }
    fn push(&mut self, entry: &[u8]) {
        let sequence = entry[0] & 0x1f;
        if sequence == 0
            || sequence > 20
            || entry[0] & 0xa0 != 0
            || entry[12] != 0
            || le16(entry, 26) != 0
        {
            self.reset();
            return;
        }
        if entry[0] & 0x40 != 0 {
            self.units.fill(0xffff);
            self.expected = sequence;
            self.checksum = entry[13];
            self.active = true;
        }
        if !self.active || sequence != self.expected || entry[13] != self.checksum {
            self.reset();
            return;
        }
        for (index, offset) in [1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30]
            .into_iter()
            .enumerate()
        {
            self.units[(usize::from(sequence) - 1) * 13 + index] = le16(entry, offset);
        }
        self.expected -= 1;
    }
    fn matches(&self, short: &[u8], name: &str) -> bool {
        if !self.active || self.expected != 0 || self.checksum != name_checksum(&short[..11]) {
            return false;
        }
        let length = self
            .units
            .iter()
            .position(|&unit| unit == 0 || unit == 0xffff)
            .unwrap_or(260);
        length <= 255 && units_match(&self.units[..length], name)
    }
}

fn short_matches(entry: &[u8], name: &str) -> bool {
    let mut units = [0; 12];
    let mut used = 0;
    for (index, &byte) in entry[..8].iter().enumerate() {
        if byte == b' ' {
            break;
        }
        units[used] = u16::from(if index == 0 && byte == 5 { 0xe5 } else { byte });
        used += 1;
    }
    if entry[8] != b' ' {
        units[used] = u16::from(b'.');
        used += 1;
        for &byte in &entry[8..11] {
            if byte == b' ' {
                break;
            }
            units[used] = u16::from(byte);
            used += 1;
        }
    }
    units_match(&units[..used], name)
}
fn units_match(units: &[u16], name: &str) -> bool {
    let upper = |unit| {
        if (0x61..=0x7a).contains(&unit) {
            unit - 32
        } else {
            unit
        }
    };
    units.len() == name.encode_utf16().count()
        && units
            .iter()
            .copied()
            .map(upper)
            .eq(name.encode_utf16().map(upper))
}
pub fn name_checksum(short: &[u8]) -> u8 {
    short
        .iter()
        .fold(0u8, |value, &byte| value.rotate_right(1).wrapping_add(byte))
}
pub(crate) fn le16(bytes: &[u8], offset: usize) -> u16 {
    u16::from_le_bytes(bytes[offset..offset + 2].try_into().unwrap())
}
pub(crate) fn le32(bytes: &[u8], offset: usize) -> u32 {
    u32::from_le_bytes(bytes[offset..offset + 4].try_into().unwrap())
}
pub(crate) fn le64(bytes: &[u8], offset: usize) -> u64 {
    u64::from_le_bytes(bytes[offset..offset + 8].try_into().unwrap())
}
