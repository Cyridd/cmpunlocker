#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-2.0-only

# GRID/vGPU builds may expose this only in /proc, without module parameters.
cmp40_gsp_setting() {
    local sys_file=${1:-/sys/module/nvidia/parameters/NVreg_EnableGpuFirmware}
    local proc_file=${2:-/proc/driver/nvidia/params}
    if [[ -r $sys_file ]]; then
        cat -- "$sys_file"
    elif [[ -r $proc_file ]]; then
        awk '$1 == "EnableGpuFirmware:" { print $2; found=1; exit }
             END { if (!found) exit 1 }' "$proc_file"
    else
        return 1
    fi
}
