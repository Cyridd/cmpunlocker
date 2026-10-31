#!/usr/bin/env python3
"""Compile the actual EFI metadata builder on the host; compare all 256 bytes."""
import os
from pathlib import Path
import struct
import subprocess
import tempfile


def main():
    source = Path(__file__).with_name("unlock40x_v70.c").read_text()
    start = source.index("typedef struct {", source.index("Field-for-field replica"))
    end = source.index("} GspFwWprMeta;", start) + len("} GspFwWprMeta;")
    declaration = source[start:end]
    start = source.index("static void\nbuild_wpr_meta(")
    end = source.index("\n#define WINDOWS_BOOT_PATH", start)
    builder = source[start:end]
    program = """#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <assert.h>
typedef uint8_t UINT8;
typedef uint16_t UINT16;
typedef uint32_t UINT32;
typedef uint64_t UINT64;
#define SetMem(p,n,v) memset(p,v,n)
#define GSP_FW_WPR_META_MAGIC 0xdc3aae21371a60b3ULL
#define GSP_FW_WPR_META_REVISION 1
#define V67_SIZE 0xfa00
static unsigned cmp40_stockSig;
""" + declaration + builder + """
int main(void) {
    GspFwWprMeta m;
    assert(sizeof(m) == 256);
    build_wpr_meta(&m, 0x100000, 0x1bfadc8, 0x200000,
                   0x200000000ULL, 0x300000, 0x6000);
    return fwrite(&m, 1, sizeof(m), stdout) == sizeof(m) ? 0 : 1;
}
"""
    with tempfile.TemporaryDirectory(prefix="cmp40-metadata-") as temporary:
        folder = Path(temporary)
        cfile = folder / "metadata.c"
        cfile.write_text(program)
        outputs = []
        for small in (False, True):
            binary = folder / ("small.exe" if small else "original.exe")
            command = [os.environ.get("CC", "gcc"), "-std=c11", "-Wall", "-Wextra", "-Werror"]
            if small:
                command.append("-DCPU_RM_HANDOFF")
            subprocess.run(command + [str(cfile), "-o", str(binary)], check=True)
            outputs.append(subprocess.check_output([str(binary)]))
    original, small = outputs
    assert len(original) == len(small) == 256
    changed = [offset for offset in range(0, 256, 8)
               if original[offset:offset + 8] != small[offset:offset + 8]]
    assert changed == [88, 96, 112, 120, 128], changed
    expected = {0x58: 0x1fdf00000, 0x60: 0x1fdf00000, 0x70: 0x1fe000000,
                0x78: 0x1fe100000, 0x80: 0xf0000, 0x88: 0x1fe1f0000,
                0xa8: 0x1fff00000, 0xb0: 0x200000000}
    for offset, value in expected.items():
        assert struct.unpack_from("<Q", small, offset)[0] == value, hex(offset)
    print("CPU-RM metadata PASS: exact 960 KiB geometry; only five reservation fields changed")


if __name__ == "__main__":
    main()
