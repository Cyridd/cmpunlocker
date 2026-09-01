#!/usr/bin/env bash
# Research reproducer only. Do not install as a service.
set -euo pipefail

usage() {
    echo "Usage: $0 --i-understand-gsp-may-be-lost [--gpu DOMAIN:BDF]"
}

GPU=""
CONFIRMED=0
while [[ $# -gt 0 ]]; do
    case "$1" in
        --gpu)
            [[ $# -ge 2 ]] || { usage >&2; exit 2; }
            GPU="$2"
            shift 2
            ;;
        --i-understand-gsp-may-be-lost)
            CONFIRMED=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            usage >&2
            exit 2
            ;;
    esac
done

[[ ${EUID} -eq 0 ]] || { echo "error: run as root (sudo)" >&2; exit 2; }
for tool in lspci setpci readlink dirname basename sleep nvidia-smi; do
    command -v "$tool" >/dev/null 2>&1 || { echo "error: missing $tool" >&2; exit 2; }
done

[[ ${CONFIRMED} -eq 1 ]] || {
    echo "error: this can detach GSP/RM and require a cold power-off" >&2
    echo "       pass --i-understand-gsp-may-be-lost" >&2
    exit 2
}

if [[ -z "$GPU" ]]; then
    mapfile -t GPUS < <(lspci -Dnn 2>/dev/null | awk 'tolower($0) ~ /\[10de:1f0b\]/ {print $1}')
    [[ ${#GPUS[@]} -eq 1 ]] || {
        echo "error: expected one CMP 40HX; use --gpu DOMAIN:BDF" >&2
        printf 'found: %s\n' "${GPUS[*]:-none}" >&2
        exit 2
    }
    GPU="${GPUS[0]}"
fi

[[ "$GPU" =~ ^[0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-7]$ ]] || {
    echo "error: invalid GPU BDF: $GPU" >&2
    exit 2
}
GPU="${GPU,,}"
if ! lspci -Dnn -s "$GPU" 2>/dev/null | grep -qi '\[10de:1f0b\]'; then
    echo "error: $GPU is not an NVIDIA CMP 40HX (10de:1f0b)" >&2
    exit 1
fi
if ! nvidia-smi >/dev/null 2>&1; then
    echo "error: nvidia-smi is not responding; load a healthy NVIDIA driver first" >&2
    exit 1
fi
GPU_PATH="/sys/bus/pci/devices/$GPU"
[[ -e "$GPU_PATH" ]] || { echo "error: GPU not present: $GPU" >&2; exit 1; }
UP_PATH="$(dirname "$(readlink -f "$GPU_PATH")")"
UP="$(basename "$UP_PATH")"
[[ "$UP" =~ ^[0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-7]$ ]] || {
    echo "error: upstream bridge not found: $UP" >&2
    exit 1
}

echo "GPU=$GPU UP=$UP"
echo "WARNING: no GPU workload; Link Disable may detach GSP/RM."
link_disabled=0
cleanup() {
    if [[ ${link_disabled} -eq 1 ]]; then
        setpci -s "$UP" CAP_EXP+10.W=0000:0010 >/dev/null 2>&1 || true
        echo "cleanup: attempted to clear Link Disable" >&2
    fi
}
trap cleanup EXIT INT TERM

echo "initial state"
setpci -s "$GPU" CAP_EXP+30.W CAP_EXP+2c.W
setpci -s "$UP" CAP_EXP+30.W CAP_EXP+2c.W
echo "set Gen2 target"
setpci -s "$GPU" CAP_EXP+30.W=0002:000f
setpci -s "$UP" CAP_EXP+30.W=0002:000f
setpci -s "$GPU" CAP_EXP+30.W
setpci -s "$UP" CAP_EXP+30.W
echo "disable upstream link for 200 ms"
link_disabled=1
setpci -s "$UP" CAP_EXP+10.W=0010:0010
sleep 0.2
echo "reapply targets, enable link, retrain"
setpci -s "$GPU" CAP_EXP+30.W=0002:000f
setpci -s "$UP" CAP_EXP+30.W=0002:000f
setpci -s "$UP" CAP_EXP+10.W=0000:0010
link_disabled=0
sleep 0.05
setpci -s "$UP" CAP_EXP+10.W=0020:0020
sleep 3
lspci -vv -s "$GPU" | grep -E 'LnkCap:|LnkSta:|LnkCap2:|LnkCtl2:|LnkSta2:' || true
if nvidia-smi >/dev/null 2>&1; then
    echo "nvidia-smi: device is responding"
    exit 0
fi
echo "nvidia-smi: device is not responding; cold power-off may be required" >&2
exit 1
