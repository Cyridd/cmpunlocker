#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Exercise the driver helpers with private sysfs and command fixtures."""
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
print("GSP setting reader: 6 stock/GRID cases passed")

# Run the manual helper against a private sysfs tree. Record all modprobe
# arguments so an explicit GSP override fails the test even if it is zero.
unlock = (source.parent / "unlock.sh").read_text()
unlock = unlock.replace("/sys/", "./sys/").replace("((EUID == 0))", "true")
mocks = """
modinfo() { echo NVIDIA; }
modprobe() {
    echo "$*" >> calls
    if [[ $1 == cmp40_preboot ]]; then
        mkdir -p sys/module/cmp40_preboot/parameters
        echo 0 > sys/module/cmp40_preboot/parameters/unlock_error
        echo N > sys/module/cmp40_preboot/parameters/resources_retained
    fi
}
"""
for load_nvidia in (False, True):
    with tempfile.TemporaryDirectory(prefix="cmp40-unlock-test-") as directory:
        root = Path(directory)
        gpu = "0000:01:00.0"
        device = root / "sys/bus/pci/devices" / gpu
        device.mkdir(parents=True)
        (device / "vendor").write_text("0x10de\n")
        (device / "device").write_text("0x1f0b\n")
        args = ["--gpu", gpu]
        if load_nvidia:
            args.append("--load-nvidia")
        subprocess.run(["bash", "-c", mocks + unlock, "unlock.sh", *args],
                       cwd=root, capture_output=True, text=True, check=True)
        calls = (root / "calls").read_text().splitlines()
        assert calls[0].startswith("cmp40_preboot ")
        assert calls[1:] == (["nvidia"] if load_nvidia else []), calls
print("Manual unlock helper: optional NVIDIA load preserves modprobe policy")

# Exercise the Gen2 shell preflight without a GPU or a loadable kernel module.
gen2 = (source.parent / "gen2.sh").read_text()
gen2 = gen2.replace("/sys/", "./sys/").replace("/proc/", "./proc/").replace("((EUID == 0))", "true")
gen2_mocks = """
nvidia-smi() { [[ $1 == -i && $3 == -q && $driver_health == ok ]] || return 1; echo "$report"; }
setpci() { echo "$link"; }
modinfo() { echo fixture; }
modprobe() { return 1; }
insmod() { return 1; }
rmmod() { return 1; }
fuser() { return 1; }
"""
with tempfile.TemporaryDirectory(prefix="cmp40-gen2-helper-") as directory:
    root = Path(directory)
    gpu = "0000:01:00.0"
    device = root / "sys/bus/pci/devices" / gpu
    device.mkdir(parents=True)
    (device / "vendor").write_text("0x10de\n")
    (device / "device").write_text("0x1f0b\n")
    (device / "driver").symlink_to("nvidia")
    version = root / "proc/driver/nvidia/version"
    version.parent.mkdir(parents=True)
    version.write_text("NVIDIA proprietary driver\n")
    for report in ("GSP Firmware Version : N/A", "GSP Firmware Version : 620.70.06", ""):
        for health, link, expected in (
            ("ok", "0102", "already has an active Gen2 link"),
            ("ok", "0101", "pre-driver module/kernel mismatch"),
            ("failed", "0102", "NVIDIA is not responding"),
        ):
            wrapper = 'report=$1; driver_health=$2; link=$3; shift 3\n' + gen2_mocks + gen2
            result = subprocess.run(["bash", "-c", wrapper, "gen2.sh", report, health, link, "--gpu", gpu],
                                    cwd=root, capture_output=True, text=True)
            assert (result.returncode == 0) == (health == "ok" and link == "0102"), result
            assert expected in result.stdout + result.stderr, result
print("Gen2 helper: 9 driver health and link cases passed without GSP settings")
