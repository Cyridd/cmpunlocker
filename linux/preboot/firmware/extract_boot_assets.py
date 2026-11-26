#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Build the TU102 Booter and GSP boot assets from the pinned 610 generated source."""
from __future__ import annotations

import argparse
import hashlib
import json
import zlib
import re
import struct
import sys
from pathlib import Path

MANIFEST = Path(__file__).with_name("artifacts-610.43.03.json")


MAGIC = 0x314C5442
LABELS = (
    "IMAGE_PROD",
    "HEADER_PROD",
    "SIG_PROD",
    "PATCH_LOC",
    "PATCH_META",
    "NUM_SIGS",
)


def _array_bytes(text: str, label: str) -> tuple[bytes, bool, int]:
    marker = f'BINDATA_LABEL_{label}_data[]'
    if text.count(marker) != 1:
        raise ValueError(f"missing or duplicate {label}")
    start = text.index(marker)
    window = text[max(0, start - 400):start]
    compression = re.findall(r"COMPRESSION: (YES|NO)", window)
    sizes = re.findall(r"DATA SIZE \(bytes\): (\d+)", window)
    if not compression or not sizes:
        raise ValueError(f"missing storage declaration for {label}")
    expected = int(sizes[-1]); compressed = compression[-1] == 'YES'
    if not 0 < expected <= 16 * 1024 * 1024:
        raise ValueError('asset exceeds extraction limit')
    declaration = re.match(re.escape(marker) + r"\s*=\s*\{([^}]*)\};", text[start:], re.S)
    if not declaration:
        raise ValueError('malformed byte-array declaration')
    tokens = declaration[1].strip().rstrip(',').split(',')
    if any(not re.fullmatch(r"\s*0x[0-9a-fA-F]{2}\s*", x) for x in tokens):
        raise ValueError('non-byte token in generated array')
    raw = bytes(int(item, 16) for item in tokens)
    if compressed:
        stored = re.findall(r"COMPRESSED SIZE \(bytes\): (\d+)", window)
        if not stored or len(raw) != int(stored[-1]):
            raise ValueError('compressed storage size mismatch')
        inflater = zlib.decompressobj(-15)
        data = inflater.decompress(raw, expected + 1)
        # NVIDIA BINDATA stores raw DEFLATE followed by the gzip CRC32/ISIZE
        # trailer (the gzip header is omitted). Validate both trailer fields.
        if not inflater.eof or len(inflater.unused_data) != 8 or inflater.unconsumed_tail:
            raise ValueError('truncated, trailing or oversized compressed stream')
        crc, size = struct.unpack('<II', inflater.unused_data)
        if size != expected or crc != zlib.crc32(data):
            raise ValueError('compressed checksum or size mismatch')
    else:
        data = raw
    if len(data) != expected:
        raise ValueError(f"{label}: inflated {len(data)} bytes, header says {expected}")
    return data, compressed, expected


def build_bundle(source_text: str, signature_size: int = 384) -> bytes:
    image, _, _ = _array_bytes(source_text, "IMAGE_PROD")
    header, _, _ = _array_bytes(source_text, "HEADER_PROD")
    signatures, _, _ = _array_bytes(source_text, "SIG_PROD")
    patch_loc, _, _ = _array_bytes(source_text, "PATCH_LOC")
    patch_meta, _, _ = _array_bytes(source_text, "PATCH_META")
    num_sigs, _, _ = _array_bytes(source_text, "NUM_SIGS")
    if len(header) != 36 or len(patch_loc) != 4 or len(patch_meta) != 12 or len(num_sigs) != 4:
        raise ValueError("unexpected BooterLoad record size")
    fields = struct.unpack("<9I", header)
    loc = struct.unpack("<I", patch_loc)[0]
    _fuse, engine, ucode = struct.unpack("<3I", patch_meta)
    count = struct.unpack("<I", num_sigs)[0]
    if not count or len(signatures) != count * signature_size or fields[4] != 1 or not ucode:
        raise ValueError('unsupported production Booter signature/application table')
    if loc < fields[2] or loc + signature_size > fields[2] + fields[3] or fields[2] + fields[3] > len(image):
        raise ValueError('signature patch is outside DMEM image')
    for offset, size in ((fields[0],fields[1]), (fields[2],fields[3]), (fields[5],fields[6])):
        if offset + size > len(image) or offset % 256 or size % 256:
            raise ValueError('invalid Falcon transfer range')
    hdr = struct.pack(
        "<14I",
        fields[0], fields[1], fields[2], fields[3], fields[4], fields[5], fields[6],
        fields[7], fields[8], loc, ucode, engine, count, len(signatures),
    )
    prefix = struct.pack("<II", MAGIC, 1) + hdr + struct.pack("<II", len(image), len(signatures))
    return prefix + image + signatures


PINNED = {
    'g_bindata_kgspGetBinArchiveBooterLoadUcode_TU102.c': 'e495483adf66798502667da137c4a24c55f471b86f3183020e3169ff88ad6c89',
    'g_bindata_kgspGetBinArchiveBooterUnloadUcode_TU102.c': '043a2677456f613d7fa97b636a06d92950b357d2d3d09376af056b22b4830692',
    'g_bindata_kgspGetBinArchiveGspRmBoot_TU102.c': '19628e0b148a5437b351ce9ecd4a04405ec4eb66521e1c318ca6ef5d1a0c2efe',
    'g_bindata_ksec2GetBinArchiveBlUcode_TU102.c': 'fc0bc277eeee754ea3071ae0fe8360a52193565bba7c07dcea60b00e8d56d9c8',
}


def extract_assets(source_root: Path, output: Path) -> None:
    inputs = {}
    for name, digest in PINNED.items():
        data = (source_root / 'src/nvidia/generated' / name).read_bytes()
        if hashlib.sha256(data).hexdigest() != digest:
            raise ValueError('pinned generated source mismatch: ' + name)
        inputs[name] = data.decode('ascii')
    booter = build_bundle(inputs['g_bindata_kgspGetBinArchiveBooterLoadUcode_TU102.c'], 16)
    unload = build_bundle(inputs['g_bindata_kgspGetBinArchiveBooterUnloadUcode_TU102.c'], 16)
    boot = inputs['g_bindata_kgspGetBinArchiveGspRmBoot_TU102.c']
    image = _array_bytes(boot, 'UCODE_IMAGE')[0]
    desc = _array_bytes(boot, 'UCODE_DESC')[0]
    if len(desc) != 76 or struct.unpack('<19I', desc)[0] != 4 or len(image) != 4096:
        raise ValueError('unsupported TU102 bootloader descriptor')
    bl = _array_bytes(inputs['g_bindata_ksec2GetBinArchiveBlUcode_TU102.c'], 'UCODE_IMAGE')[0]
    if len(bl) != 768:
        raise ValueError('unexpected TU102 SEC2 loader size')
    blobs = {'booter_load_tu102.bin': booter, 'booter_unload_tu102.bin': unload, 'gsp_boot_tu102.bin': image,
             'gsp_boot_desc_tu102.bin': desc, 'sec2_bl_tu102.bin': bl}
    pinned = json.loads(MANIFEST.read_text())['derived_assets']
    for name, data in blobs.items():
        if hashlib.sha256(data).hexdigest() != pinned[name]:
            raise ValueError('derived asset differs from its pinned SHA-256: ' + name)
    output.mkdir(parents=True, exist_ok=True)
    for name, data in blobs.items():
        (output/name).write_bytes(data)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path, help='pinned NVIDIA source root')
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    extract_assets(args.source, args.output)
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, zlib.error) as error:
        sys.exit(f"extract failed: {error}")
