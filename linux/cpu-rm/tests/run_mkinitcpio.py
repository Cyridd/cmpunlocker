#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Exercise the initramfs hook and its failure guard without loading a module."""
import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest


HOOK = Path(__file__).resolve().parents[1] / "mkinitcpio/cmp40-unlock"
SOURCE = HOOK.read_text()
RUNTIME = re.search(r"^#!/usr/bin/bash\n.*?^SCRIPT$", SOURCE, re.M | re.S)[0][:-6]

BUILD_API = r'''
modinfo() {
    [[ $TEST_MODULE != missing ]] || return 1
    if [[ " $* " == *" -F firmware "* ]]; then
        printf '%s\n' cmp40-preboot/610.43.03/first.bin cmp40-preboot/610.43.03/second.bin
    fi
    return 0
}
warning() { printf 'warning: %s\n' "$*"; }
error() { printf 'error: %s\n' "$*"; }
add_module() { printf 'module: %s\n' "$1" >> "$BUILDROOT/manifest"; }
add_binary() { printf 'binary: %s\n' "$1" >> "$BUILDROOT/manifest"; }
add_file() {
    local src=$1 dest=${2:-$1}
    mkdir -p "$BUILDROOT${dest%/*}"
    if [[ $src == - ]]; then cat > "$BUILDROOT$dest"
    else cp "$src" "$BUILDROOT$dest"; fi
    chmod "${3:-644}" "$BUILDROOT$dest"
}
add_symlink() {
    mkdir -p "$BUILDROOT${1%/*}"
    ln -s "$2" "$BUILDROOT$1"
}
KERNELVERSION=test-kernel
HOOKS=(base systemd modconf cmp40-unlock)
MODULES=()
[[ $TEST_CONFIG != no-systemd ]] || HOOKS=(base modconf cmp40-unlock)
[[ $TEST_CONFIG != early-module ]] || MODULES=(cmp40_preboot)
source "$1"
build
'''

MODPROBE = r'''#!/bin/bash
printf '%s\n' "$*" >> "$TEST_CALLS"
if [[ $1 == -n ]]; then
    [[ $TEST_RESULT != unavailable ]]
    exit $?
fi
[[ -s $TEST_GUARD ]] || exit 80
if [[ $TEST_RESULT == interrupt ]]; then
    kill -KILL "$PPID"
    exit 1
fi
[[ $TEST_RESULT != load-error ]] || exit 6
mkdir -p "$TEST_PARAMETERS"
[[ $TEST_RESULT == missing-error ]] || printf '%s\n' "$TEST_ERROR" > "$TEST_PARAMETERS/unlock_error"
[[ $TEST_RESULT == missing-retained ]] || printf '%s\n' "$TEST_RETAINED" > "$TEST_PARAMETERS/resources_retained"
'''


