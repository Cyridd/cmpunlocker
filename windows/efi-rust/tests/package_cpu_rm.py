#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Package local experimental EFI images for separate USB hardware tests."""
import argparse
import hashlib
from pathlib import Path
import zipfile
from qemu_smoke import check_efi

WORKSPACE = Path(__file__).resolve().parents[1]
GEN2_R2_HASHES = {
    "prepare": "98f8bc17bf1c2a1af1a3eb71b046923bc09016c0bb3c83124e041cceeb3e7b0b",
    "targets": "c4d8b2141bc57321d968593685b7be8a878e50faf8b93ac18fa1b8bc12929660",
    "retrain": "1c31f8dda02409f5374f328476999a7caddc583d0216121834b560cc15625263",
}


def package_gen2(args, guide):
    images = [("G2PREP", "prepare"), ("G2TLS", "targets"), ("G2TRAIN", "retrain")]
    inputs = []
    for name, mode in images:
        path = args.image_dir / f"cmp40-gen2-{mode}.efi"
        check_efi(path)
        data = path.read_bytes()
        if args.gen2_registry and hashlib.sha256(data).hexdigest() != GEN2_R2_HASHES[mode]:
            raise ValueError("registry trials must reuse the frozen Gen2-r2 images")
        suffix = "2" if args.gen2_revision == "r2" else ""
        inputs.append((f"EFI/CMP40/{name}{suffix}.EFI", data))
    check_efi(args.baseline_full)
    baseline = args.baseline_full.read_bytes()
    if hashlib.sha256(baseline).hexdigest() != "50b8974e4af13630e98b91c1008fa8e3b8dfe2a548a5edb83f06d0ad549f6473":
        raise ValueError("baseline FULL must match the accepted ReBAR-r1 image")
    fallback = inputs[2][1] if args.gen2_registry else inputs[0][1]
    inputs += [("EFI/BOOT/BOOTX64.EFI", fallback),
               ("EFI/CMP40/BASEFULL.EFI", baseline),
               ("EFI/40HX/REBAR.CFG", b"CMP40HX_REBAR_V1=0\n"),
               ("TEST_GUIDE.md", guide)]
    if args.gen2_registry:
        inputs.append(("WINDOWS/gen2_registry.ps1", (WORKSPACE / "tests/gen2_registry.ps1").read_bytes()))
    sums = "".join(f"{hashlib.sha256(data).hexdigest()}  {name}\n" for name, data in inputs).encode()
    inputs.append(("SHA256SUMS.txt", sums))
    args.output_dir.mkdir(parents=True, exist_ok=True)
    archive_name = "cmp40-rust-gen2-registry-r1-usb.zip" if args.gen2_registry else f"cmp40-rust-gen2-{args.gen2_revision}-usb.zip"
    archive = args.output_dir / archive_name
    with zipfile.ZipFile(archive, "x", compression=zipfile.ZIP_DEFLATED) as output:
        for name, data in inputs:
            info = zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            output.writestr(info, data)
    print(f"GEN2: {archive}\nArchive SHA-256: {hashlib.sha256(archive.read_bytes()).hexdigest()}")


