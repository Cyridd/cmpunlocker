#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Check stock and GRID representations of the actual driver's GSP setting."""
from pathlib import Path
import subprocess
import tempfile

source = Path(__file__).resolve().parents[1] / "common.sh"
with tempfile.TemporaryDirectory(prefix="cmp40-gsp-test-") as directory:
    root = Path(directory)
    sys_file, proc_file = root / "sys-param", root / "proc-params"
    for sys_value, proc_value, expected in (
        ("0\n", "EnableGpuFirmware: 1\n", "0"),
        ("1\n", "EnableGpuFirmware: 0\n", "1"),
        (None, "OtherParam: 1\nEnableGpuFirmware: 0\n", "0"),
        (None, "EnableGpuFirmware: 1\n", "1"),
        (None, "OtherParam: 0\n", None),
        (None, None, None),
    ):
        for path, value in ((sys_file, sys_value), (proc_file, proc_value)):
            if value is None:
                path.unlink(missing_ok=True)
            else:
                path.write_text(value)
        result = subprocess.run(
            ["bash", "-c", 'source "$1"; cmp40_gsp_setting "$2" "$3"',
             "test", str(source), str(sys_file), str(proc_file)],
            capture_output=True, text=True, check=False,
        )
        if expected is None:
            assert result.returncode != 0, result
        else:
            assert result.returncode == 0 and result.stdout.strip() == expected, result
print("CPU-RM helpers: 6 stock/GRID GSP cases passed")