class HookTests(unittest.TestCase):
    def test_build(self):
        for case in ("success", "missing-module", "missing-firmware", "empty-firmware",
                     "no-systemd", "early-module"):
            with self.subTest(case=case), tempfile.TemporaryDirectory(prefix="cmp40-hook-build-") as directory:
                root = Path(directory)
                firmware = root / "firmware/cmp40-preboot/610.43.03"
                firmware.mkdir(parents=True)
                for name in ("first.bin", "second.bin", "LICENSE"):
                    (firmware / name).write_text("fixture\n")
                if case == "missing-firmware":
                    (firmware / "second.bin").unlink()
                elif case == "empty-firmware":
                    (firmware / "second.bin").write_text("")
                hook = root / "hook"
                hook.write_text(SOURCE.replace("/usr/lib/firmware", str(root / "firmware")))
                image = root / "image"
                image.mkdir()
                result = subprocess.run(
                    ["bash", "-c", BUILD_API, "test", str(hook)], capture_output=True, text=True,
                    env={**os.environ, "BUILDROOT": str(image), "TEST_CONFIG": case,
                         "TEST_MODULE": "missing" if case == "missing-module" else "present"},
                )
                if case in ("no-systemd", "early-module"):
                    self.assertEqual(result.returncode, 1, result)
                else:
                    self.assertEqual(result.returncode, 0, result)
                unit = image / "usr/lib/systemd/system/cmp40-unlock.service"
                if case != "success":
                    self.assertFalse(unit.exists())
                    self.assertFalse((image / "manifest").exists())
                    continue
                wrapper = image / "usr/lib/cmp40-unlock/cmp40-initrd-unlock"
                self.assertTrue(wrapper.stat().st_mode & 0o111)
                self.assertIn("binary: /usr/bin/bash", (image / "manifest").read_text())
                link = unit.parent / "sysinit.target.wants/cmp40-unlock.service"
                self.assertEqual(link.resolve(), unit.resolve())
                self.assertEqual(subprocess.run(["bash", "-n", str(wrapper)]).returncode, 0)

    def test_runtime(self):
        cases = ("success", "no-gpu", "two-gpus", "bound", "nvidia-loaded", "preboot-loaded",
                 "unavailable", "no-firmware", "previous-guard", "load-error", "unlock-error",
                 "retained", "missing-error", "missing-retained", "guard-error", "interrupt")
        skipped = {"no-gpu", "two-gpus", "bound", "nvidia-loaded", "preboot-loaded",
                   "unavailable", "no-firmware", "previous-guard"}
        for case in cases:
            with self.subTest(case=case), tempfile.TemporaryDirectory(prefix="cmp40-hook-runtime-") as directory:
                root = Path(directory)
                devices = root / "sys/bus/pci/devices"
                devices.mkdir(parents=True)
                count = 0 if case == "no-gpu" else 2 if case == "two-gpus" else 1
                for index in range(count):
                    device = devices / f"0000:0{index + 1}:00.0"
                    device.mkdir()
                    (device / "vendor").write_text("0x10de\n")
                    (device / "device").write_text("0x1f0b\n")
                    if case == "bound":
                        (device / "driver").symlink_to("/nonexistent/test-driver")
                for module, selected in (("nvidia", "nvidia-loaded"), ("cmp40_preboot", "preboot-loaded")):
                    if case == selected:
                        (root / "sys/module" / module).mkdir(parents=True)
                firmware = root / "firmware/cmp40-preboot/610.43.03"
                if case != "no-firmware":
                    firmware.mkdir(parents=True)
                guard = root / "run/modprobe.d/cmp40-block-nvidia.conf"
                if case == "previous-guard":
                    guard.parent.mkdir(parents=True)
                    guard.write_text("existing guard\n")
                elif case == "guard-error":
                    guard.parent.parent.mkdir(parents=True)
                    guard.parent.write_text("not a directory\n")
                script = RUNTIME.replace("/sys/", str(root / "sys") + "/")
                script = script.replace("/usr/lib/firmware/", str(root / "firmware") + "/")
                script = script.replace("/run/modprobe.d/", str(root / "run/modprobe.d") + "/")
                script = script.replace("/dev/kmsg", str(root / "kmsg"))
                wrapper = root / "runtime.sh"
                wrapper.write_text(script)
                binaries = root / "bin"
                binaries.mkdir()
                probe = binaries / "modprobe"
                probe.write_text(MODPROBE)
                probe.chmod(0o755)
                calls = root / "calls"
                result = subprocess.run(
                    ["bash", str(wrapper)], capture_output=True, text=True,
                    env={**os.environ, "PATH": f"{binaries}:{os.environ['PATH']}",
                         "TEST_RESULT": case, "TEST_CALLS": str(calls), "TEST_GUARD": str(guard),
                         "TEST_PARAMETERS": str(root / "sys/module/cmp40_preboot/parameters"),
                         "TEST_ERROR": "-5" if case == "unlock-error" else "0",
                         "TEST_RETAINED": "Y" if case == "retained" else "N"},
                )
                attempted = calls.exists() and "mode=unlock" in calls.read_text()
                if case == "success":
                    self.assertEqual(result.returncode, 0, result)
                    self.assertTrue(attempted)
                    self.assertFalse(guard.exists())
                    self.assertIn("pre-driver unlock completed", (root / "kmsg").read_text())
                elif case in skipped:
                    self.assertEqual(result.returncode, 0, result)
                    self.assertFalse(attempted)
                    if case == "previous-guard":
                        self.assertEqual(guard.read_text(), "existing guard\n")
                    else:
                        self.assertFalse(guard.exists())
                elif case == "guard-error":
                    self.assertEqual(result.returncode, 1, result)
                    self.assertFalse(attempted)
                else:
                    self.assertEqual(result.returncode, -9 if case == "interrupt" else 1, result)
                    self.assertTrue(attempted)
                    rules = guard.read_text()
                    for module in ("nvidia", "nvidia_vgpu_vfio", "nouveau"):
                        self.assertIn(f"install {module} /usr/bin/false", rules)


if __name__ == "__main__":
    unittest.main(verbosity=2)
