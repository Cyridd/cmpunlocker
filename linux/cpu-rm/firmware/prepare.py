#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Prepare hash-pinned CMP40HX CPU-RM firmware without installing a driver."""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import struct
import tarfile
import tempfile
import zlib

from extract_boot_assets import PINNED, extract_assets
from fetch_artifacts import MANIFEST, fetch, inputs
from generate_v67 import build_payload, extract_member

ROOT = Path(__file__).resolve().parents[3]
PAYLOAD = ROOT / "windows/tools/unlock40x/v67_payload.bin"
FULL_SHA256 = "de7288bcfb80c2d4b259a59e823c926ba0a9adcab594219cc6802c94257ce271"
ZERO_SHA256 = "6e1f97567bc75b7389782226c73d52c07ae51d04c00fc7df975e5ec325463262"
FULL_NAME = "v67-tu106-stock-handoff-610.43.03.bin"
ZERO_NAME = "v67-tu106-locked-control-610.43.03.bin"


def checked(data: bytes, expected: str, name: str) -> bytes:
    if hashlib.sha256(data).hexdigest() != expected:
        raise ValueError(f"SHA-256 mismatch: {name}")
    return data


def selector_payloads(data: bytes) -> dict[str, bytes]:
    checked(data, FULL_SHA256, FULL_NAME)
    if len(data) != 64000:
        raise ValueError("invalid selector payload size")
    zero = bytearray(data)
    for offset, expected in ((0xF988, 0x88888888), (0xBB20, 8)):
        if struct.unpack_from("<I", zero, offset)[0] != expected:
            raise ValueError("invalid selector operand")
        struct.pack_into("<I", zero, offset, 0)
    return {FULL_NAME: data, ZERO_NAME: checked(bytes(zero), ZERO_SHA256, ZERO_NAME)}


def source_files(archive: Path, output: Path) -> None:
    # Read only the four generated files; never extract archive paths to disk.
    found = set()
    prefix = "NVIDIA-kernel-module-source-610.43.03/src/nvidia/generated/"
    with tarfile.open(archive, "r:xz") as source:
        for member in source:
            if not member.name.startswith(prefix):
                continue
            name = member.name[len(prefix):]
            if name not in PINNED:
                continue
            if name in found or not member.isfile() or member.size > 16 * 1024 * 1024:
                raise ValueError(f"invalid generated source member: {name}")
            stream = source.extractfile(member)
            if stream is None:
                raise ValueError(f"unreadable generated source: {name}")
            data = checked(stream.read(), PINNED[name], name)
            destination = output / "src/nvidia/generated" / name
            destination.parent.mkdir(parents=True, exist_ok=True)
            destination.write_bytes(data)
            found.add(name)
    if found != set(PINNED):
        raise ValueError("missing pinned generated sources")


def prepare(cache: Path, output: Path, offline: bool) -> None:
    manifest = json.loads(MANIFEST.read_text())
    source, package = inputs(manifest)
    source_path = fetch(source, cache, offline)
    package_path = fetch(package, cache, offline)
    # All inputs and outputs are validated in staging before publishing assets.
    with tempfile.TemporaryDirectory(prefix="cmp40-firmware-") as temporary:
        staging = Path(temporary)
        source_files(source_path, staging / "source")
        assets = staging / "assets"
        extract_assets(staging / "source", assets)
        gsp = package["firmware"][0]
        data = checked(extract_member(package_path, gsp["path"]), gsp["sha256"], gsp["path"])
        if len(data) != gsp["size"]:
            raise ValueError("invalid GSP firmware size")
        (assets / "gsp_tu10x.bin").write_bytes(data)
        notice = package["notice"]
        (assets / "NVIDIA-FIRMWARE-LICENSE.txt").write_bytes(
            checked(extract_member(package_path, notice["path"]), notice["sha256"], notice["path"]))
        for name, blob in selector_payloads(PAYLOAD.read_bytes()).items():
            (assets / name).write_bytes(blob)
        legacy = checked(build_payload(), manifest["tu102_payload"]["sha256"], "legacy V67")
        (assets / "v67-tu102-610.43.03.bin").write_bytes(legacy)
        output.mkdir(parents=True, exist_ok=True)
        if output.is_symlink():
            raise ValueError("output directory must not be a symlink")
        for path in assets.iterdir():
            destination = output / path.name
            if destination.is_symlink():
                raise ValueError(f"refusing output symlink: {destination}")
        for path in assets.iterdir():
            (output / path.name).write_bytes(path.read_bytes())
    print(f"Verified firmware prepared in {output}")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cache", type=Path, default=Path(__file__).parents[1] / "build/inputs")
    parser.add_argument("--output", type=Path, default=Path(__file__).parents[1] / "build/assets")
    parser.add_argument("--offline", action="store_true", help="verify cached inputs without downloads")
    args = parser.parse_args()
    try:
        prepare(args.cache, args.output, args.offline)
    except (OSError, ValueError, tarfile.TarError, zlib.error) as error:
        parser.exit(1, f"firmware preparation failed: {error}\n")


if __name__ == "__main__":
    main()
