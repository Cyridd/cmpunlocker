#!/usr/bin/env python3
"""Pin the images whose data layout is used by experiment 7."""
import hashlib
from pathlib import Path
from verify_selector_payload import verify


def verify_images(root):
    verify(root / 'v67_payload.bin')
    blob = (root / 'booter_ucode_prod.bin').read_bytes()
    digest = hashlib.sha256(blob).hexdigest()
    if len(blob) != 0xE700 or digest != 'e0f0fc93da097b5b68e5af3adb58b2235ed1fad5fce0b23592d3895af44cb743':
        raise ValueError(f'Unknown TU102 booter: {len(blob)} bytes, SHA256 {digest}')


if __name__ == '__main__':
    verify_images(Path(__file__).resolve().parent)
    print('Known V67 and signed TU102 booter verified (no firmware execution)')
