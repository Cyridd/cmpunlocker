# SPDX-License-Identifier: MIT
"""Small local FAT16 image for QEMU file-write checks; no host block devices."""
import struct

SECTOR = 512
SECTORS = 65536
FAT_SECTORS = 64
ROOT_SECTOR = 1 + 2 * FAT_SECTORS
DATA_SECTOR = ROOT_SECTOR + 32
CLUSTER_BYTES = 4 * SECTOR


def short_name(name):
    parts = name.upper().split(".")
    stem, extension = parts[0], parts[1] if len(parts) == 2 else ""
    if len(parts) > 2 or not 1 <= len(stem) <= 8 or len(extension) > 3:
        raise ValueError(f"not an 8.3 fixture name: {name}")
    return (stem.ljust(8) + extension.ljust(3)).encode("ascii")


def names(name):
    try:
        return short_name(name), None
    except ValueError:
        stem, _, extension = name.upper().partition(".")
        return short_name(stem[:6] + "~1" + ("." + extension[:3] if extension else "")), name


def long_entries(name, short):
    checksum = 0
    for byte in short:
        checksum = (((checksum >> 1) | (checksum << 7)) + byte) & 255
    units = list(struct.unpack("<" + "H" * (len(name.encode("utf-16le")) // 2), name.encode("utf-16le"))) + [0]
    units += [0xffff] * (-len(units) % 13)
    output = []
    for part in reversed(range(len(units) // 13)):
        entry = bytearray(32)
        entry[0] = part + 1 | (0x40 if part + 1 == len(units) // 13 else 0)
        entry[11], entry[13] = 0x0f, checksum
        for index, offset in enumerate((1, 3, 5, 7, 9, 14, 16, 18, 20, 22, 24, 28, 30)):
            struct.pack_into("<H", entry, offset, units[part * 13 + index])
        output.append(entry)
    return output


class Fat16:
    def __init__(self, data=None):
        self.data = bytearray(data) if data is not None else bytearray(SECTOR * SECTORS)
        self.next_cluster = 2
        self.directories = {(): 0}
        self.used = {0: 0}
        if data is None:
            self.data[:11] = b"\xeb\x3c\x90CMPTEST "
            struct.pack_into("<HBHBHHBHHHII", self.data, 11,
                             SECTOR, 4, 1, 2, 512, 0, 0xf8, FAT_SECTORS, 32, 64, 0, SECTORS)
            struct.pack_into("<BBB I", self.data, 36, 0x80, 0, 0x29, 0x434d5034)
            self.data[43:54] = b"CMP40TEST  "
            self.data[54:62] = b"FAT16   "
            self.data[510:512] = b"\x55\xaa"
            self.fat(0, 0xfff8)
            self.fat(1, 0xffff)

    def fat(self, cluster, value=None):
        offset = SECTOR + cluster * 2
        if value is None:
            return struct.unpack_from("<H", self.data, offset)[0]
        struct.pack_into("<H", self.data, offset, value)
        struct.pack_into("<H", self.data, offset + FAT_SECTORS * SECTOR, value)

    def cluster_offset(self, cluster):
        return DATA_SECTOR * SECTOR + (cluster - 2) * CLUSTER_BYTES

    def allocate(self, payload):
        count = max(1, (len(payload) + CLUSTER_BYTES - 1) // CLUSTER_BYTES)
        first = self.next_cluster
        self.next_cluster += count
        if self.next_cluster > 2 + (SECTORS - DATA_SECTOR) // 4:
            raise ValueError("fixture disk full")
        for index in range(count):
            cluster = first + index
            self.fat(cluster, 0xffff if index + 1 == count else cluster + 1)
            chunk = payload[index * CLUSTER_BYTES:(index + 1) * CLUSTER_BYTES]
            offset = self.cluster_offset(cluster)
            self.data[offset:offset + len(chunk)] = chunk
        return first

    def entry(self, directory, name, cluster, size, attribute, long_name=None):
        count = self.used[directory]
        capacity = 512 if directory == 0 else CLUSTER_BYTES // 32
        records = long_entries(long_name, name) if long_name else []
        if count + len(records) >= capacity:
            raise ValueError("fixture directory full")
        offset = ROOT_SECTOR * SECTOR if directory == 0 else self.cluster_offset(directory)
        offset += count * 32
        for record in records:
            self.data[offset:offset + 32] = record
            offset += 32
        self.data[offset:offset + 11] = name
        self.data[offset + 11] = attribute
        struct.pack_into("<H", self.data, offset + 26, cluster)
        struct.pack_into("<I", self.data, offset + 28, size)
        self.used[directory] += len(records) + 1

    def add(self, filename, payload):
        parts = tuple(filename.upper().split("/"))
        for depth in range(1, len(parts)):
            path = parts[:depth]
            if path not in self.directories:
                parent = self.directories[path[:-1]]
                cluster = self.allocate(b"")
                self.used[cluster] = 0
                self.entry(cluster, b".          ", cluster, 0, 0x10)
                self.entry(cluster, b"..         ", parent, 0, 0x10)
                short, long_name = names(path[-1])
                self.entry(parent, short, cluster, 0, 0x10, long_name)
                self.directories[path] = cluster
        cluster = self.allocate(payload)
        short, long_name = names(parts[-1])
        self.entry(self.directories[parts[:-1]], short, cluster, len(payload), 0x20, long_name)

    def directory_entries(self, path=""):
        parts = path.upper().split("/") if path else []
        blocks = [bytes(self.data[ROOT_SECTOR * SECTOR:(ROOT_SECTOR + 32) * SECTOR])]
        for part in parts:
            entry = next((item for block in blocks for item in (block[i:i+32] for i in range(0, len(block), 32))
                          if item[:11] == short_name(part) and item[11] & 0x10), None)
            if entry is None: raise FileNotFoundError(path)
            cluster = struct.unpack_from("<H", entry, 26)[0]
            blocks, seen = [], set()
            while cluster < 0xfff8:
                if cluster < 2 or cluster in seen: raise ValueError("invalid directory chain")
                seen.add(cluster)
                offset = self.cluster_offset(cluster)
                blocks.append(bytes(self.data[offset:offset + CLUSTER_BYTES]))
                cluster = self.fat(cluster)
        result = []
        for block in blocks:
            for i in range(0, len(block), 32):
                entry = block[i:i+32]
                if entry[0] == 0: return result
                if entry[0] != 0xe5 and entry[11] != 0x0f:
                    result.append(entry)
        return result

    def read_root_file(self, filename):
        return self.read_file(filename)

    def read_file(self, filename):
        directory, _, basename = filename.rpartition("/")
        for entry in self.directory_entries(directory):
            if entry[:11] != short_name(basename): continue
            cluster = struct.unpack_from("<H", entry, 26)[0]
            size = struct.unpack_from("<I", entry, 28)[0]
            content, seen = bytearray(), set()
            while len(content) < size:
                if cluster < 2 or cluster >= 0xfff8 or cluster in seen:
                    raise ValueError("invalid fixture FAT chain")
                seen.add(cluster)
                location = self.cluster_offset(cluster)
                content += self.data[location:location + CLUSTER_BYTES]
                cluster = self.fat(cluster)
            return bytes(content[:size])
        raise FileNotFoundError(filename)
