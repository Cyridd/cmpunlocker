#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Run selected functions from the project's C EFI on host data, without firmware access.

The radix adapter replaces its sole WBINVD instruction with a host no-op.
Allocation uses calloc, Print is disabled, and register/protocol reads are
replaced by input snapshots. No firmware blob or external project is read.
"""
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

EFI = Path(__file__).resolve().parents[3] / "tools/unlock40x"
sys.path.insert(0, str(EFI))
from verify_cpu_rm_selectors import function

SOURCE = (EFI / "unlock40x_v70.c").read_text()

COMMON = r"""
#include <stdint.h>
#include <inttypes.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
typedef uint8_t UINT8;
typedef uint16_t UINT16;
typedef uint32_t UINT32;
typedef int32_t INT32;
typedef uint64_t UINT64;
typedef uintptr_t UINTN;
typedef uint64_t EFI_STATUS;
typedef uint16_t CHAR16;
typedef void VOID;
typedef void *EFI_HANDLE;
typedef int BOOLEAN;
#define TRUE 1
#define FALSE 0
#define EFI_SUCCESS 0
#define EFI_NOT_READY 6
#define EFI_DEVICE_ERROR 7
#define EFI_ERROR(s) ((s) != EFI_SUCCESS)
#define SetMem(p,n,v) memset(p,v,n)
#define CopyMem memcpy
#define CompareMem memcmp
#define Print(...) ((void)0)
#define CPU_RM_HANDOFF
#define STAGE_CONTROL 9
#define HANDOFF_PRELOAD_DEFAULT
"""


def constants(*names):
    return "\n".join(
        re.search(r"^#define\s+" + name + r"\s+[^\n]+", SOURCE, re.M).group()
        for name in names
    ) + "\n"


def metadata_program():
    start = SOURCE.index("typedef struct {", SOURCE.index("Field-for-field replica"))
    end = SOURCE.index("} GspFwWprMeta;", start) + len("} GspFwWprMeta;")
    return (
        constants("GSP_FW_WPR_META_MAGIC", "GSP_FW_WPR_META_REVISION", "V67_SIZE")
        + SOURCE[start:end]
        + "\nstatic UINTN cmp40_stockSig;\n"
        + function(SOURCE, "static void\nbuild_wpr_meta(")
        + r"""
int main(void) {
    UINT64 radix, signature, boot;
    unsigned stock;
    _Static_assert(sizeof(GspFwWprMeta) == 256, "metadata ABI");
    while (scanf("%" SCNu64 " %" SCNu64 " %" SCNu64 " %u",
                 &radix, &signature, &boot, &stock) == 4) {
        GspFwWprMeta meta;
        cmp40_stockSig = stock;
        build_wpr_meta(&meta, radix, 0x1bfadc8, signature,
                       0x200000000ULL, boot, 0x6000);
        if (fwrite(&meta, 1, sizeof(meta), stdout) != sizeof(meta)) return 1;
    }
    return ferror(stdin) ? 1 : 0;
}
"""
    )


def radix_program():
    body = function(SOURCE, "static EFI_STATUS u40x_build_radix(")
    instruction = '__asm__ volatile("wbinvd" ::: "memory");'
    if body.count(instruction) != 1:
        raise RuntimeError("radix cache-maintenance adapter no longer matches")
    body = body.replace(instruction, "/* Host-only: no GPU DMA to synchronize. */")
    if "__asm" in body:
        raise RuntimeError("unexpected assembly in host radix reference")
    return r"""
static EFI_STATUS alloc_fwsec_buffer(UINTN pages, UINT64 *physical) {
    void *buffer = calloc(pages, 4096);
    *physical = (uintptr_t)buffer;
    return buffer ? EFI_SUCCESS : EFI_DEVICE_ERROR;
}
""" + body + r"""
int main(void) {
    UINT64 size, device_base;
    unsigned zero_image;
    while (scanf("%" SCNu64 " %" SCNu64 " %u", &size, &device_base, &zero_image) == 3) {
        UINT8 *image = malloc(size);
        UINT64 tab = 0, leaves = (size + 4095) / 4096;
        UINT64 tables = (2 + (leaves + 511) / 512) * 4096;
        if (!image || !size || size > 0x1bfadc8) return 1;
        for (UINT64 i = 0; i < size; ++i) image[i] = zero_image ? 0 : i % 251;
        if (u40x_build_radix((uintptr_t)image, size, &tab)) return 1;
        /* Normalize host allocation addresses into the caller's DMA band;
         * preserve every table entry and meaningful image byte verbatim. */
        UINT64 *entries = (UINT64 *)(uintptr_t)tab;
        for (UINT64 i = 0; i < tables / 8; ++i)
            if (entries[i]) entries[i] = entries[i] - tab + device_base;
        UINTN bytes = tables + size;
        if (fwrite((void *)(uintptr_t)tab, 1, bytes, stdout) != bytes) return 1;
        free((void *)(uintptr_t)tab);
        free(image);
    }
    return ferror(stdin) ? 1 : 0;
}
"""


def options_program():
    prelude = r"""
