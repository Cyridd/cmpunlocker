#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Verify runtime assets against the actual C build inputs, including refusal."""
import hashlib
import importlib.util
from pathlib import Path
import re
import struct
import tempfile
import unittest

WORKSPACE = Path(__file__).resolve().parents[1]
INPUTS = WORKSPACE.parent / "tools/unlock40x"
spec = importlib.util.spec_from_file_location("prepare_assets", WORKSPACE / "app/prepare_assets.py")
assets = importlib.util.module_from_spec(spec)
spec.loader.exec_module(assets)


class Assets(unittest.TestCase):
    def test_runtime_inputs_match_c_copy_sizes_and_signature(self):
        source = (INPUTS / "unlock40x_v70.c").read_text()
        self.assertRegex(source, r"#define GSP_RM_BOOT_SIZE\s+0x6000UL")
        signature = re.search(r"u40x_booter_sig_prod\[16\]\s*=\s*\{([^}]+)\}", source)[1]
        self.assertEqual(bytes(int(v, 16) for v in re.findall(r"0x([0-9a-fA-F]+)", signature)), assets.BOOTER_SIG)
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            manifest = assets.prepare(INPUTS, output)
            self.assertEqual((output / "boot_runtime.bin").read_bytes(), (INPUTS / "gsp_rm_boot_dbg.bin").read_bytes()[:0x6000])
            self.assertEqual(manifest["gsp_rm_boot_dbg.bin"]["bytes"], 0x20000)
            self.assertEqual(manifest["boot_runtime.bin"]["bytes"], 0x6000)
            full = (output / "v67_payload.bin").read_bytes()
            zero = (output / "payload_zero.bin").read_bytes()
            expected = bytearray(full)
            for offset in (0xf988, 0xbb20):
                struct.pack_into("<I", expected, offset, 0)
            self.assertEqual(zero, expected)
            self.assertEqual(hashlib.sha256(zero).hexdigest(), assets.ZERO_SHA256)
            booter = bytearray((INPUTS / "booter_ucode_prod.bin").read_bytes())
            booter[0x8700:0x8710] = assets.BOOTER_SIG
            self.assertEqual(manifest["booter_runtime"]["sha256"], hashlib.sha256(booter).hexdigest())

    def test_missing_modified_and_truncated_inputs_never_publish(self):
        for mode in ("missing", "suffix", "truncated", "payload"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                inputs, output = root / "inputs", root / "output"
                inputs.mkdir()
                output.mkdir()
                previous = output / "assets.json"
                previous.write_bytes(b"previous valid build")
                for name in assets.PINS:
                    (inputs / name).symlink_to(INPUTS / name)
                name = "v67_payload.bin" if mode == "payload" else "gsp_rm_boot_dbg.bin"
                path = inputs / name
                blob = bytearray(path.read_bytes())
                path.unlink()
                if mode != "missing":
                    if mode == "truncated":
                        blob = blob[:0x6000]
                    else:
                        blob[-1] ^= 1  # Also reject bytes outside the copied boot prefix.
                    path.write_bytes(blob)
                with self.assertRaises((ValueError, FileNotFoundError)):
                    assets.prepare(inputs, output)
                self.assertEqual(previous.read_bytes(), b"previous valid build")
                self.assertEqual(list(output.iterdir()), [previous])


if __name__ == "__main__":
    unittest.main()
