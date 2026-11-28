#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Execute the project's actual C firmware routines against synthetic host buffers.

No GPU or NVIDIA blob is used. Print evaluates its register arguments, reset
and port callbacks run a host model, and each WBINVD becomes a recorded event.
"""
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

from c_reference import COMMON, SOURCE, function


MODEL = r"""
#include <stdarg.h>
typedef uint64_t EFI_PHYSICAL_ADDRESS;
typedef intptr_t INTN;
#define EFI_TIMEOUT 18
#define EFI_LOAD_ERROR 1
#define EFI_NOT_STARTED 19
#define EFI_ABORTED 21
#define FWSEC_SB_RESTORE_DISABLED
static unsigned fault, zero_control, gsp_started, sec_started, ticks;
static UINT32 regs[0x900000 / 4], mem[4][0x10000 / 4], ports[4];
static void event(UINT32 kind, UINT32 a, UINT32 b) {
    UINT32 row[] = {kind, a, b};
    if (fwrite(row, sizeof(row), 1, stdout) != 1) exit(1);
}
static void print_ref(const CHAR16 *fmt, ...) { (void)fmt; }
#undef Print
#define Print(...) print_ref(__VA_ARGS__)
static void completion_faults(void) {
    switch (fault) {
    case 14: case 15: regs[GSP_CPUCTL/4] = 0; break;
    case 16: regs[GSP_ENGINE/4] = 1; break;
    case 17: regs[GSP_DMACTL/4] = 6; break;
    case 18: regs[GSP_HWCFG2/4] = 0x80001000; break;
    case 19: regs[REG_FEAT_OVR_PLM/4] = 0xffffffff; break;
    case 20: regs[REG_FEAT_OVR_SM_SPD/4] = 1; break;
    case 21: regs[REG_PFB_MMU_WPR2_HI/4] = FW40_WPR2_HI_UP; break;
    case 22: regs[SEC2_CPUCTL/4] = 0xbadf5040; break;
    case 23: regs[GSP_DMACTL/4] = 0xbadf1100; break;
    case 24: regs[GSP_CPUCTL/4] = 0xffffffff; break;
    case 27: regs[REG_FEAT_OVR_SM_SPD_1/4] = 0x18; break;
    case 28: regs[SEC2_MAILBOX1/4] = 0xbadf5040; break;
    }
}
static void completed(void) {
    gsp_started = sec_started = 1;
    regs[REG_FEAT_OVR_SM_SPD/4] = zero_control ? 0 : 0x88888888;
    regs[REG_FEAT_OVR_SM_SPD_1/4] = zero_control ? 0 : 8;
    regs[REG_PFB_MMU_WPR2_HI/4] = 0;
    regs[REG_PFB_MMU_WPR2_LO/4] = 0x1ffffe00;
    regs[SEC2_MAILBOX0/4] = 0;
    regs[SEC2_MAILBOX1/4] = 1;
    completion_faults();
}
static UINT32 mmio_read32(UINTN offset) {
    UINT32 v = regs[offset/4];
    int bank = -1;
    if (offset == GSP_BASE+0x184) bank = 0;
    if (offset == GSP_BASE+0x1c4) bank = 1;
    if (offset == NV_PSEC_BASE+0x184) bank = 2;
    if (offset == NV_PSEC_BASE+0x1c4) bank = 3;
    if (bank >= 0) {
        UINT32 addr = ports[bank] & 0xfffffc;
        v = mem[bank][addr/4];
        if (bank == 2 && (ports[bank] & (1u<<28))) v = 0xdead5ec1;
        if ((fault == 4 && bank == 0) || (fault == 5 && bank == 1 && addr == 0x20) ||
            (fault == 8 && bank == 2 && addr == 0) || (fault == 9 && bank == 3 && addr == 0x200)) v ^= 1;
    }
    if (fault == 3 && offset == GSP_HWCFG) v = 1;
    if (fault == 25 && offset == GSP_HWCFG2) v = 0;
    if (fault == 7 && offset == SEC2_CPUCTL && !sec_started) v = 0;
    event(0, offset, v);
    return v;
}
static void mmio_write32(UINTN offset, UINT32 v) {
    event(1, offset, v);
    if (offset == REG_PFB_MMU_WPR2_LO || offset == REG_PFB_MMU_WPR2_HI) return;
    if ((fault == 10 && offset == SEC2_MAILBOX0 && v != 0xa5a5c3c3 && v != 0x91) ||
        (fault == 11 && offset == SEC2_MAILBOX1)) return;
    regs[offset/4] = v;
    if (fault == 26 && offset == SEC2_BCR_CTRL && v == 0) {
        regs[SEC2_CPUCTL/4] = 0x10; regs[SEC2_DMATRFCMD/4] = 0;
    }
    int bank = -1;
    if (offset == GSP_BASE+0x180) ports[0] = v;
    if (offset == GSP_BASE+0x1c0) ports[1] = v;
    if (offset == NV_PSEC_BASE+0x180) ports[2] = v;
    if (offset == NV_PSEC_BASE+0x1c0) ports[3] = v;
    if (offset == GSP_BASE+0x184) bank = 0;
    if (offset == GSP_BASE+0x1c4) bank = 1;
    if (offset == NV_PSEC_BASE+0x184) bank = 2;
    if (offset == NV_PSEC_BASE+0x1c4) bank = 3;
    if (bank >= 0) {
        mem[bank][(ports[bank]&0xfffffc)/4] = v;
        if (ports[bank] & (1u<<24)) ports[bank] += 4;
    }
    if (offset == GSP_ENGINE && v == 1) {
        regs[GSP_CPUCTL/4] = 0x10;
        regs[GSP_DMACTL/4] = fault == 2 ? 6 : 0;
    }
    if (offset == GSP_CPUCTL && v == 2) {
        gsp_started = 1; ticks = 0; regs[GSP_CPUCTL/4] = 0x10;
        if (fault != 1 && fault != 6) {
            regs[REG_PFB_MMU_WPR2_LO/4] = FW40_WPR2_LO_UP;
            regs[REG_PFB_MMU_WPR2_HI/4] = FW40_WPR2_HI_UP;
        }
    }
    if (offset == SEC2_CPUCTL && v == 2) {
        completed();
        regs[SEC2_CPUCTL/4] = fault == 13 ? 0 : 0x10;
        regs[SEC2_MAILBOX0/4] = fault == 12 ? 0x31 : 0;
    }
}
static void stall(UINTN us) {
    event(2, us, 0);
    if (gsp_started && us == 1000) {
        ++ticks;
        if (fault == 1 && ticks >= 2) {
            regs[REG_PFB_MMU_WPR2_LO/4] = FW40_WPR2_LO_UP;
            regs[REG_PFB_MMU_WPR2_HI/4] = FW40_WPR2_HI_UP;
        }
        if (fault == 14 && ticks >= 2) regs[GSP_CPUCTL/4] = 0x10;
    }
}
static EFI_STATUS start_image(EFI_HANDLE h, void *a, void *b) {
    (void)h; (void)a; (void)b; event(6, 0, 0); return EFI_SUCCESS;
}
static struct { void (*Stall)(UINTN); EFI_STATUS (*StartImage)(EFI_HANDLE, void *, void *); } services = {stall, start_image};
static __typeof__(services) *BS = &services;
#define uefi_call_wrapper(fn,argc,...) (fn)(__VA_ARGS__)
static UINT32 loader_words[0x300/4], fwsec_words[0x9df0/4], booter_words[0xe700/4];
static UINT8 *gsp_bl_tu102 = (UINT8 *)loader_words;
static BOOLEAN u40x_fwsec_prepared, u40x_booter_prepared, u40x_booter_started;
static BOOLEAN u40x_cpu_rm_return;
static EFI_HANDLE u40x_cpu_rm_parent;
#define U40X_STAGE_NAME L"FULL"
static void u40x_seal_output(void) { event(5,0,0); }
typedef struct { UINT32 (*read32)(UINTN); void (*write32)(UINTN,UINT32); void (*stall)(UINTN); } U40X_SOFT_IO;
static void pattern(void *p, UINTN n) { for (UINTN i=0; i<n; ++i) ((UINT8 *)p)[i] = (i*17+3)&255; }
static void initialize(void) {
    memset(regs,0,sizeof(regs)); memset(mem,0,sizeof(mem)); memset(ports,0,sizeof(ports));
    gsp_started = sec_started = ticks = 0;
    regs[GSP_CPUCTL/4] = regs[SEC2_CPUCTL/4] = 0x10;
    regs[GSP_HWCFG2/4] = 0x80000000;
    regs[GSP_HWCFG/4] = 255;
    regs[REG_PFB_MMU_WPR2_LO/4] = 0x1ffffe00;
    regs[REG_FEAT_OVR_PLM/4] = 0xffffff8f;
    regs[SEC2_MAILBOX0/4] = 0x91;
    regs[SEC2_DMATRFBASE/4] = 0x1234;
    if (fault == 26 || fault == 29 || fault == 31) {
        regs[SEC2_DMATRFCMD/4] = 0xbadf5040;
        if (fault != 31) regs[SEC2_CPUCTL/4] = 0xbadf5040;
    }
    if (fault == 30) regs[SEC2_CPUCTL/4] = 0xffffffff;
    pattern(loader_words,sizeof(loader_words)); pattern(fwsec_words,sizeof(fwsec_words)); pattern(booter_words,sizeof(booter_words));
}
"""


def program(mode):
    names = re.findall(r"^#define\s+([A-Za-z_][A-Za-z_0-9]*)\s+", SOURCE, re.M)
    wanted = [name for name in names if name.startswith(("REG_", "SEC2_", "GSP_", "FW40_", "FWSEC_", "BL_DESC_", "BOOTER_", "NV_PSEC", "NV_FALCON2_SEC", "NV_PFALCON_FALCON_", "VAL_SS"))]
    # Retain only single-line object macros, in original dependency order.
    defines = []
    for name in dict.fromkeys(wanted):
        line = re.search(r"^#define\s+" + name + r"\s+[^\n]+", SOURCE, re.M).group()
        if not line.rstrip().endswith("\\"):
            defines.append(line)
    declarations = {
        "prepare": ["static void\ngsp_engine_reset(", "static EFI_STATUS u40x_stage_abort("],
        "fwsec": ["static BOOLEAN\nfalcon_wait_scrub_done(", "static void\nfalcon_wait_reset_ready(", "static BOOLEAN\nfwsec_boot_gsp_40hx("],
        "booter": ["static BOOLEAN\nfalcon_wait_scrub_done(", "static void\nu40x_imem_write(", "static void\nu40x_dmem_write(", "static EFI_STATUS\nbooter_load_tu102_direct("],
        "finish": ["static void u40x_soft_snapshot(", "static EFI_STATUS u40x_stage_abort(", "static EFI_STATUS u40x_stage_wait_fwsec(", "static EFI_STATUS u40x_stage_check_booter(", "static EFI_STATUS u40x_finish_stage("],
    }
    bodies = function(SOURCE, "static BOOLEAN u40x_mmio_unavailable(UINT32 v)") + "\n"
    for declaration in declarations[mode]:
        body = function(SOURCE, declaration)
        body = body.replace('__asm__ volatile("wbinvd" ::: "memory");', "event(3,0,0);")
        if "__asm" in body:
            raise RuntimeError("unadapted assembly in C execution reference")
        if declaration == "static EFI_STATUS u40x_finish_stage(":
            # Compare native unlock completion and terminal handoff only.
            # Exclude the optional post-completion extension in the C source.
            body, count = re.subn(
                r"#ifdef CPU_RM_HANDOFF\n    [^\n]+\(u40x_cpu_rm_parent\);\n.*?\n#endif(?=\n    u40x_seal_output\(\);)",
                "", body, count=1, flags=re.S,
            )
            if count != 1:
                raise RuntimeError("C post-completion extension no longer matches")
        bodies += body + "\n"
    if mode == "prepare":
        start = SOURCE.index("gsp_engine_reset();", SOURCE.index("/* ---------- [8] kill GFW"))
        end = SOURCE.index("/* ---------- [8b]", start)
        # Compile the actual main-entry segment, including its STAGE_CONTROL
        # conditions. Only the outer entry's error label is supplied on host.
        bodies += "static EFI_STATUS prepare(void) { UINTN i;\n" + SOURCE[start:end] + "\nreturn EFI_SUCCESS;\ndone: return EFI_NOT_READY;\n}\n"
    # fwsecPhys is simultaneously a CPU pointer and a DMA address in the C
    # code. AllocateAddress cannot run on host: replace only its pointer cast
    # while preserving the provided numeric address in descriptor fields.
    if mode == "fwsec":
        replacements = {
            "UINT8 *blob = (UINT8 *)(UINTN)fwsecPhys;": "UINT8 *blob = (UINT8 *)fwsec_words;",
            "*(UINT32 *)(UINTN)fwsecPhys": "fwsec_words[0]",
            "*(UINT32 *)(UINTN)(fwsecPhys + FW40_DATA_OFF + 0x10)": "fwsec_words[(FW40_DATA_OFF+0x10)/4]",
        }
        for before, after in replacements.items():
            if before not in bodies:
                raise RuntimeError(f"C FWSEC host adapter no longer matches: {before}")
            bodies = bodies.replace(before, after)
    invocation = {
        "prepare": "result = prepare();",
        "fwsec": "result = fwsec_boot_gsp_40hx(address) ? EFI_SUCCESS : EFI_NOT_READY;",
        "booter": "result = booter_load_tu102_direct(address, (uintptr_t)booter_words);",
        "finish": "completed(); result = u40x_finish_stage(&io, (void *)1);",
    }[mode]
    tail = r"""
