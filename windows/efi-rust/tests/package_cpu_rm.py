#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Package local experimental EFI images for separate USB hardware tests."""
import argparse
import hashlib
from pathlib import Path
import zipfile
from qemu_smoke import check_efi

WORKSPACE = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image-dir", type=Path, default=WORKSPACE / "target/x86_64-unknown-uefi/release")
    parser.add_argument("--output-dir", type=Path, default=WORKSPACE / "target")
    parser.add_argument("--guide", type=Path, default=WORKSPACE / "tests/CPU_RM_HARDWARE.md")
    parser.add_argument("--control", choices=("logcheck", "zero", "full"), action="append")
    args = parser.parse_args()
    guide = args.guide.read_bytes()
    # Verify every selected image before making any archive.
    controls = list(dict.fromkeys(args.control or ("logcheck", "zero", "full")))
    inputs = []
    for control in controls:
        filename = "cmp40-logcheck.efi" if control == "logcheck" else f"cmp40-cpu-rm-{control}.efi"
        inputs.append((control, args.image_dir / filename))
    hashes = {control: check_efi(path) for control, path in inputs}
    args.output_dir.mkdir(parents=True, exist_ok=True)
    for control, path in inputs:
        archive = args.output_dir / ("cmp40-rust-logcheck-usb.zip" if control == "logcheck" else f"cmp40-rust-cpu-rm-{control}-usb.zip")
        sums = (f"{hashes[control]}  EFI/BOOT/BOOTX64.EFI\n"
                f"{hashlib.sha256(guide).hexdigest()}  TEST_GUIDE.md\n").encode()
        with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as output:
            for name, data in (("EFI/BOOT/BOOTX64.EFI", path.read_bytes()),
                               ("TEST_GUIDE.md", guide), ("SHA256SUMS.txt", sums)):
                info = zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0))
                info.compress_type = zipfile.ZIP_DEFLATED
                output.writestr(info, data)
        print(f"{control.upper()}: {archive}\nEFI SHA-256: {hashes[control]}")


if __name__ == "__main__":
    main()
