# SPDX-License-Identifier: MIT
"""Compare the embedded FULL EFI with a fresh build from the pinned Rust toolchain."""
import argparse
from pathlib import Path


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("built", type=Path)
    parser.add_argument("embedded", type=Path)
    args = parser.parse_args()
    try:
        if args.built.read_bytes() != args.embedded.read_bytes():
            parser.exit(1, "Embedded EFI differs from the Rust FULL build.\n")
    except OSError as error:
        parser.exit(1, f"{error}\n")
