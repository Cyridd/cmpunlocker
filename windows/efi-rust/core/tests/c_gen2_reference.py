#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Compare masked writes with actual repository C policy (XP3G path excluded)."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from c_reference import COMMON, function

ROOT = Path(__file__).resolve().parents[4]
SOURCE = (ROOT / "linux/cpu-rm/kernel/gen2_core.h").read_text()
PREFIX = r"""
#define __iomem
#define BIT(n) (1u<<(n))
#define ARRAY_SIZE(a) (sizeof(a)/sizeof((a)[0]))
#define ENODEV 19
#define EIO 5
#define bool BOOLEAN
#define pci_info(...) ((void)0)
typedef UINT32 u32;
struct pci_dev { int unused; };
static UINT32 registers[0x500000/4];
#define readl(p) (*(UINT32 *)(p))
static void writel(UINT32 value, VOID *p) {
    UINT32 row[2] = {(UINT32)((UINT8 *)p-(UINT8 *)registers),value};
    *(UINT32 *)p=value;
    if(fwrite(row,sizeof(row),1,stdout)!=1) exit(1);
}
#define REG_MISC 0x8841c
#define REG_CYA 0x8c2c0
#define REG_CFG 0x8c040
#define REG_PL 0x8c1c0
#define REG_OVR 0x8872c
#define REG_XP_PLM 0x8e1b0
#define REG_XP_VAL 0x8e120
#define REG_XP_OVR 0x8e110
#define REG_SS0 0x409664
#define REG_SS1 0x40966c
"""
MAIN = r"""
int main(void) {
    unsigned adopt;
    UINT32 values[5];
    while(scanf("%u %" SCNu32 " %" SCNu32 " %" SCNu32 " %" SCNu32 " %" SCNu32,
        &adopt,values,values+1,values+2,values+3,values+4)==6) {
        memset(registers,0,sizeof(registers));registers[0]=0x166000a1;
        registers[REG_SS0/4]=0x88888888;registers[REG_SS1/4]=8;
        UINT32 offsets[]={REG_MISC,REG_CYA,REG_CFG,REG_PL,REG_OVR};
        for(unsigned i=0;i<5;++i) registers[offsets[i]/4]=values[i];
        struct pci_dev dev={0};
        if(cmp40_g2_policy(&dev,registers,FALSE,adopt)!=0) return 2;
        UINT32 end=0xffffffff;if(fwrite(&end,sizeof(end),1,stdout)!=1) return 1;
    }
    return ferror(stdin)?1:0;
}
"""


def main():
    bodies = "\n".join(function(SOURCE, declaration) for declaration in (
        "static bool cmp40_g2_bad_reg(", "static bool cmp40_g2_device_ok(",
        "static int cmp40_g2_policy_write(", "static int cmp40_g2_policy("))
    with tempfile.TemporaryDirectory(prefix="cmp40-gen2-reference-") as directory:
        root=Path(directory)
        c=root/"reference.c"
        c.write_text("/* Extracted C functions: SPDX-License-Identifier: GPL-2.0-only */\n"+COMMON+PREFIX+bodies+MAIN)
        binary=root/"reference"
        subprocess.run([os.environ.get("CC","gcc"),"-std=gnu11","-O2","-fno-builtin",str(c),"-o",str(binary)],check=True)
        subprocess.run([str(binary)],check=True)


if __name__ == "__main__":
    main()
