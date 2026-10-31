#!/usr/bin/env python3
"""Pin the known V67 image before building either selector control."""
import hashlib
from pathlib import Path
import struct


def verify(path):
    data = path.read_bytes()
    digest = hashlib.sha256(data).hexdigest()
    if len(data) != 64000 or digest != 'de7288bcfb80c2d4b259a59e823c926ba0a9adcab594219cc6802c94257ce271':
        raise ValueError(f'Unknown V67 payload: {len(data)} bytes, SHA256 {digest}')
    for offset, expected in ((0xF988, 0x88888888), (0xF9A4, 0x409664),
                             (0xBB20, 8), (0xBB38, 0x40966C)):
        if struct.unpack_from('<I', data, offset)[0] != expected:
            raise ValueError(f'Unexpected selector operand/address at {offset:#x}')
    return data


if __name__ == '__main__':
    verify(Path(__file__).with_name('v67_payload.bin'))
    print('Known V67 payload and selector operands verified')