typedef struct { const CHAR16 *LoadOptions; UINT32 LoadOptionsSize; } EFI_LOADED_IMAGE_PROTOCOL;
static EFI_LOADED_IMAGE_PROTOCOL image;
static int u40x_li_guid;
static EFI_STATUS handle_protocol(EFI_HANDLE handle, void *guid, void **out) {
    (void)handle; (void)guid; *out = &image; return EFI_SUCCESS;
}
static struct { EFI_STATUS (*HandleProtocol)(EFI_HANDLE, void *, void **); } services = {handle_protocol};
static __typeof__(services) *BS = &services;
typedef enum {
    U40X_HANDOFF_STOCK, U40X_HANDOFF_FLR,
    U40X_HANDOFF_PRELOAD, U40X_HANDOFF_GSP_RESET
} U40X_HANDOFF_MODE;
"""
    bodies = "\n".join(function(SOURCE, declaration) for declaration in (
        "static BOOLEAN u40x_has_load_option(",
        "static U40X_HANDOFF_MODE u40x_handoff_mode(",
        "static BOOLEAN u40x_stage_options_valid(",
    ))
    return prelude + bodies + r"""
static UINTN decode(const char *hex, UINT8 *out) {
    if (!strcmp(hex, "-") || !strcmp(hex, "_")) return 0;
    UINTN length = strlen(hex);
    if (length % 2) exit(1);
    for (UINTN i = 0; i < length / 2; ++i) {
        unsigned value;
        if (sscanf(hex + 2 * i, "%2x", &value) != 1) exit(1);
        out[i] = value;
    }
    return length / 2;
}
int main(void) {
    char options[4097];
    while (scanf("%4096s", options) == 1) {
        UINT16 raw_options[2048] = {0};
        image.LoadOptions = raw_options;
        image.LoadOptionsSize = decode(options, (UINT8 *)raw_options);
        EFI_HANDLE handle = (void *)1;
        INT32 result[2] = {
            u40x_stage_options_valid(handle) && u40x_handoff_mode(handle) == U40X_HANDOFF_PRELOAD,
            u40x_has_load_option(handle, L"--return-to-bootloader") ||
                u40x_has_load_option(handle, L"--return-to-grub") ||
                u40x_has_load_option(handle, L"--return-to-limine")
        };
        if (fwrite(result, 1, sizeof(result), stdout) != sizeof(result)) return 1;
    }
    return ferror(stdin) ? 1 : 0;
}
"""


def state_program():
    names = (
        "REG_FEAT_OVR_SM_SPD", "REG_FEAT_OVR_SM_SPD_1", "REG_FEAT_OVR_PLM",
        "REG_PFB_MMU_WPR2_HI", "REG_PFB_MMU_WPR2_LO", "NV_PSEC_BASE",
        "SEC2_CPUCTL", "SEC2_MAILBOX0", "NV_PFALCON_FALCON_CPUCTL_HALTED_TRUE",
        "VAL_SS0_UNLOCKED", "VAL_SS1_UNLOCKED",
        "FW40_WPR2_HI_UP", "FW40_WPR2_LO_UP",
    )
    body = "\n".join(function(SOURCE, declaration) for declaration in (
        "static BOOLEAN u40x_mmio_unavailable(UINT32 v)",
        "static EFI_STATUS u40x_stage_check_locked(",
        "static EFI_STATUS u40x_stage_check_booter(",
    ))
    prelude = r"""
typedef struct { UINT32 (*read32)(UINTN offset); } U40X_SOFT_IO;
static UINT32 snapshot[7];
static UINT32 read32(UINTN offset) {
    switch (offset) {
    case REG_FEAT_OVR_SM_SPD: return snapshot[0];
    case REG_FEAT_OVR_SM_SPD_1: return snapshot[1];
    case REG_PFB_MMU_WPR2_LO: return snapshot[2];
    case REG_PFB_MMU_WPR2_HI: return snapshot[3];
    case SEC2_CPUCTL: return snapshot[4];
    case SEC2_MAILBOX0: return snapshot[5];
    case REG_FEAT_OVR_PLM: return snapshot[6];
    default: abort();
    }
}
"""
    return constants(*names) + prelude + body + r"""
int main(void) {
    const U40X_SOFT_IO io = {read32};
    while (scanf("%" SCNu32 " %" SCNu32 " %" SCNu32 " %" SCNu32
                 " %" SCNu32 " %" SCNu32 " %" SCNu32,
                 snapshot, snapshot+1, snapshot+2, snapshot+3,
                 snapshot+4, snapshot+5, snapshot+6) == 7) {
        UINT32 result[2] = {
            u40x_stage_check_locked(&io), u40x_stage_check_booter(&io)
        };
        if (fwrite(result, 1, sizeof(result), stdout) != sizeof(result)) return 1;
    }
    return ferror(stdin) ? 1 : 0;
}
"""


def main():
    mode = sys.argv[1]
    programs = {
        "metadata": metadata_program,
        "radix": radix_program,
        "options": options_program,
        "state-full": state_program,
        "state-zero": state_program,
    }
    program = COMMON + programs[mode]()
    if mode == "state-zero":
        program = program.replace("#define STAGE_CONTROL 9", "#define STAGE_CONTROL 10")
    with tempfile.TemporaryDirectory(prefix="cmp40-rust-parity-") as temporary:
        folder = Path(temporary)
        source, binary = folder / "reference.c", folder / "reference"
        source.write_text(program)
        # Match the EFI build's -fno-builtin. With -fshort-wchar, Clang can
        # fold the CHAR16 length loop into host wcslen (32-bit wchar_t ABI).
        subprocess.run([
            os.environ.get("CC", "gcc"), "-std=gnu11", "-O2", "-Wall", "-Wextra",
            "-Werror", "-fshort-wchar", "-fno-builtin", str(source), "-o", str(binary),
        ], check=True)
        subprocess.run([str(binary)], stdin=sys.stdin.buffer,
                       stdout=sys.stdout.buffer, check=True)


if __name__ == "__main__":
    main()
