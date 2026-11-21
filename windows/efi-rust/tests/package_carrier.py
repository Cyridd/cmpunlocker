#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Package the no-CPU-RM carrier for an isolated physical preload/parent check."""
import argparse
import hashlib
from pathlib import Path
import zipfile
from qemu_smoke import check_efi

WORKSPACE = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", type=Path, default=WORKSPACE / "target/carrier-r1/readonly/x86_64-unknown-uefi/release/cmp40-readonly.efi")
    parser.add_argument("--output-dir", type=Path, default=WORKSPACE / "target/carrier-r1")
    args = parser.parse_args()
    digest = check_efi(args.image)
    guide = (WORKSPACE / "tests/CARRIER_HARDWARE.md").read_bytes()
    inputs = [("EFI/BOOT/BOOTX64.EFI", args.image.read_bytes()),
              ("EFI/CMP40/CARRIER1.EFI", args.image.read_bytes()),
              ("TEST_GUIDE.md", guide)]
    sums = "".join(f"{hashlib.sha256(data).hexdigest()}  {name}\n" for name, data in inputs).encode()
    inputs.append(("SHA256SUMS.txt", sums))
    args.output_dir.mkdir(parents=True, exist_ok=True)
    archive = args.output_dir / "cmp40-rust-carrier-r1-usb.zip"
    with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as output:
        for name, data in inputs:
            info = zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            output.writestr(info, data)
    print(f"READONLY: {archive}\nEFI SHA-256: {digest}")


if __name__ == "__main__":
    main()
