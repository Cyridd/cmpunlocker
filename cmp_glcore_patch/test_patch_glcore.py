"""Check binary-patcher rejection paths without redistributing NVIDIA data."""
import os
from pathlib import Path
import struct
import subprocess
import tempfile
import unittest


PROGRAM = struct.pack('<12I',
    0x00000003, 0x1cc00000, 0xb1cc5f00,
    0x9d140113, 0x18c001a2, 0xf18c0300,
    0x00000003, 0x18c00000, 0x318c0301,
    0x00000003, 0x18c00000, 0x318c0300)


def fixture(method=0x68, count=2):
    data = bytearray(64)
    data[:7] = b'\x7fELF\x02\x01\x01'
    struct.pack_into('<HH', data, 16, 3, 62)
    data += PROGRAM
    for _ in range(count):
        data += b'\x48\xb9' + struct.pack('<II', 0x20010e00 | method, 240)
        data += b'\x48\x89\x08'
    return data


class PatcherTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory(prefix='glcore-test-')
        cls.root = Path(cls.temp.name)
        cls.patcher = cls.root / 'patch_glcore'
        subprocess.run([os.environ.get('CXX', 'g++'), '-std=c++17', '-O2',
                        '-Wall', '-Wextra', '-Werror',
                        str(Path(__file__).with_name('patch_glcore.cpp')),
                        '-o', str(cls.patcher)], check=True)

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    def run_patch(self, data, argument='0', reject=False):
        source, output = self.root / 'input.so', self.root / 'output.so'
        source.write_bytes(data)
        output.write_bytes(b'preserve existing output')
        result = subprocess.run([str(self.patcher), str(source), str(output), argument],
                                capture_output=True, text=True)
        self.assertEqual(source.read_bytes(), data)
        if reject:
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(output.read_bytes(), b'preserve existing output')
            return result
        self.assertEqual(result.returncode, 0, result.stderr)
        return output.read_bytes()

    def test_both_slots_and_dword_values(self):
        for method in (0x66, 0x68):
            for argument, value in [('0', 0), ('1', 1), ('010', 10),
                                    ('0xffffffff', 0xffffffff), ('240', 240)]:
                with self.subTest(method=method, argument=argument):
                    data = fixture(method)
                    expected = bytearray(data)
                    pattern = struct.pack('<II', 0x20010e00 | method, 240)
                    for offset in (data.index(pattern), data.rindex(pattern)):
                        struct.pack_into('<I', expected, offset + 4, value)
                    self.assertEqual(self.run_patch(data, argument), expected)

    def test_wrong_match_count(self):
        for count in (0, 1, 3):
            with self.subTest(count=count):
                self.run_patch(fixture(count=count), reject=True)

    def test_mixed_slots(self):
        data = fixture(count=1)
        data += fixture(0x66, 1)[64 + len(PROGRAM):]
        self.run_patch(data, reject=True)

    def test_unrelated_or_changed_program(self):
        data = fixture()
        data[64] ^= 1
        self.run_patch(data, reject=True)

    def test_constants_outside_emitter(self):
        data = fixture()
        data[64 + len(PROGRAM)] = 0
        self.run_patch(data, reject=True)

    def test_invalid_elf(self):
        for offset, value in [(0, 0), (4, 1), (5, 2), (16, 2), (18, 183)]:
            with self.subTest(offset=offset):
                data = fixture()
                data[offset] = value
                self.run_patch(data, reject=True)

    def test_invalid_argument(self):
        for argument in ('-1', '+1', '1junk', '0x', '4294967296',
                         '18446744073709551616'):
            with self.subTest(argument=argument):
                self.run_patch(fixture(), argument, reject=True)

    def test_already_patched(self):
        data = self.run_patch(fixture())
        self.run_patch(data, reject=True)

    def test_input_output_alias(self):
        source, alias = self.root / 'source.so', self.root / 'alias.so'
        data = fixture()
        source.write_bytes(data)
        alias.symlink_to(source)
        result = subprocess.run([str(self.patcher), str(source), str(alias), '0'],
                                capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(source.read_bytes(), data)


if __name__ == '__main__':
    unittest.main()
