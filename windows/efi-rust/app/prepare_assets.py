#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Prepare only the pinned inputs used by the project's current C CPU-RM EFI."""
import argparse
import hashlib
import json
from pathlib import Path
import struct
import tempfile
import os

PINS = {
    "v67_payload.bin": (0xfa00, "de7288bcfb80c2d4b259a59e823c926ba0a9adcab594219cc6802c94257ce271"),
    "booter_ucode_prod.bin": (0xe700, "e0f0fc93da097b5b68e5af3adb58b2235ed1fad5fce0b23592d3895af44cb743"),
    "gsp_rm_boot_dbg.bin": (0x20000, "fa43239bcee7b97ca62f007cc68487560a39e19f74f3dde7486db3f98df8e471"),
    "fwsec_40hx_prod.bin": (0x9df0, "4e39b01bf057d5f7e4a25567b6d3dbd0fd3d672a78e1a86f36d68e255558306c"),
    "bl_gsp_tu102.bin": (0x300, "f21f1cfbb8fffa8067685314367fc4c50f3f84dd99eda7c19db4292d9269b9d9"),
}
ZERO_SHA256 = "6e1f97567bc75b7389782226c73d52c07ae51d04c00fc7df975e5ec325463262"
BOOT_PREFIX_SHA256 = "de676bae28a480011d3d012db14bef539324e62a841a9627863c689bea168af3"
# The existing C loader patches these original TU102 SIG_PROD bytes at 0x8700.
BOOTER_SIG = bytes.fromhex("3260436798897b06e85f1aa871435dcb")


def prepare(inputs, output):
    blobs = {}
    for name, (size, digest) in PINS.items():
        blob = (inputs / name).read_bytes()
        if len(blob) != size or hashlib.sha256(blob).hexdigest() != digest:
            raise ValueError(f"Pinned input mismatch: {name}")
        blobs[name] = blob
    # C embeds the full source file but copies only GSP_RM_BOOT_SIZE bytes.
    # Verify the full input before deriving that exact runtime prefix.
    boot = blobs["gsp_rm_boot_dbg.bin"][:0x6000]
    if hashlib.sha256(boot).hexdigest() != BOOT_PREFIX_SHA256:
        raise ValueError("GSP boot runtime prefix mismatch")
    blobs["boot_runtime.bin"] = boot
    original = blobs["v67_payload.bin"]
    for offset, value in [(0xf988, 0x88888888), (0xf9a4, 0x409664), (0xbb20, 8), (0xbb38, 0x40966c)]:
        if struct.unpack_from("<I", original, offset)[0] != value:
            raise ValueError(f"Selector contract mismatch at {offset:#x}")
    zero = bytearray(original)
    for offset in [0xf988, 0xbb20]:
        struct.pack_into("<I", zero, offset, 0)
    if hashlib.sha256(zero).hexdigest() != ZERO_SHA256:
        raise ValueError("ZERO payload digest mismatch")
    blobs["payload_zero.bin"] = bytes(zero)
    blobs["booter_sig.bin"] = BOOTER_SIG
    manifest = {name: {"bytes": len(blob), "sha256": hashlib.sha256(blob).hexdigest()} for name, blob in blobs.items()}
    patched = bytearray(blobs["booter_ucode_prod.bin"])
    patched[0x8700:0x8710] = BOOTER_SIG
    manifest["booter_runtime"] = {"bytes": len(patched), "sha256": hashlib.sha256(patched).hexdigest()}
    output.mkdir(parents=True, exist_ok=True)
    # Validate every input and derived control before publishing any asset.
    with tempfile.TemporaryDirectory(prefix="assets-", dir=output) as temporary:
        staging = Path(temporary)
        for name, blob in blobs.items():
            (staging / name).write_bytes(blob)
        (staging / "assets.json").write_text(json.dumps(manifest, indent=2) + "\n")
        for name in [*blobs, "assets.json"]:
            os.replace(staging / name, output / name)
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--inputs", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    prepare(args.inputs, args.output)


if __name__ == "__main__":
    main()
