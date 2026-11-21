#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Host adapters for the C reserved-page policy and PTIMER arithmetic."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile

from c_reference import COMMON, SOURCE, constants, function


def allocation():
    prelude = r"""
typedef uint64_t EFI_PHYSICAL_ADDRESS;
#define AllocateAddress 2
#define AllocateMaxAddress 1
#define EfiReservedMemoryType 0
#define uefi_call_wrapper(fn,argc,...) (fn)(__VA_ARGS__)
static UINT32 failure_mask, calls;
static UINT64 starts[2], sizes[2];
static unsigned occupied;
static int overlaps(UINT64 p, UINT64 n) {
    for (unsigned i=0; i<occupied; ++i)
        if (p < starts[i]+sizes[i] && starts[i] < p+n) return 1;
    return 0;
}
static EFI_STATUS allocate_pages(unsigned kind, unsigned type, UINTN pages, UINT64 *p) {
    if (type != EfiReservedMemoryType) abort();
    UINT64 event[] = {0,kind,pages,*p};
    if (fwrite(event,sizeof(event),1,stdout) != 1) exit(1);
    if (failure_mask & (1U << calls++)) return EFI_DEVICE_ERROR;
    UINT64 bytes = (UINT64)pages*4096;
    if (kind == AllocateMaxAddress) {
        *p = 0x70000000ULL - bytes;
        while (overlaps(*p,bytes)) *p -= bytes;
    }
    if (overlaps(*p,bytes)) return EFI_DEVICE_ERROR;
    starts[occupied]=*p; sizes[occupied++]=bytes;
    return EFI_SUCCESS;
}
static struct { EFI_STATUS (*AllocatePages)(unsigned,unsigned,UINTN,UINT64 *); } services = {allocate_pages};
static __typeof__(services) *BS = &services;
static UINT64 cmp40_next_high_slot;
"""
    bodies = function(SOURCE, "static EFI_STATUS\nalloc_below_4g(") + "\n" + function(SOURCE, "static EFI_STATUS\nalloc_fwsec_buffer(")
    tail = r"""
int main(void) {
    UINTN pages;
    UINT32 first,second;
    while (scanf("%" SCNuPTR " %" SCNu32 " %" SCNu32, &pages,&first,&second) == 3) {
        occupied=0; cmp40_next_high_slot=0x110000000ULL;
        for (unsigned i=0; i<2; ++i) {
            calls=0; failure_mask=i ? second : first;
            UINT64 p=0;
            EFI_STATUS st=alloc_fwsec_buffer(pages,&p);
            UINT64 event[]={1,st,EFI_ERROR(st) ? 0 : p,cmp40_next_high_slot};
            if (fwrite(event,sizeof(event),1,stdout) != 1) return 1;
        }
    }
    return 0;
}
"""
    return COMMON + prelude + bodies + tail


def timer():
    prelude = r"""
typedef struct { UINT16 Year; UINT8 Month,Day,Hour,Minute,Second; } EFI_TIME;
static EFI_TIME rtc;
static unsigned time_ok;
static EFI_STATUS get_time(EFI_TIME *out, void *cap) { (void)cap; *out=rtc; return time_ok ? EFI_SUCCESS : EFI_DEVICE_ERROR; }
static struct { EFI_STATUS (*GetTime)(EFI_TIME *,void *); } runtime = {get_time};
static struct { __typeof__(runtime) *RuntimeServices; } table = {&runtime};
static __typeof__(table) *ST=&table;
#define uefi_call_wrapper(fn,argc,...) (fn)(__VA_ARGS__)
static void mmio_write32(UINTN offset, UINT32 v) {
    UINT32 row[]={offset,v}; if (fwrite(row,sizeof(row),1,stdout) != 1) exit(1);
}
"""
    tail = r"""
int main(void) {
    unsigned y,m,d,h,min,s;
    while (scanf("%u %u %u %u %u %u %u", &y,&m,&d,&h,&min,&s,&time_ok) == 7) {
        rtc=(EFI_TIME){y,m,d,h,min,s}; set_gpu_time();
    }
    return 0;
}
"""
    return COMMON + constants("NV_PTIMER_TIME_0", "NV_PTIMER_TIME_1") + prelude + function(SOURCE,"static void\nset_gpu_time(") + tail


def main():
    with tempfile.TemporaryDirectory(prefix="cmp40-preparation-parity-") as temporary:
        folder=Path(temporary)
        source,binary=folder/"reference.c",folder/"reference"
        source.write_text({"allocation": allocation,"timer":timer}[sys.argv[1]]())
        # The unchanged C timer compares UINTN to the promoted UINT8 Month.
        # Suppress that source warning; retain the exact reference arithmetic.
        subprocess.run([os.environ.get("CC","gcc"),"-std=gnu11","-O2","-Wall","-Wextra","-Werror","-Wno-sign-compare","-fshort-wchar",str(source),"-o",str(binary)],check=True)
        subprocess.run([str(binary)],stdin=sys.stdin.buffer,stdout=sys.stdout.buffer,check=True)


if __name__ == "__main__":
    main()
