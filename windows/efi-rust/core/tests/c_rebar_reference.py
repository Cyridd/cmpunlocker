#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Replay actual C ReBAR routines on host PCI/MMIO and memory-map models.

Only the hardware/firmware access boundaries are substituted. The two volatile
VRAM loads are replaced with recorded synthetic reads; placement, probes and
rollback are compiled from the unchanged C source. Print arguments still run.
"""
import os
from pathlib import Path
import subprocess
import sys
import tempfile

from c_reference import COMMON, SOURCE, constants, function

MODEL = r"""
typedef intptr_t INTN;
#include <stdarg.h>
static unsigned fault, gBus = 2, gRbAllN = 2, u40x_enc_found;
static UINT32 gpu[64], br[2][2][64], xve[4];
static UINT64 size3, top;
static unsigned map_calls;
static void event(UINT32 kind, UINT32 a, UINT32 b, UINT32 c, UINT32 d) {
    UINT32 row[] = {kind, a, b, c, d};
    if (fwrite(row, sizeof(row), 1, stdout) != 1) exit(1);
}
static UINT32 key(UINTN ri, UINTN b, UINTN d, UINTN f) {
    return (UINT32)((ri << 24) | (b << 16) | (d << 8) | f);
}
static void print_ref(const CHAR16 *fmt, ...) { (void)fmt; }
#undef Print
#define Print(...) print_ref(__VA_ARGS__)
static unsigned xi(UINTN off) {
    switch(off) { case XVE_CYA_OFF: return 0; case XVE_CFG_OFF: return 1;
                 case XVE_CAP_OFF: return 2; case XVE_RBAR_CTL_OFF: return 3; }
    abort();
}
static UINT32 mmio_read32(UINTN off) {
    UINT32 v = xve[xi(off)]; event(0, (UINT32)off, v, 0, 0); return v;
}
static void mmio_write32(UINTN off, UINT32 v) {
    event(1, (UINT32)off, v, 0, 0);
    if (fault == 3 && off == XVE_CFG_OFF) return;
    xve[xi(off)] = v;
    if (off == XVE_CFG_OFF) xve[2] = v & 0x80000000 ? 0x7fc00 : 0x400;
}
static UINT32 rebar_rd(UINTN off) {
    UINT32 v = gpu[off/4];
    if (v == 0xffffffff && (off == 0x14 || off == 0x18 || off == 0x1c || off == 0x20)) {
        UINT64 size = off < 0x1c ? (fault == 4 ? 0x4000000ULL : U40X_REBAR_SIZE) : size3;
        UINT64 mask = size ? ~(size-1) : 0;
        v = (off == 0x14 || off == 0x1c) ? (UINT32)mask | 12 : (UINT32)(mask >> 32);
    }
    if (fault == 18 && off == 0x18 && v != 0xffffffff && gpu[0x14/4] == 12) v ^= 1;
    event(2, (UINT32)off, v, 0, 0); return v;
}
static void rebar_wr(UINTN off, UINT32 v) { event(3, (UINT32)off, v, 0, 0); gpu[off/4] = v; }
static void rb_collect(void) {}
static UINT32 rebar_br_rd(UINTN ri, UINTN b, UINTN d, UINTN f, UINTN off) {
    UINT32 v = 0xffffffff;
    if (ri < 2 && b == 0 && d < 2 && f == (fault == 21 && d == 0 ? 1 : 0)) v = br[ri][d][off/4];
    if (fault == 21 && b == 0 && d == 0 && f == 0) v = off == 0 ? 0x12348086 : off == 0xc ? 0x00800000 : 0;
    event(4, key(ri,b,d,f), (UINT32)off, v, 0); return v;
}
static void rebar_br_wr(UINTN ri, UINTN b, UINTN d, UINTN f, UINTN off, UINT32 v) {
    event(5, key(ri,b,d,f), (UINT32)off, v, 0);
    if (ri < 2 && b == 0 && d < 2) br[ri][d][off/4] = v;
}
static UINT64 rebar_memmap_top(void) { event(6, (UINT32)top, top >> 32, 0, 0); return top; }
static BOOLEAN rebar_addr_in_memmap(UINT64 base, UINT64 size) {
    unsigned hit = fault == 14 || (fault == 13 && map_calls == 0);
    ++map_calls;
    event(7, (UINT32)base, base >> 32, (UINT32)size, size >> 32);
    event(8, hit, 0, 0, 0); return hit;
}
static UINT32 vram_read(UINT64 address) {
    UINT32 v = fault == 17 || (fault == 19 && address % U40X_REBAR_SIZE == 0) ? 0xffffffff : 0x13579bdf;
    event(9, (UINT32)address, address >> 32, v, 0); return v;
}
static void init(void) {
    memset(gpu,0,sizeof(gpu)); memset(br,0xff,sizeof(br)); map_calls=0;
    xve[0]=0x12; xve[1]=0x12340000; xve[2]=0x400; xve[3]=0x6a5;
    gpu[1]=7; gpu[0x14/4]=fault == 1 ? 8 : 0xf400000c; gpu[0x18/4]=0x7f;
    gpu[0x1c/4]=0xf800000c; gpu[0x20/4]=0x7f;
    unsigned root = fault == 22 ? 1 : 0;
    memset(br[root][0],0,sizeof(br[root][0]));
    br[root][0][0]=0x12348086; br[root][0][3]=0x00010000;
    br[root][0][6]=fault == 2 ? 0x30300 : 0x20200;
    br[root][0][1]=7; br[root][0][9]=0xf9f1f401; br[root][0][10]=0x7f; br[root][0][11]=0x7f;
    if (fault == 9) br[root][0][9]=0xffffffff;
    if (fault == 10 || fault == 24) br[root][0][9]=0x0000ffff;
    if (fault == 11) { br[root][0][9]=0xfff1f401; br[root][0][11]=0xffffffff; }
    if (fault == 12 || fault == 20) {
        memset(br[0][1],0,sizeof(br[0][1])); br[0][1][0]=0x43218086;
        br[0][1][3]=0x00010000; br[0][1][6]=0x20100; br[0][1][1]=7;
        br[0][1][9]=0x00110001; br[0][1][10]=8; br[0][1][11]=9;
    }
    if (fault == 20) br[root][0][6]=0x30200; // subordinate differs, secondary stays 2
}
int main(void) {
    while(scanf("%u %" SCNu64 " %" SCNu64, &fault, &size3, &top)==3) {
        init(); u40x_rebar_try(U40X_REBAR_SIZE,U40X_REBAR_SELECTOR);
        event(10,0,0,0,0);
    }
    return ferror(stdin) ? 1 : 0;
}
"""


def relocation_program():
    names = ("XVE_CYA_OFF", "XVE_CFG_OFF", "XVE_CAP_OFF", "XVE_CFG_SEL_MASK",
             "XVE_CFG_ENABLE", "XVE_RBAR_CTL_OFF", "XVE_RBAR_SIZE_MASK",
             "XVE_RBAR_SIZE_SHIFT", "U40X_REBAR_SIZE", "U40X_REBAR_SELECTOR", "U40X_REBAR_ENCSIZE")
    definitions = constants(*names)
    range_type = "typedef struct { UINT64 min, max; BOOLEAN valid; } U40X_REBAR_RANGE;\n"
    functions = "\n".join(function(SOURCE, declaration) for declaration in (
        "static BOOLEAN\nrebar_find_bridge(", "static UINT64\nrebar_probe_bar_size(",
        "static UINT64\nrebar_align_up(", "static BOOLEAN\nrebar_window_conflicts(",
        "static BOOLEAN\nrebar_choose_window(", "static VOID\nu40x_rebar_revert(",
        "static VOID\nu40x_rebar_try("))
    substitutions = {
        "UINT32 v0 = *p0, v1 = *p1;": "UINT32 v0 = vram_read(cand1), v1 = vram_read(cand1 + want / 2);",
    }
    for original, replacement in substitutions.items():
        if functions.count(original) != 1:
            raise RuntimeError("C VRAM reference adapter no longer matches")
        functions = functions.replace(original, replacement)
    stub = "static BOOLEAN rebar_dump_aperture(UINTN ri, U40X_REBAR_RANGE *r) { (void)ri; r->valid=FALSE; return FALSE; }\n"
    prefix, main = MODEL.split("int main(void)", 1)
    return definitions + range_type + prefix + stub + functions + "\nint main(void)" + main


def range_program():
    return ("typedef struct { UINT64 min, max; BOOLEAN valid; } U40X_REBAR_RANGE;\n"
            + function(SOURCE, "static BOOLEAN\nrebar_parse_range(") + r"""
