#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0-only
"""Regenerate Gen2 patches from a pinned NVIDIA archive and canonical C sources."""
import argparse
import difflib
import hashlib
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile

REPO = Path(__file__).resolve().parents[2]
GSP = 'src/nvidia/src/kernel/gpu/gsp/arch/turing/kernel_gsp_tu102.c'
NV = 'kernel-open/nvidia/nv.c'
INTERFACES = ('src/nvidia/arch/nvalloc/unix/include/os-interface.h',
              'kernel-open/common/inc/os-interface.h')


def replace_once(text, old, new):
    if text.count(old) != 1:
        raise ValueError(f'expected one insertion point: {old!r}')
    return text.replace(old, new)


def generate(tree, diagnostic):
    before = {p: (tree / p).read_text() for p in (GSP, NV, *INTERFACES)}
    after = before.copy()
    declaration = 'void        NV_API_CALL  os_cmp40_gen2(void *os_gpu, NvU32 stage);\n'
    for p in INTERFACES:
        after[p] = replace_once(before[p], 'NV_STATUS   NV_API_CALL  os_alloc_mem',
                                declaration + 'NV_STATUS   NV_API_CALL  os_alloc_mem')
    after[GSP] = replace_once(before[GSP], '#include "os/os.h"',
                              '#include "os/os.h"\n#include "os-interface.h"')
    # Insert inside the successful stock-Booter guard, before the GSP-ready stage.
    after[GSP] = replace_once(after[GSP],
        '                  postPlm, postResetPlm);\n    }',
        '                  postPlm, postResetPlm);\n'
        '        os_cmp40_gen2(pGpu->pOsGpuInfo, 0);\n    }')
    after[GSP] = replace_once(after[GSP],
        '                      CMP40_FECS_FEATURE_OVERRIDE_SM_SPEED_SELECT_1));\n    }',
        '                      CMP40_FECS_FEATURE_OVERRIDE_SM_SPEED_SELECT_1));\n'
        '        os_cmp40_gen2(pGpu->pOsGpuInfo, 1);\n    }')
    include = ('#define CMP40_GEN2_DIAGNOSTIC\n' if diagnostic else '') + '#include "nv-cmp40-gen2.h"\n'
    after[NV] = replace_once(before[NV], '#include <linux/cdev.h>\n',
                             '#include <linux/cdev.h>\n' + include)
    init, = re.findall(r'    if \(!rm_init_adapter\(sp, nv\)\)\n    \{.*?\n    \}',
                       after[NV], re.S)
    after[NV] = replace_once(after[NV], init, init + '\n\n    os_cmp40_gen2(nv, 2);')
    after['kernel-open/nvidia/gen2_core.h'] = (REPO / 'linux/cpu-rm/kernel/gen2_core.h').read_text()
    after['kernel-open/nvidia/nv-cmp40-gen2.h'] = (REPO / 'tools/gen2/open_adapter.h').read_text()
    output = []
    for p in after:
        output.append(f'diff --git a/{p} b/{p}\n')
        output.extend(difflib.unified_diff(before.get(p, '').splitlines(True),
                      after[p].splitlines(True), fromfile='a/' + p if p in before else '/dev/null',
                      tofile='b/' + p))
    # Match the repository's blank-context convention (accepted by GNU patch).
    return ''.join(output).replace('\n \n', '\n\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', required=True)
    parser.add_argument('--source', type=Path, required=True, help='pinned, unmodified .tar.gz archive')
    parser.add_argument('--check', action='store_true', help='compare without writing patches')
    args = parser.parse_args()
    pins = dict(re.findall(r'([\d.]+)\) echo "([0-9a-f]{64})"',
                           (REPO / 'tools/check-linux-patches.sh').read_text()))
    if args.version not in pins:
        parser.error('unsupported driver version')
    digest = hashlib.sha256(args.source.read_bytes()).hexdigest()
    if digest != pins[args.version]:
        parser.error('NVIDIA archive SHA256 does not match the source pin')
    patches = REPO / 'patches' / args.version
    with tempfile.TemporaryDirectory(prefix='cmp40-gen2-generate-') as work:
        with tarfile.open(args.source) as archive:
            archive.extractall(work, filter='data')
        tree, = Path(work).iterdir()
        with (patches / '0001-cmp40hx-unlock.patch').open() as patch:
            subprocess.run(['patch', '-s', '-p1', '-F0'], cwd=tree, stdin=patch, check=True)
        for diagnostic in (False, True):
            dest = patches / ('0002-cmp40hx-pcie2-diagnostic.patch' if diagnostic else
                              '0002-cmp40hx-pcie2-unlock.patch')
            if diagnostic and not dest.exists():
                continue
            output = generate(tree, diagnostic)
            if args.check:
                if dest.read_text() != output:
                    raise SystemExit(f'stale generated patch: {dest}')
            else:
                dest.write_text(output)
            print(f'{"checked" if args.check else "generated"}: {dest.relative_to(REPO)}')


if __name__ == '__main__':
    main()
