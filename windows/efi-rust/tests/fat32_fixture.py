# SPDX-License-Identifier: MIT
"""Sparse GPT/FAT32 disk for QEMU; writes only a new local fixture file."""
import struct
import uuid
import zlib
from fat16_fixture import names, long_entries


class Fat32:
    def __init__(self, block_bytes=512):
        self.block = block_bytes
        self.total = 70000
        self.fat_blocks = (4 * (self.total + 2) + self.block - 1) // self.block
        self.data_block = 32 + 2 * self.fat_blocks
        self.next_cluster = 3
        self.fat = {0: 0x0ffffff8, 1: 0x0fffffff, 2: 0x0fffffff}
        self.content = {}
        self.directories = {(): 2}
        self.entries = {2: []}

    def allocate(self, data):
        count = max(1, (len(data) + self.block - 1) // self.block)
        first = self.next_cluster
        self.next_cluster += count
        for index in range(count):
            cluster = first + index
            self.fat[cluster] = 0x0fffffff if index + 1 == count else cluster + 1
            self.content[cluster] = data[index * self.block:(index + 1) * self.block]
        return first

    def entry(self, directory, short, cluster, size, attribute, long_name=None):
        records = long_entries(long_name, short) if long_name else []
        entry = bytearray(32)
        entry[:11], entry[11] = short, attribute
        struct.pack_into("<H", entry, 20, cluster >> 16)
        struct.pack_into("<H", entry, 26, cluster & 0xffff)
        struct.pack_into("<I", entry, 28, size)
        records.append(entry)
        if len(self.entries[directory]) + len(records) >= self.block // 32:
            raise ValueError("fixture directory full")
        self.entries[directory] += records

    def add(self, filename, payload):
        parts = tuple(filename.upper().split("/"))
        for depth in range(1, len(parts)):
            path = parts[:depth]
            if path not in self.directories:
                parent = self.directories[path[:-1]]
                cluster = self.allocate(b"")
                self.entries[cluster] = []
                self.entry(cluster, b".          ", cluster, 0, 0x10)
                self.entry(cluster, b"..         ", 0 if parent == 2 else parent, 0, 0x10)
                short, long_name = names(path[-1])
                self.entry(parent, short, cluster, 0, 0x10, long_name)
                self.directories[path] = cluster
        cluster = self.allocate(payload)
        short, long_name = names(parts[-1])
        self.entry(self.directories[parts[:-1]], short, cluster, len(payload), 0x20, long_name)

    def write_gpt(self, path):
        first, blocks = 2048, 2048 + self.total + 64
        entries = bytearray(128 * 128)
        entries[:16] = uuid.UUID("c12a7328-f81f-11d2-ba4b-00a0c93ec93b").bytes_le
        entries[16:32] = uuid.UUID("42424242-4242-4242-4242-424242424242").bytes_le
        struct.pack_into("<QQ", entries, 32, first, first + self.total - 1)
        table_blocks = (len(entries) + self.block - 1) // self.block
        backup_table = blocks - 1 - table_blocks

        def header(current, backup, table):
            data = bytearray(self.block)
            data[:8] = b"EFI PART"
            struct.pack_into("<IIIIQQQQ", data, 8, 0x10000, 92, 0, 0, current, backup, 34, backup_table - 1)
            data[56:72] = uuid.UUID("40404040-4040-4040-4040-404040404040").bytes_le
            struct.pack_into("<QIII", data, 72, table, 128, 128, zlib.crc32(entries))
            struct.pack_into("<I", data, 16, zlib.crc32(data[:92]))
            return data

        mbr = bytearray(512)
        mbr[450] = 0xee
        struct.pack_into("<II", mbr, 454, 1, blocks - 1)
        mbr[510:512] = b"\x55\xaa"
        boot = bytearray(512)
        boot[:11] = b"\xeb\x58\x90CMPTEST "
        struct.pack_into("<HBHBHHBHHHII", boot, 11, self.block, 1, 32, 2, 0, 0, 0xf8, 0, 32, 64, first, self.total)
        struct.pack_into("<IHHIHH", boot, 36, self.fat_blocks, 0, 0, 2, 1, 6)
        boot[64], boot[66] = 0x80, 0x29
        struct.pack_into("<I", boot, 67, 0x40404040)
        boot[71:82], boot[82:90], boot[510:512] = b"CMP40TEST  ", b"FAT32   ", b"\x55\xaa"
        fsinfo = bytearray(512)
        struct.pack_into("<I", fsinfo, 0, 0x41615252)
        struct.pack_into("<III", fsinfo, 484, 0x61417272, 0xffffffff, 0xffffffff)
        struct.pack_into("<I", fsinfo, 508, 0xaa550000)
        with path.open("xb") as output:
            output.truncate(blocks * self.block)

            def write(offset, data):
                output.seek(offset)
                output.write(data)

            write(0, mbr)
            write(self.block, header(1, blocks - 1, 2))
            write(2 * self.block, entries)
            write(backup_table * self.block, entries)
            write((blocks - 1) * self.block, header(blocks - 1, 1, backup_table))
            base = first * self.block
            write(base, boot)
            write(base + self.block, fsinfo)
            write(base + 6 * self.block, boot)
            for cluster, value in self.fat.items():
                for mirror in range(2):
                    write(base + (32 + mirror * self.fat_blocks) * self.block + cluster * 4, struct.pack("<I", value))
            for cluster, records in self.entries.items():
                self.content[cluster] = b"".join(records)
            for cluster, data in self.content.items():
                write(base + (self.data_block + cluster - 2) * self.block, data)