int main(void) {
    UINT64 address;
    const U40X_SOFT_IO io = {mmio_read32, mmio_write32, stall};
    (void)io; (void)BS; (void)gsp_bl_tu102; (void)u40x_fwsec_prepared;
    (void)u40x_booter_prepared; (void)u40x_booter_started; (void)u40x_cpu_rm_return;
    (void)u40x_cpu_rm_parent; (void)u40x_seal_output;
    while (scanf("%u %u %" SCNu64, &fault, &zero_control, &address) == 3) {
        initialize();
        EFI_STATUS result;
        INVOCATION
        event(4, result, 0);
        if (fwrite(fwsec_words,sizeof(fwsec_words),1,stdout) != 1) return 1;
    }
    return 0;
}
""".replace("INVOCATION", invocation)
    code = COMMON + "\n" + "\n".join(defines) + "\n" + MODEL + bodies + tail
    # Retain production FULL/ZERO preprocessing; the host accepts one control
    # per process so no alternative stage-control code is exercised.
    if mode == "finish" and sys.argv[2] == "zero":
        code = code.replace("#define STAGE_CONTROL 9", "#define STAGE_CONTROL 10")
    return code


def main():
    with tempfile.TemporaryDirectory(prefix="cmp40-execution-parity-") as temporary:
        folder = Path(temporary)
        source, binary = folder / "reference.c", folder / "reference"
        source.write_text(program(sys.argv[1]))
        subprocess.run([os.environ.get("CC", "gcc"), "-std=gnu11", "-O2", "-Wall", "-Wextra", "-Werror", "-Wno-unused-function", "-fshort-wchar", str(source), "-o", str(binary)], check=True)
        subprocess.run([str(binary)], stdin=sys.stdin.buffer, stdout=sys.stdout.buffer, check=True)


if __name__ == "__main__":
    main()
