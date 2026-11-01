#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Build the actual Gen2 C implementation against a minimal hardware mock."""
import os
from pathlib import Path
import shlex
import subprocess
import tempfile

root = Path(__file__).resolve().parent
with tempfile.TemporaryDirectory(prefix="cmp40-gen2-test-") as directory:
    work = Path(directory)
    include = work / "linux"
    include.mkdir()
    for name in ("pci.h", "io.h", "delay.h", "types.h", "string.h"):
        (include / name).write_text('#include "gen2_shim.h"\n')
    binary = work / "test_gen2"
    subprocess.run([
        *shlex.split(os.environ.get("CC", "cc")), "-std=gnu11", "-O2",
        "-Wall", "-Wextra", "-Werror", "-I", str(work), "-I", str(root),
        str(root / "test_gen2.c"), "-o", str(binary),
    ], check=True)
    subprocess.run([str(binary)], check=True)
