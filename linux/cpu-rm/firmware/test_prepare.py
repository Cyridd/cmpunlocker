# SPDX-License-Identifier: GPL-2.0-only
"""Pin the production payload and the two-operand negative control."""
import unittest
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


if __name__ == "__main__":
    unittest.main()
