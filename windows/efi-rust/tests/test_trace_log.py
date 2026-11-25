# SPDX-License-Identifier: MIT
import unittest
from unittest.mock import patch
from trace_log import transform


class TraceLogTests(unittest.TestCase):
    def test_legacy_byte_round_trip_and_counts(self):
        pair = "R 001100f4 00000000 count=1\nS 00000000 00000064 count=2\n"
        old = ("BEGIN\nTRACE_BEGIN records=20001 omitted_data_ports=7 dropped=0\n"
               + pair * 10000 + "R 001100f4 00000001 count=1\nTRACE_END\nTAIL\n")
        compressed, reports = transform(old, True)
        self.assertIn("REPEAT_PAIR additional=9999\n", compressed)
        self.assertEqual(reports[0]["operations"], 30001)
        self.assertEqual(transform(compressed)[0], old)
        self.assertLess(len(compressed), 400)

    def test_bounded_and_incomplete_inputs_are_refused(self):
        header = ("TRACE_BEGIN version=2 phase=CPU_RM records=2 encoded_records=2 "
                  "operations=2 omitted_data_ports=0 dropped=0\n")
        events = "R 001100f4 00000000 count=1\nS 00000000 00000064 count=1\n"
        good = header + events + "TRACE_END\n"
        transform(good)
        for bad in [good.replace("records=2", "records=131073", 1),
                    good.replace("operations=2", "operations=3"),
                    good.replace("encoded_records=2", "encoded_records=3"),
                    good.replace("version=2", "version=3"),
                    good.replace("count=1", "count=0", 1),
                    good.replace("count=1", "count=4294967296", 1),
                    header + "REPEAT_PAIR additional=1\nTRACE_END\n",
                    header + events + "REPEAT_PAIR additional=999999\nTRACE_END\n",
                    header + events + "REPEAT_PAIR additional=1\nREPEAT_PAIR additional=1\nTRACE_END\n",
                    header + events, header + header, "TRACE_END\n", "TRACE_BEGIN\n", "REPEAT_PAIR\n"]:
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                transform(bad)

    def test_empty_trace_and_maximum_single_rle_count(self):
        old = "TRACE_BEGIN records=0 omitted_data_ports=0 dropped=5\nTRACE_END\n"
        self.assertEqual(transform(transform(old, True)[0])[0], old)
        old = ("TRACE_BEGIN records=1 omitted_data_ports=0 dropped=0\n"
               "C 00000000 00000000 count=4294967295\nTRACE_END\n")
        self.assertEqual(transform(transform(old, True)[0])[0], old)

    def test_total_output_budget_applies_across_blocks(self):
        old = ("TRACE_BEGIN records=200 omitted_data_ports=0 dropped=0\n"
               + "R 001100f4 00000000 count=1\nS 00000000 00000064 count=1\n" * 100
               + "TRACE_END\n")
        compressed = transform(old, True)[0]
        with patch("trace_log.MAX_OUTPUT_BYTES", len(old.encode()) + 1):
            transform(compressed)
            with self.assertRaises(ValueError):
                transform(compressed * 2)


if __name__ == "__main__":
    unittest.main()