int main(void) {
    unsigned n;
    while(scanf("%u", &n)==1) {
        UINT8 data[65536]; if (n>sizeof(data)) return 2;
        for(unsigned i=0;i<n;++i) { unsigned v; if(scanf("%u",&v)!=1) return 2; data[i]=(UINT8)v; }
        U40X_REBAR_RANGE r={0}; UINTN size=0;
        UINT64 row[4]={0}; row[0]=rebar_parse_range(data,n,&r,&size);
        row[1]=size; row[2]=r.min; row[3]=r.max;
        if(fwrite(row,sizeof(row),1,stdout)!=1) return 1;
    }
    return ferror(stdin) ? 1 : 0;
}
""")


def policy_program():
    return function(SOURCE, "static INT32 u40x_rebar_policy_parse(") + r"""
int main(void) {
    unsigned n;
    while(scanf("%u",&n)==1) {
        UINT8 data[256]; if(n>sizeof(data)) return 2;
        for(unsigned i=0;i<n;++i) { unsigned v; if(scanf("%u",&v)!=1) return 2; data[i]=(UINT8)v; }
        INT32 result=u40x_rebar_policy_parse(data,n);
        if(fwrite(&result,sizeof(result),1,stdout)!=1) return 1;
    }
    return ferror(stdin) ? 1 : 0;
}
"""


def main():
    programs = {"relocate": relocation_program, "range": range_program, "policy": policy_program}
    source = COMMON + programs[sys.argv[1]]()
    with tempfile.TemporaryDirectory(prefix="cmp40-rebar-reference-") as directory:
        root = Path(directory)
        c = root / "reference.c"
        executable = root / "reference"
        c.write_text(source)
        subprocess.run([os.environ.get("CC", "gcc"), "-std=gnu11", "-O2", "-fshort-wchar", "-fno-builtin",
                        str(c), "-o", str(executable)], check=True, stdout=sys.stderr, stderr=sys.stderr)
        subprocess.run([str(executable)], check=True)


if __name__ == "__main__":
    main()
