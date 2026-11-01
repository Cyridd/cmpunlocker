#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-2.0-only
# Explicit one-shot Gen2 policy reapplication on idle proprietary CPU-RM.
set -euo pipefail
source "$(dirname -- "${BASH_SOURCE[0]}")/common.sh"
usage() { echo "Usage: sudo $0 --gpu 0000:01:00.0 [--module /path/cmp40_preboot.ko]"; }
die() { printf 'cmp40 CPU-RM Gen2: %s\n' "$*" >&2; exit 1; }
gpu= module=
while (($#)); do
    case "$1" in
        --gpu) (($# >= 2)) || die "--gpu needs a PCI address"; gpu=$2; shift 2 ;;
        --module) (($# >= 2)) || die "--module needs a path"; module=$2; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) usage; die "unknown argument: $1" ;;
    esac
done
[[ $gpu =~ ^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$ ]] || die "specify the full lowercase PCI address with --gpu"
((EUID == 0)) || die "run as root"
for tool in modinfo modprobe insmod rmmod nvidia-smi fuser setpci; do
    command -v "$tool" >/dev/null || die "$tool is required"
done
device=/sys/bus/pci/devices/$gpu
[[ -d $device && $(<"$device/vendor") == 0x10de && $(<"$device/device") == 0x1f0b ]] || die "target is not CMP40HX"
[[ $(basename -- "$(readlink -f "$device/driver")") == nvidia ]] || die "NVIDIA does not own the target"
[[ -r /proc/driver/nvidia/version ]] && ! grep -q 'Open Kernel Module' /proc/driver/nvidia/version || die "requires the proprietary NVIDIA module"
[[ ! -d /sys/module/nvidia_drm && ! -d /sys/module/nvidia_modeset ]] || die "stop NVIDIA display users before the Gen2 experiment"
[[ ! -d /sys/module/cmp40_preboot ]] || die "cmp40_preboot is already loaded; inspect its result before another operation"
firmware=$(cmp40_gsp_setting) || die "cannot read NVIDIA's GSP setting from sysfs or /proc"
[[ $firmware == 0 ]] || die "requires GSP OFF"
report=$(nvidia-smi -i "$gpu" -q) || die "NVIDIA is not responding"
grep -q 'GSP Firmware Version[[:space:]]*:[[:space:]]*N/A' <<< "$report" || die "NVIDIA-SMI did not confirm GSP OFF"
status=$(setpci -s "$gpu" CAP_EXP+12.W)
[[ $status =~ ^[[:xdigit:]]{4}$ && $status != ffff ]] || die "cannot read live Link Status"
# An already trained link needs only reads, even with active GPU clients.
if (( (16#$status & 15) == 2 && (16#$status & 0x3f0) != 0 && (16#$status & 0x800) == 0 )); then
    printf 'CMP40HX already has an active Gen2 link; no writes needed.\n'
    exit 0
fi
selected_module=${module:-cmp40_preboot}
[[ $(modinfo -F name "$selected_module") == cmp40_preboot &&
   $(modinfo -F vermagic "$selected_module") == "$(uname -r) "* ]] || die "pre-driver module/kernel mismatch"
modinfo -p "$selected_module" | grep -q '^mode:.*gen2_runtime' || die "rebuild cmp40_preboot with runtime Gen2 support"
nodes=()
for node in /dev/nvidiactl /dev/nvidia-modeset /dev/nvidia-uvm /dev/nvidia-uvm-tools /dev/nvidia[0-9]*; do
    [[ ! -e $node ]] || nodes+=("$node")
done
[[ ${#nodes[@]} -gt 0 ]] || die "NVIDIA device nodes are missing"
holders=$(fuser "${nodes[@]}" 2>/dev/null || true)
[[ -z ${holders//[[:space:]]/} ]] || die "GPU clients are active (PIDs: $holders)"
monitor_pid= loaded=0
monitor_log=$(mktemp)
cleanup() {
    if ((loaded)); then rmmod cmp40_preboot || printf 'Could not unload the diagnostic module.\n' >&2; fi
    if [[ -n $monitor_pid ]]; then
        kill "$monitor_pid" 2>/dev/null || true
        wait "$monitor_pid" 2>/dev/null || true
    fi
    rm -f -- "$monitor_log"
}
trap cleanup EXIT
# A temporary NVML client keeps RM initialized across the policy change.
nvidia-smi -i "$gpu" --query-gpu=pci.bus_id --format=csv,noheader -lms 500 > "$monitor_log" 2>&1 &
monitor_pid=$!
for attempt in {1..40}; do
    kill -0 "$monitor_pid" || die "NVML monitor stopped"
    [[ ! -s $monitor_log ]] || break
    sleep 0.1
done
[[ -s $monitor_log ]] || die "NVML monitor did not initialize"
if [[ -n $module ]]; then
    insmod "$module" target="$gpu" mode=gen2_runtime gen2=retrain gen2_policy=rmw
else
    modprobe cmp40_preboot target="$gpu" mode=gen2_runtime gen2=retrain gen2_policy=rmw
fi
loaded=1
parameters=/sys/module/cmp40_preboot/parameters
error=$(<"$parameters/gen2_error")
[[ $(<"$parameters/resources_retained") == N && $(<"$parameters/gen2_device_ok") == Y && $error == 0 ]] ||
    die "Gen2 failed (errno=$error); inspect GEN2_RUNTIME_RESULT in dmesg"
nvidia-smi -i "$gpu" >/dev/null || die "native NVIDIA health check failed after Gen2"
printf 'CMP40HX Gen2 trained successfully. Verify a transfer and compute workload.\n'
