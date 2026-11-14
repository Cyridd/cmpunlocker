# SPDX-License-Identifier: MIT
"""Read CMP40 PCI identity and its BAR0 mirror without changing GPU state."""

import argparse
import json
import mmap
import struct
from pathlib import Path


def read_config(device):
    with (device / "config").open("rb", buffering=0) as stream:
        data = stream.read(8)
    if len(data) != 8:
        raise ValueError("PCI configuration read was incomplete")
    identity, command = struct.unpack("<IH", data[:6])
    if identity == 0xFFFFFFFF or not command & 2:
        raise ValueError("PCI memory decoding is unavailable")
    return identity, command


def read_bar_dword(stream, offset):
    base = offset & ~(mmap.PAGESIZE - 1)
    with mmap.mmap(stream.fileno(), mmap.PAGESIZE, access=mmap.ACCESS_READ,
                   offset=base) as window:
        value = struct.unpack_from("<I", window, offset - base)[0]
    if value == 0xFFFFFFFF or value >> 16 == 0xBADF:
        raise ValueError("GPU register read returned an inaccessible value")
    return value


def read_cached_identity(device):
    vendor = int((device / "vendor").read_text().strip(), 0)
    devid = int((device / "device").read_text().strip(), 0)
    return vendor, devid


def inspect(device):
    vendor, devid = read_cached_identity(device)
    if vendor != 0x10DE or devid not in (0x1F0B, 0x1F4B, 0x1F06):
        raise ValueError("expected CMP40, its observed alternate ID, or RTX 2060 SUPER")
    if (device / "power" / "runtime_status").read_text().strip() != "active":
        raise ValueError("GPU runtime PM is not active; set power/control to on "
                         "before inspection")
    config, command = read_config(device)
    with (device / "resource0").open("rb", buffering=0) as stream:
        boot0 = read_bar_dword(stream, 0)
        if boot0 != 0x166000A1:
            raise ValueError("unsupported TU106 BOOT0 value")
        mirror = read_bar_dword(stream, 0x88000)
        mirror_again = read_bar_dword(stream, 0x88000)
    config_after, command_after = read_config(device)
    cached_after = read_cached_identity(device)
    active = (device / "power" / "runtime_status").read_text().strip() == "active"
    if (not active or config != config_after or command != command_after
            or mirror != mirror_again or cached_after != (vendor, devid)):
        raise ValueError("GPU identity or power state changed during inspection")
    cached = vendor | devid << 16
    return {
        "cached_pci_identity": f"{cached:08x}",
        "config_pci_identity": f"{config:08x}",
        "bar0_xve_identity": f"{mirror:08x}",
        "boot0": f"{boot0:08x}",
        "pci_command": f"{command:04x}",
        "identities_agree": cached == config == mirror,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("device", type=Path, help="PCI device directory")
    args = parser.parse_args()
    try:
        result = inspect(args.device)
    except (OSError, ValueError) as error:
        message = error.strerror if isinstance(error, OSError) else str(error)
        parser.exit(1, f"identity inspection failed: {message}\n")
    print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
