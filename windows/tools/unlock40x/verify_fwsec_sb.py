#!/usr/bin/env python3
"""Verify the exact native FWSEC/loader and unchanged V67/booter images."""
import hashlib
from pathlib import Path
from verify_payload_control import verify_images


def verify_sb_images(root):
    verify_images(root)
    for name, size, expected in (
        ('fwsec_40hx_prod.bin', 0x9df0, '4e39b01bf057d5f7e4a25567b6d3dbd0fd3d672a78e1a86f36d68e255558306c'),
        ('bl_gsp_tu102.bin', 0x300, 'f21f1cfbb8fffa8067685314367fc4c50f3f84dd99eda7c19db4292d9269b9d9'),
    ):
        blob = (root / name).read_bytes()
        digest = hashlib.sha256(blob).hexdigest()
        if len(blob) != size or digest != expected:
            raise ValueError(f'Unknown {name}: {len(blob)} bytes, SHA256 {digest}')


if __name__ == '__main__':
    verify_sb_images(Path(__file__).resolve().parent)
    print('Known native FWSEC/loader and V67/signed booter verified')
