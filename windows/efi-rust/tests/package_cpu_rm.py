#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Package local experimental EFI images for separate USB hardware tests."""
import argparse
import hashlib
from pathlib import Path
import zipfile
from qemu_smoke import check_efi

WORKSPACE = Path(__file__).resolve().parents[1]


def package_rebar(args, guide):
    full = args.image_dir / "cmp40-cpu-rm-full.efi"
    zero = args.image_dir / "cmp40-cpu-rm-zero.efi"
    for image in (full, zero):
        check_efi(image)
    inputs = [("EFI/BOOT/BOOTX64.EFI", full.read_bytes()),
              ("EFI/CMP40/FULLR1.EFI", full.read_bytes()),
              ("EFI/CMP40/ZEROR1.EFI", zero.read_bytes()),
              ("EFI/40HX/REBAR.CFG", b"CMP40HX_REBAR_V1=1\n"),
              ("TEST_GUIDE.md", guide)]
    sums = "".join(f"{hashlib.sha256(data).hexdigest()}  {name}\n" for name, data in inputs).encode()
    inputs.append(("SHA256SUMS.txt", sums))
    args.output_dir.mkdir(parents=True, exist_ok=True)
    archive = args.output_dir / "cmp40-rust-rebar-r1-usb.zip"
    # Keep previously prepared test packages intact.
    with zipfile.ZipFile(archive, "x", compression=zipfile.ZIP_DEFLATED) as output:
        for name, data in inputs:
            info = zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            output.writestr(info, data)
    print(f"REBAR: {archive}\nArchive SHA-256: {hashlib.sha256(archive.read_bytes()).hexdigest()}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image-dir", type=Path, default=WORKSPACE / "target/x86_64-unknown-uefi/release")
    parser.add_argument("--output-dir", type=Path, default=WORKSPACE / "target")
    parser.add_argument("--guide", type=Path)
    parser.add_argument("--control", choices=("logcheck", "zero", "full"), action="append")
    parser.add_argument("--rebar", action="store_true", help="package FULL/ZERO together with explicit ReBAR USB policy")
    args = parser.parse_args()
    if args.rebar and args.control:
        parser.error("--rebar packages both FULL and ZERO; do not combine it with --control")
    guide_path = args.guide or WORKSPACE / ("tests/REBAR_HARDWARE.md" if args.rebar else "tests/CPU_RM_HARDWARE.md")
    guide = guide_path.read_bytes()
    if args.rebar:
        package_rebar(args, guide)
        return
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
