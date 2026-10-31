#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Extract pinned 610.43.03 firmware and generate the TU102 V67 image."""

from __future__ import annotations

import argparse
import json
import hashlib
import subprocess
import tarfile
from pathlib import Path


PACKAGE_SHA256 = "e17e7c92eeffa178b0fe1f40eb17a47095f8fc3ce4a77bf115cf5bd5a4df86e9"
GSP_SHA256 = "73065619db9ec921d19fc4e519dd04d91a9199b525eaca9b257b89fb8c5ec52c"
PAYLOAD_SHA256 = "b0ec90294d1de3c67e024631c0a11000f281470d76eed806fc24c66b7fd1ef41"
PACKAGE_VERSION = "610.43.03-1"
FIRMWARE_PREFIX = "usr/lib/firmware/nvidia/610.43.03/"
MANIFEST = Path(__file__).with_name("artifacts-610.43.03.json")

PAYLOAD_SIZE = 0xFA00
FILL_DWORD = 0x000004A7


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def extract_member(package: Path, name: str) -> bytes:
    if package.name.endswith(".zst"):
        process = subprocess.Popen(["zstd", "-dc", str(package)], stdout=subprocess.PIPE)
        try:
            with tarfile.open(fileobj=process.stdout, mode="r|") as archive:
                result = read_member(archive, name)
            if process.wait() != 0:
                raise ValueError("zstd could not read the pinned package")
            return result
        finally:
            if process.stdout:
                process.stdout.close()
            if process.poll() is None:
                process.kill()
                process.wait()
    with tarfile.open(package, mode="r:*") as archive:
        return read_member(archive, name)


def read_member(archive: tarfile.TarFile, name: str) -> bytes:
    result = None
    for member in archive:
        if member.name != name:
            continue
        if result is not None:
            raise ValueError(f"duplicate package member: {name}")
        if not member.isfile() or member.size > 128 * 1024 * 1024:
            raise ValueError(f"not a regular file in package: {name}")
        stream = archive.extractfile(member)
        if stream is None:
            raise ValueError(f"could not read package member: {name}")
        result = stream.read()
    if result is None:
        raise ValueError(f"missing package member: {name}")
    return result


def build_payload() -> bytes:
    writes = [(0x00409650, 0xFFFFFFFF), (0x001FA828, 0)]
    guard = 0x6140
    word = FILL_DWORD.to_bytes(4, "little")
    payload = bytearray(word * (PAYLOAD_SIZE // len(word)))

    def put(dmem: int, value: int) -> None:
        off = dmem - 0x600
        assert 0 <= off <= PAYLOAD_SIZE - 4, hex(dmem)
        payload[off : off + 4] = value.to_bytes(4, "little")

    put(0x1700, 0x00000007)
    put(0xFF38, guard)
    put(0xFF40, writes[0][1])
    put(0xFF44, writes[0][0])
    put(0xFF4C, 0x10B9)
    for i in range(len(writes)):
        sp = 0xFF50 + i * 0x18
        put(sp, guard)
        if i + 1 < len(writes):
            put(sp + 0x08, writes[i + 1][1])
            put(sp + 0x0C, writes[i + 1][0])
            put(sp + 0x14, 0x10B9)
        else:
            put(sp + 0x14, 0x7C43)

    put(0xFF68 + 0x0C, 0)
    put(0xFF68 + 0x14, 0xCBD)
    a = 0xFF80
    main_sp = 0xFFF0
    token = main_sp + 0xF
    put(a + 0x00, 1)
    put(a + 0x04, 0)
    put(a + 0x08, 0x8E18)
    put(a + 0x0C, 0)
    put(a + 0x10, FILL_DWORD)
    put(a + 0x14, 0xC95)
    put(a + 0x18, guard)
    put(a + 0x1C, 0)
    put(a + 0x20, 0)
    put(a + 0x24, 1)
    put(a + 0x28, FILL_DWORD)
    put(a + 0x2C, 0x1F89)
    put(a + 0x30, guard)
    put(a + 0x34, guard)
    put(a + 0x38, token)
    put(a + 0x3C, FILL_DWORD)
    put(a + 0x40, 0x1F89)
    put(a + 0x44, guard)
    put(a + 0x48, guard)
    put(a + 0x4C, 0)
    put(a + 0x50, FILL_DWORD)
    put(a + 0x54, 0xE85)
    put(a + 0x58, 0x1F89)
    put(a + 0x5C, guard)
    put(a + 0x60, guard)
    put(a + 0x64, 0)
    put(a + 0x68, FILL_DWORD)
    put(a + 0x6C, 0x7EA4)
    put(main_sp + 0x08, 0)
    return bytes(payload)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("package", type=Path, help="pinned nvidia-utils-610.43.03 package")
    parser.add_argument("output", type=Path, help="directory for extracted firmware and payload")
    args = parser.parse_args()

    package_bytes_hash = hashlib.sha256()
    with args.package.open("rb") as package_file:
        for block in iter(lambda: package_file.read(1024 * 1024), b""):
            package_bytes_hash.update(block)
    if package_bytes_hash.hexdigest() != PACKAGE_SHA256:
        raise SystemExit("package SHA-256 does not match the pinned nvidia-utils artifact")

    pkginfo = extract_member(args.package, ".PKGINFO").decode("utf-8", errors="replace")
    if f"pkgver = {PACKAGE_VERSION}" not in pkginfo.splitlines():
        raise SystemExit("package metadata version does not match 610.43.03-1")

    gsp = extract_member(args.package, FIRMWARE_PREFIX + "gsp_tu10x.bin")
    if sha256(gsp) != GSP_SHA256:
        raise SystemExit("TU102 GSP firmware SHA-256 does not match the pinned artifact")

    payload = build_payload()
    if sha256(payload) != PAYLOAD_SHA256:
        raise SystemExit("generated V67 image differs from its pinned SHA-256")
    manifest = json.loads(MANIFEST.read_text())
    notice = manifest["source_artifacts"][1]["notice"]
    license_text = extract_member(args.package, notice["path"])
    if sha256(license_text) != notice["sha256"]:
        raise SystemExit("NVIDIA license notice differs from pinned package")
    args.output.mkdir(parents=True, exist_ok=True)
    (args.output / "gsp_tu10x.bin").write_bytes(gsp)
    (args.output / "v67-tu102-610.43.03.bin").write_bytes(payload)
    (args.output / "NVIDIA-LICENSE").write_bytes(license_text)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
