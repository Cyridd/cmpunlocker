#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Package early diagnostic with pinned, unstarted carrier-r1 GPU candidates."""
import argparse
import hashlib
import json
from pathlib import Path
import zipfile
from qemu_smoke import check_efi

WORKSPACE = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", type=Path, default=WORKSPACE / "target/entry-r1/x86_64-unknown-uefi/release/cmp40-entrycheck.efi")
    parser.add_argument("--output-dir", type=Path, default=WORKSPACE / "target/entry-r1")
    args = parser.parse_args()
    check_efi(args.image)
    checker = args.image.read_bytes()
    inputs = [("EFI/CMP40/ENTRYCHK.EFI", checker), ("EFI/BOOT/BOOTX64.EFI", checker)]
    summary = json.loads((WORKSPACE / "tests/evidence/stage4a/summary.json").read_text())
    for control in ("zero", "full"):
        entry = summary["provided_gpu_candidates"][control]
        candidate = WORKSPACE / entry["image"]
        if check_efi(candidate) != entry["efi_sha256"]:
            raise ValueError(f"original carrier-r1 image changed: {candidate}")
        inputs.append((f"EFI/CMP40/{control.upper()}CHK.EFI", candidate.read_bytes()))
    inputs.append(("TEST_GUIDE.md", (WORKSPACE / "tests/ENTRYCHECK_HARDWARE.md").read_bytes()))
    sums = "".join(f"{hashlib.sha256(data).hexdigest()}  {name}\n" for name, data in inputs).encode()
    inputs.append(("SHA256SUMS.txt", sums))
    args.output_dir.mkdir(parents=True, exist_ok=True)
    archive = args.output_dir / "cmp40-rust-entrycheck-r1-usb.zip"
    with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as output:
        for name, data in inputs:
            info = zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            output.writestr(info, data)
    print(f"ENTRYCHECK: {archive}\nEFI SHA-256: {hashlib.sha256(checker).hexdigest()}")


if __name__ == "__main__":
    main()
