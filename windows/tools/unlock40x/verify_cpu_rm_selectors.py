#!/usr/bin/env python3
"""Exercise the EFI's initial and final selector checks with host MMIO mocks."""
import os
from pathlib import Path
import re
import subprocess
import tempfile


def function(source, declaration):
    match = re.search(re.escape(declaration) + r"[^;{]*\{", source)
    start, end = match.start(), match.end()
    depth = 1
    while depth:
        depth += (source[end] == "{") - (source[end] == "}")
        end += 1
    return source[start:end]


def main():
    source = Path(__file__).with_name("unlock40x_v70.c").read_text()
    names = (
        "REG_FEAT_OVR_SM_SPD", "REG_FEAT_OVR_SM_SPD_1", "REG_FEAT_OVR_PLM",
        "REG_PFB_MMU_WPR2_HI", "REG_PFB_MMU_WPR2_LO", "NV_PSEC_BASE", "SEC2_CPUCTL",
        "SEC2_MAILBOX0", "NV_PFALCON_FALCON_CPUCTL_HALTED_TRUE",
        "VAL_SS0_UNLOCKED", "VAL_SS1_UNLOCKED", "FW40_WPR2_HI_UP",
        "FW40_WPR2_LO_UP",
    )
    constants = "\n".join(
        re.search(r"^#define\s+" + name + r"\s+[^\n]+", source, re.M).group()
        for name in names
    )
    functions = "\n".join(function(source, declaration) for declaration in (
        "static BOOLEAN u40x_mmio_unavailable(UINT32 v)",
        "static EFI_STATUS u40x_stage_check_locked(",
        "static EFI_STATUS u40x_stage_check_booter(",
    ))
    program = """
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
typedef uint32_t UINT32;
typedef uintptr_t UINTN;
typedef int BOOLEAN;
typedef uint64_t EFI_STATUS;
#define EFI_SUCCESS 0
#define EFI_NOT_READY 6
#define EFI_DEVICE_ERROR 7
typedef struct { UINT32 (*read32)(UINTN offset); } U40X_SOFT_IO;
""" + constants + "\n" + functions + """
static struct { UINT32 ss0, ss1, hi, lo, cpu, mb0, plm; } state;
static unsigned checks;
static UINT32 read32(UINTN offset) {
    switch (offset) {
    case REG_FEAT_OVR_SM_SPD: return state.ss0;
    case REG_FEAT_OVR_SM_SPD_1: return state.ss1;
    case REG_PFB_MMU_WPR2_HI: return state.hi;
    case REG_PFB_MMU_WPR2_LO: return state.lo;
    case SEC2_CPUCTL: return state.cpu;
    case SEC2_MAILBOX0: return state.mb0;
    case REG_FEAT_OVR_PLM: return state.plm;
    default: abort();
    }
}
static const U40X_SOFT_IO io = { read32 };
static void expect(EFI_STATUS actual, EFI_STATUS expected, const char *label) {
    ++checks;
    if (actual != expected) {
        fprintf(stderr, "%s: SS=%08x/%08x WPR2=%08x/%08x: status=%llu expected=%llu\\n",
                label, state.ss0, state.ss1, state.hi, state.lo,
                (unsigned long long)actual, (unsigned long long)expected);
        exit(1);
    }
}
int main(void) {
    static const UINT32 initial[][2] = {
        {0, 0}, {0x40020000U, 7}, /* issue #22 */
        {0x00400016U, 7}, /* observed after Linux -> Windows */
        {VAL_SS0_UNLOCKED, 0}, {0, VAL_SS1_UNLOCKED},
        {VAL_SS0_UNLOCKED, 7}, {0x00400016U, VAL_SS1_UNLOCKED},
        {0x11111111U, 0x22222222U},
    };
    static const UINT32 unavailable[] = {0xFFFFFFFFU, 0xBADF0000U, 0xBADF0016U};
    UINTN i, field;
    state.lo = 0x1FFFFE00U;
    for (i = 0; i < sizeof(initial) / sizeof(initial[0]); ++i) {
        state.ss0 = initial[i][0]; state.ss1 = initial[i][1];
#ifdef CPU_RM_HANDOFF
        expect(u40x_stage_check_locked(&io), EFI_SUCCESS, "initial CPU-RM selectors");
#else
        expect(u40x_stage_check_locked(&io), i ? EFI_NOT_READY : EFI_SUCCESS,
               "research control keeps zero baseline");
#endif
    }
    state.ss0 = VAL_SS0_UNLOCKED; state.ss1 = VAL_SS1_UNLOCKED;
    expect(u40x_stage_check_locked(&io), EFI_NOT_READY, "already fully unlocked");
    state.ss0 = state.ss1 = 0;
    state.hi = FW40_WPR2_HI_UP; state.lo = FW40_WPR2_LO_UP;
    expect(u40x_stage_check_locked(&io), EFI_NOT_READY, "WPR2 still up");
    state.hi = 0;
    expect(u40x_stage_check_locked(&io), EFI_NOT_READY, "mixed WPR2 range");
    for (field = 0; field < 4; ++field) {
        for (i = 0; i < sizeof(unavailable) / sizeof(unavailable[0]); ++i) {
            state.ss0 = state.ss1 = state.hi = 0; state.lo = 0x1FFFFE00U;
            switch (field) {
            case 0: state.ss0 = unavailable[i]; break;
            case 1: state.ss1 = unavailable[i]; break;
            case 2: state.hi = unavailable[i]; break;
            case 3: state.lo = unavailable[i]; break;
            }
            expect(u40x_stage_check_locked(&io), EFI_DEVICE_ERROR, "unavailable MMIO");
        }
    }
    state.hi = 0; state.lo = 0x1FFFFE00U;
    state.cpu = NV_PFALCON_FALCON_CPUCTL_HALTED_TRUE;
    state.mb0 = 0; state.plm = 0xFFFFFF8FU;
    state.ss0 = STAGE_CONTROL == 9 ? VAL_SS0_UNLOCKED : 0;
    state.ss1 = STAGE_CONTROL == 9 ? VAL_SS1_UNLOCKED : 0;
    expect(u40x_stage_check_booter(&io), EFI_SUCCESS, "exact final selectors");
    state.ss0 ^= 1;
    expect(u40x_stage_check_booter(&io), EFI_NOT_READY, "wrong final SS0");
    state.ss0 ^= 1; state.ss1 ^= 1;
    expect(u40x_stage_check_booter(&io), EFI_NOT_READY, "wrong final SS1");
    state.ss0 = 0x40020000U; state.ss1 = 7;
    expect(u40x_stage_check_booter(&io), EFI_NOT_READY, "unchanged initial pair is not success");
    printf("stage=%d: %u checks passed\\n", STAGE_CONTROL, checks);
    return 0;
}
"""
    with tempfile.TemporaryDirectory(prefix="cmp40-selectors-") as temporary:
        folder = Path(temporary)
        cfile = folder / "selectors.c"
        cfile.write_text(program)
        for cpu_rm in (True, False):
            for stage in (9, 10):
                binary = folder / f"selectors-{cpu_rm}-{stage}.exe"
                command = [os.environ.get("CC", "gcc"), "-std=c11", "-Wall",
                           "-Wextra", "-Werror", f"-DSTAGE_CONTROL={stage}"]
                if cpu_rm:
                    command.append("-DCPU_RM_HANDOFF")
                subprocess.run(command + [str(cfile), "-o", str(binary)], check=True)
                subprocess.run([str(binary)], check=True)
    print("CPU-RM selector checks PASS: nonzero initial values; exact final state; MMIO/WPR2 guards")


if __name__ == "__main__":
    main()