def package_rebar(args, guide):
    full = args.image_dir / "cmp40-cpu-rm-full.efi"
    zero = args.image_dir / "cmp40-cpu-rm-zero.efi"
    for image in (full, zero):
        check_efi(image)
    inputs = [("EFI/BOOT/BOOTX64.EFI", full.read_bytes()),
              ("EFI/CMP40/FULLOPT.EFI" if args.optimize else "EFI/CMP40/FULLLOG.EFI" if args.logging else "EFI/CMP40/FULLR1.EFI", full.read_bytes()),
              ("EFI/CMP40/ZEROOPT.EFI" if args.optimize else "EFI/CMP40/ZEROLOG.EFI" if args.logging else "EFI/CMP40/ZEROR1.EFI", zero.read_bytes()),
              ("EFI/40HX/REBAR.CFG", b"CMP40HX_REBAR_V1=1\n"),
              ("TEST_GUIDE.md", guide)]
    if args.logging:
        inputs.append(("TOOLS/trace_log.py", (WORKSPACE / "tests/trace_log.py").read_bytes()))
    if args.optimize:
        check_efi(args.baseline_full)
        baseline = args.baseline_full.read_bytes()
        if hashlib.sha256(baseline).hexdigest() != "e1fb4b5bcd2aac189ac383bc0c279c1617d995b4d29c82eedcd9349ccdeabb11":
            raise ValueError("optimization control must match the accepted logging-r1 FULL")
        inputs.append(("EFI/CMP40/BASEFULL.EFI", baseline))
    sums = "".join(f"{hashlib.sha256(data).hexdigest()}  {name}\n" for name, data in inputs).encode()
    inputs.append(("SHA256SUMS.txt", sums))
    args.output_dir.mkdir(parents=True, exist_ok=True)
    archive = args.output_dir / ("cmp40-rust-opt-r1-usb.zip" if args.optimize else "cmp40-rust-logging-r1-usb.zip" if args.logging else "cmp40-rust-rebar-r1-usb.zip")
    # Keep previously prepared test packages intact.
    with zipfile.ZipFile(archive, "x", compression=zipfile.ZIP_DEFLATED) as output:
        for name, data in inputs:
            info = zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            output.writestr(info, data)
    label = "OPTIMIZE" if args.optimize else "LOGGING" if args.logging else "REBAR"
    print(f"{label}: {archive}\nArchive SHA-256: {hashlib.sha256(archive.read_bytes()).hexdigest()}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image-dir", type=Path, default=WORKSPACE / "target/x86_64-unknown-uefi/release")
    parser.add_argument("--output-dir", type=Path, default=WORKSPACE / "target")
    parser.add_argument("--guide", type=Path)
    parser.add_argument("--control", choices=("logcheck", "zero", "full"), action="append")
    parser.add_argument("--rebar", action="store_true", help="package FULL/ZERO together with explicit ReBAR USB policy")
    parser.add_argument("--logging", action="store_true", help="package compact-log FULL/ZERO with ReBAR policy and trace decoder")
    parser.add_argument("--optimize", action="store_true", help="package optimized FULL/ZERO with frozen logging-r1 FULL control")
    parser.add_argument("--gen2", action="store_true", help="package separate FULL Gen2 experiments with ReBAR disabled")
    parser.add_argument("--gen2-revision", choices=("r1", "r2"), help="Gen2 archive revision; default r1")
    parser.add_argument("--gen2-registry", action="store_true", help="reuse frozen Gen2-r2 images with the stock-driver registry trial")
    parser.add_argument("--baseline-full", type=Path, help="accepted FULL control: ReBAR-r1 for --gen2, logging-r1 for --optimize")
    args = parser.parse_args()
    if args.optimize and (args.logging or args.rebar or args.gen2 or args.control or not args.baseline_full):
        parser.error("--optimize requires --baseline-full and cannot be combined with other package modes")
    args.logging = args.logging or args.optimize
    if args.logging and (args.rebar or args.gen2 or args.control):
        parser.error("--logging packages FULL/ZERO and cannot be combined with --rebar/--gen2/--control")
    if args.gen2_registry and (not args.gen2 or args.gen2_revision != "r2"):
        parser.error("--gen2-registry requires --gen2 --gen2-revision r2")
    if args.gen2_revision and not args.gen2:
        parser.error("--gen2-revision is only valid with --gen2")
    args.gen2_revision = args.gen2_revision or "r1"
    if args.gen2 and (args.rebar or args.control or not args.baseline_full):
        parser.error("--gen2 needs --baseline-full and cannot be combined with --rebar/--control")
    if args.baseline_full and not (args.gen2 or args.optimize):
        parser.error("--baseline-full is only valid with --gen2/--optimize")
    if args.rebar and args.control:
        parser.error("--rebar packages both FULL and ZERO; do not combine it with --control")
    guide_path = args.guide or WORKSPACE / ("LOGGING_POLICY.md" if args.logging else "tests/GEN2_HARDWARE.md" if args.gen2 else "tests/REBAR_HARDWARE.md" if args.rebar else "tests/CPU_RM_HARDWARE.md")
    guide = guide_path.read_bytes()
    if args.gen2:
        package_gen2(args, guide)
        return
    if args.rebar or args.logging:
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
