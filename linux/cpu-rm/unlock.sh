#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-2.0-only
# Explicit pre-driver unlock. No unbind, reset, PCI rescan or background task.
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
usage() {
    echo "Usage: sudo $0 --gpu 0000:01:00.0 [--load-nvidia] [--gen2 prepare|retrain] [--gen2-policy rmw|windows]"
    echo "The target must be an unbound 10de:1f0b; firmware/module must be installed."
}
die() { echo "cmp40 CPU-RM: $*" >&2; exit 1; }
gpu= load_nvidia=0 gen2=off gen2_policy=rmw
while (($#)); do
    case "$1" in
        --gpu) (($# >= 2)) || die "--gpu needs a PCI address"; gpu=$2; shift 2 ;;
        --load-nvidia) load_nvidia=1; shift ;;
        --gen2) (($# >= 2)) || die "--gen2 needs prepare or retrain"; gen2=$2; shift 2 ;;
        --gen2-policy) (($# >= 2)) || die "--gen2-policy needs rmw or windows"; gen2_policy=$2; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) usage; die "unknown argument: $1" ;;
    esac
done
[[ $gpu =~ ^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$ ]] || die "specify the full lowercase PCI address with --gpu"
[[ $gen2 == off || $gen2 == prepare || $gen2 == retrain ]] || die "--gen2 needs prepare or retrain"
[[ $gen2_policy == rmw || $gen2_policy == windows ]] || die "--gen2-policy needs rmw or windows"
[[ $gen2 != off || $gen2_policy == rmw ]] || die "--gen2-policy requires --gen2"
((EUID == 0)) || die "run as root"
device=/sys/bus/pci/devices/$gpu
[[ -d $device ]] || die "PCI device $gpu does not exist"
[[ $(cat "$device/vendor") == 0x10de && $(cat "$device/device") == 0x1f0b ]] || die "target is not CMP40HX (10de:1f0b)"
[[ ! -L $device/driver ]] || die "a driver owns $gpu; boot with the GPU unbound before using this tool"
[[ ! -d /sys/module/cmp40_preboot ]] || die "cmp40_preboot is already loaded; inspect its status before another operation"
if ((load_nvidia)); then
    [[ ! -d /sys/module/nvidia ]] || die "NVIDIA is already loaded; boot without NVIDIA to select CPU-RM"
    [[ $(modinfo -F license nvidia) == NVIDIA ]] || die "--load-nvidia requires the proprietary kernel module"
fi
modprobe cmp40_preboot target="$gpu" mode=unlock cpu_rm_handoff=1 stock_handoff=1 small_wpr_heap=1 \
    gen2="$gen2" gen2_policy="$gen2_policy"
parameters=/sys/module/cmp40_preboot/parameters
[[ $(cat "$parameters/unlock_error") == 0 && $(cat "$parameters/resources_retained") == N ]] ||
    die "unlock failed or DMA resources are retained; read dmesg and power off before loading NVIDIA"
echo "Pre-driver unlock completed on $gpu; native driver/workload verification is still required."
if [[ $gen2 != off ]]; then
    gen2_error=$(cat "$parameters/gen2_error")
    [[ $(cat "$parameters/gen2_device_ok") == Y ]] || die "Gen2 experiment did not confirm a healthy unlocked device; read dmesg before loading NVIDIA"
    [[ $gen2_error == 0 || $gen2_error == -110 ]] || die "Gen2 preparation failed (errno=$gen2_error); inspect the register readbacks before loading NVIDIA"
    echo "Gen2 experiment: errno=$gen2_error Link Status=$(cat "$parameters/gen2_link_status") (decimal)."
fi
if ((load_nvidia)); then
    modprobe nvidia NVreg_EnableGpuFirmware=0
    firmware=$(cmp40_gsp_setting) || die "cannot read NVIDIA's GSP setting from sysfs or /proc"
    [[ $firmware == 0 ]] || die "NVIDIA did not select GSP OFF"
    echo "Proprietary NVIDIA loaded with GSP OFF. Verify with nvidia-smi and a compute workload."
fi
