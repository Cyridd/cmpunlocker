# SPDX-License-Identifier: GPL-2.0-only
"""Pin the production payload and the two-operand negative control."""
import io
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
from fetch_artifacts import MANIFEST, inputs
from generate_v67 import extract_member
from prepare import PAYLOAD, FULL_NAME, ZERO_NAME, checked, selector_payloads


class PayloadTests(unittest.TestCase):
    def test_control_changes_only_selector_bytes(self):
        full = PAYLOAD.read_bytes()
        pair = selector_payloads(full)
        self.assertEqual(pair[FULL_NAME], full)
        changed = {i for i, (a, b) in enumerate(zip(full, pair[ZERO_NAME])) if a != b}
        self.assertEqual(changed, {0xF988, 0xF989, 0xF98A, 0xF98B, 0xBB20})

    def test_corrupt_or_truncated_payload_rejected(self):
        full = PAYLOAD.read_bytes()
        corrupt = bytearray(full)
        corrupt[0xBB4C] ^= 1  # stock FECS PLM operand must remain intact
        for data in (bytes(corrupt), full[:-1], b""):
            with self.assertRaises(ValueError):
                selector_payloads(data)

    def test_artifact_hash_mismatch_rejected(self):
        with self.assertRaises(ValueError):
            checked(b"wrong firmware", "0" * 64, "test artifact")


class RunArchiveTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="cmp40-run-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.archive = self.root / "driver.run"
        self.marker = self.root / "executed"

    def write_archive(self, members, header=None):
        data = io.BytesIO()
        with tarfile.open(fileobj=data, mode="w") as archive:
            for name, content in members:
                info = tarfile.TarInfo(name)
                info.size = len(content)
                archive.addfile(info, io.BytesIO(content))
        compressed = subprocess.run(["zstd", "-q", "-c"], input=data.getvalue(),
                                    capture_output=True, check=True).stdout
        if header is None:
            header = f"#!/bin/sh\nskip=4\ntouch '{self.marker}'\n".encode()
        self.archive.write_bytes(header + compressed)

    def test_extract_without_executing_header_or_writing_tar_paths(self):
        outside = self.root.parent / (self.root.name + "-outside")
        self.write_archive([("../" + outside.name, b"unrelated"),
                            ("firmware/gsp_tu10x.bin", b"firmware fixture")])
        self.assertEqual(extract_member(self.archive, "firmware/gsp_tu10x.bin"),
                         b"firmware fixture")
        self.assertFalse(self.marker.exists())
        self.assertFalse(outside.exists())
        self.assertEqual({p.name for p in self.root.iterdir()}, {"driver.run"})

    def test_duplicate_and_missing_members_rejected(self):
        self.write_archive([("firmware/gsp_tu10x.bin", b"one"),
                            ("firmware/gsp_tu10x.bin", b"two")])
        with self.assertRaisesRegex(ValueError, "duplicate package member"):
            extract_member(self.archive, "firmware/gsp_tu10x.bin")
        with self.assertRaisesRegex(ValueError, "missing package member"):
            extract_member(self.archive, "LICENSE")

    def test_invalid_header_and_payload_rejected(self):
        for data in (b"#!/bin/sh\n", b"#!/bin/sh\nskip=0\n",
                     b"#!/bin/sh\nskip=16385\n",
                     b"#!/bin/sh\nskip=100\n",
                     b"#!/bin/sh\nskip=3\nnot zstd",
                     b"#!/bin/sh\nskip=4\n" + b"x" * (1024 * 1024) + b"\n"):
            with self.subTest(data_length=len(data)):
                self.archive.write_bytes(data)
                with self.assertRaises(ValueError):
                    extract_member(self.archive, "LICENSE")

    def test_truncated_zstd_stream_rejected(self):
        self.write_archive([("LICENSE", b"license fixture")])
        self.archive.write_bytes(self.archive.read_bytes()[:-4])
        with self.assertRaises((ValueError, tarfile.TarError)):
            extract_member(self.archive, "LICENSE")

    def test_official_default_and_legacy_cache_selection(self):
        manifest = json.loads(MANIFEST.read_text())
        official = manifest["source_artifacts"][1]
        legacy = manifest["legacy_firmware_package"]
        self.assertEqual(inputs(manifest, self.root)[1], official)
        (self.root / legacy["filename"]).touch()
        self.assertEqual(inputs(manifest, self.root)[1], legacy)
        (self.root / official["filename"]).touch()
        self.assertEqual(inputs(manifest, self.root)[1], official)


if __name__ == "__main__":
    unittest.main()
