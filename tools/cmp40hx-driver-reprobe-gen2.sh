#!/usr/bin/env bash
# Optional recovery helper for CMP 40HX cards whose first Gen2 pass leaves
# the endpoint capability vector at Gen1 (LnkCap2 bit 0x04 is absent).
#
# This performs a normal NVIDIA driver unbind/bind cycle. It does not toggle
# PCIe Link Disable and it is deliberately not installed or enabled by the
# project's normal installer.
set -euo pipefail

usage() {
    cat <<'EOF'
Usage: sudo ./tools/cmp40hx-driver-reprobe-gen2.sh \
    --confirm-driver-reprobe [--gpu DOMAIN:BUS:DEVICE.FN]

Options:
  --gpu BDF                    CMP 40HX PCI address, for example 0000:01:00.0
  --confirm-driver-reprobe     Required: authorize an NVIDIA driver reset
  --skip-usage-check           Skip the /dev/nvidia* open-file check (headless only)
  --timeout SEC                Timeout for each sysfs operation (default: 30)
  -h, --help                   Show this help

The helper only runs when the endpoint is a CMP 40HX and LnkCap2 does not
advertise Gen2. It requires a healthy nvidia-smi before starting. A driver
unbind can interrupt display/compute clients and may require a cold power-off
if the subsequent bind fails.
EOF
}

die() {
    printf 'error: %s\n' "$*" >&2
    exit 1
}

GPU=''
CONFIRMED=0
SKIP_USAGE_CHECK=0
SYSFS_TIMEOUT=30

while [[ $# -gt 0 ]]; do
    case "$1" in
        --gpu)
            [[ $# -ge 2 ]] || { usage >&2; exit 2; }
            GPU="$2"
            shift 2
            ;;
        --gpu=*)
            GPU="${1#--gpu=}"
            shift
            ;;
        --confirm-driver-reprobe)
            CONFIRMED=1
            shift
            ;;
        --skip-usage-check)
            SKIP_USAGE_CHECK=1
            shift
            ;;
        --timeout)
            [[ $# -ge 2 ]] || { usage >&2; exit 2; }
            SYSFS_TIMEOUT="$2"
            shift 2
            ;;
        --timeout=*)
            SYSFS_TIMEOUT="${1#--timeout=}"
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

[[ ${EUID} -eq 0 ]] || die 'run as root (for example: sudo ...).'
[[ ${CONFIRMED} -eq 1 ]] || {
    printf '%s\n' \
        'error: this will reset the NVIDIA driver for one GPU and may interrupt display/compute clients.' \
        '       pass --confirm-driver-reprobe after stopping GPU workloads.' >&2
    exit 2
}
[[ "$SYSFS_TIMEOUT" =~ ^[1-9][0-9]*$ ]] || die '--timeout must be a positive integer.'

for tool in lspci setpci readlink basename sleep nvidia-smi timeout; do
    command -v "$tool" >/dev/null 2>&1 || die "missing required tool: $tool"
done

if [[ -z "$GPU" ]]; then
    mapfile -t GPUS < <(lspci -Dnn 2>/dev/null |\
        awk 'tolower($0) ~ /\[10de:1f0b\]/ {print $1}')
    [[ ${#GPUS[@]} -eq 1 ]] || {
        printf 'error: expected exactly one CMP 40HX; use --gpu DOMAIN:BUS:DEVICE.FN\n' >&2
        printf 'found: %s\n' "${GPUS[*]:-none}" >&2
        exit 2
    }
    GPU="${GPUS[0]}"
fi

[[ "$GPU" =~ ^[0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2}\.[0-7]$ ]] ||
    die "invalid GPU BDF: $GPU"
GPU="${GPU,,}"

lspci -Dnn -s "$GPU" 2>/dev/null | grep -qi '\[10de:1f0b\]' ||
    die "$GPU is not an NVIDIA CMP 40HX (10de:1f0b)."

GPU_PATH="/sys/bus/pci/devices/$GPU"
[[ -d "$GPU_PATH" ]] || die "GPU sysfs path does not exist: $GPU_PATH"
[[ -L "$GPU_PATH/driver" ]] || die "$GPU is not currently bound to a driver."
DRIVER_PATH="$(readlink -f "$GPU_PATH/driver")"
DRIVER_NAME="$(basename "$DRIVER_PATH")"
[[ "$DRIVER_NAME" == 'nvidia' ]] ||
    die "$GPU is bound to '$DRIVER_NAME', not the NVIDIA driver."

[[ -e "$DRIVER_PATH/unbind" ]] || die 'NVIDIA driver unbind interface is unavailable.'
[[ -e "$DRIVER_PATH/bind" ]] || die 'NVIDIA driver bind interface is unavailable.'
UNBIND_PATH="$DRIVER_PATH/unbind"
BIND_PATH="$DRIVER_PATH/bind"

read_pci_hex() {
    local reg="$1"
    local value
    value="$(setpci -s "$GPU" "$reg" 2>/dev/null)" || return 1
    value="${value//[[:space:]]/}"
    [[ "$value" =~ ^[[:xdigit:]]+$ ]] || return 1
    printf '%s' "${value,,}"
}

hex_to_dec() {
    local value="${1,,}"
    printf '%u' "$((16#$value))"
}

read_snapshot() {
    local cap2 ctl2 sta
    cap2="$(read_pci_hex CAP_EXP+2c.L)" || return 1
    ctl2="$(read_pci_hex CAP_EXP+30.W)" || return 1
    sta="$(read_pci_hex CAP_EXP+12.W)" || return 1
    printf 'CAP2=%s CTL2=%s LNKSTA=%s\n' "$cap2" "$ctl2" "$sta"
}

snapshot_values() {
    local cap2="$1" sta="$2"
    local cap2_num sta_num
    cap2_num="$(hex_to_dec "$cap2")"
    sta_num="$(hex_to_dec "$sta")"
    printf '%u %u %u %u' \
        "$cap2_num" \
        "$((sta_num & 0xf))" \
        "$(( (sta_num >> 4) & 0x3f ))" \
        "$(( (cap2_num & 0x4) != 0 ))"
}

wait_for_driver_absent() {
    local i
    for ((i = 0; i < SYSFS_TIMEOUT; i++)); do
        [[ ! -L "$GPU_PATH/driver" ]] && return 0
        sleep 1
    done
    return 1
}

wait_for_driver_bound() {
    local i
    for ((i = 0; i < SYSFS_TIMEOUT; i++)); do
        if [[ -L "$GPU_PATH/driver" ]] &&
           [[ "$(basename "$(readlink -f "$GPU_PATH/driver")")" == 'nvidia' ]]; then
            return 0
        fi
        sleep 1
    done
    return 1
}

sysfs_write() {
    local value="$1"
    local path="$2"
    timeout "${SYSFS_TIMEOUT}s" bash -c \
        'printf "%s\\n" "$1" > "$2"' _ "$value" "$path"
}

printf 'CMP 40HX driver reprobe recovery\n'
printf 'GPU: %s\n' "$GPU"
printf 'VBIOS (all visible NVIDIA devices):\n'
nvidia-smi --query-gpu=pci.bus_id,vbios_version --format=csv,noheader 2>/dev/null ||
    printf '  unavailable\n'
printf 'Initial PCIe state: '
INITIAL_SNAPSHOT="$(read_snapshot)" || die 'unable to read PCIe capability registers.'
printf '%s\n' "$INITIAL_SNAPSHOT"

read -r INITIAL_CAP2 INITIAL_CTL2 INITIAL_LNKSTA <<< "$INITIAL_SNAPSHOT"
read -r CAP2_NUM SPEED WIDTH HAS_GEN2 <<< \
    "$(snapshot_values "${INITIAL_CAP2#CAP2=}" "${INITIAL_LNKSTA#LNKSTA=}")"

if (( SPEED >= 2 )); then
    printf 'The link is already active at Gen%u x%u; no reprobe needed.\n' \
        "$SPEED" "$WIDTH"
    exit 0
fi
if (( HAS_GEN2 != 0 )); then
    die "endpoint already advertises Gen2 (CAP2=${INITIAL_CAP2#CAP2=}) but link is not Gen2; use the normal diagnostic path."
fi

if ! nvidia-smi >/dev/null 2>&1; then
    die 'nvidia-smi is not responding; do not start a driver reprobe from an unhealthy state.'
fi

if (( SKIP_USAGE_CHECK == 0 )); then
    command -v fuser >/dev/null 2>&1 || die \
        'fuser is required for the safety check; install psmisc or use --skip-usage-check only on a headless recovery console.'

    NVIDIA_NODES=()
    for node in /dev/nvidiactl /dev/nvidia-modeset /dev/nvidia-drm \
                /dev/nvidia-uvm /dev/nvidia-uvm-tools /dev/nvidia[0-9]*; do
        [[ -e "$node" ]] && NVIDIA_NODES+=("$node")
    done
    if [[ ${#NVIDIA_NODES[@]} -gt 0 ]]; then
        HOLDERS="$(fuser "${NVIDIA_NODES[@]}" 2>/dev/null || true)"
        [[ -z "${HOLDERS//[[:space:]]/}" ]] || die \
            "NVIDIA device nodes are in use (PIDs: ${HOLDERS//$'\\n'/ }); stop GPU workloads/display services or use --skip-usage-check on a headless console."
    fi

    COMPUTE_PIDS="$(nvidia-smi --query-compute-apps=pid --format=csv,noheader,nounits 2>/dev/null |\
        sed '/^[[:space:]]*$/d' || true)"
    [[ -z "${COMPUTE_PIDS//[[:space:]]/}" ]] || die \
        "NVIDIA compute processes are active: ${COMPUTE_PIDS//$'\\n'/ }; stop them first."
else
    printf 'WARNING: usage checks were skipped; this is appropriate only from a headless recovery console.\n' >&2
fi

printf 'The endpoint lacks the Gen2 capability bit (CAP2 bit 0x04).\n'
printf 'Performing NVIDIA driver unbind -> 3 s wait -> bind.\n'

UNBOUND=0
cleanup() {
    local rc="$1"
    if (( UNBOUND != 0 )) && [[ ! -L "$GPU_PATH/driver" ]]; then
        printf 'cleanup: attempting to re-bind %s to nvidia\n' "$GPU" >&2
        if sysfs_write "$GPU" "$BIND_PATH"; then
            printf 'cleanup: bind request sent; verify nvidia-smi after the script exits\n' >&2
        else
            printf 'cleanup: bind request failed; a cold power-off may be required\n' >&2
        fi
    fi
    exit "$rc"
}
trap 'cleanup $?' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

UNBOUND=1
if ! sysfs_write "$GPU" "$UNBIND_PATH"; then
    die "driver unbind failed or timed out after ${SYSFS_TIMEOUT}s."
fi
wait_for_driver_absent || die \
    "driver did not detach within ${SYSFS_TIMEOUT}s; no bind was attempted."
printf 'Driver detached. Waiting 3 seconds before rebind.\n'
sleep 3

if ! sysfs_write "$GPU" "$BIND_PATH"; then
    die "driver bind failed or timed out after ${SYSFS_TIMEOUT}s."
fi
wait_for_driver_bound || die \
    "NVIDIA driver did not rebind within ${SYSFS_TIMEOUT}s."
UNBOUND=0
printf 'Driver rebound. Waiting for nvidia-smi.\n'

NVIDIA_READY=0
for ((i = 0; i < SYSFS_TIMEOUT; i++)); do
    if nvidia-smi >/dev/null 2>&1; then
        NVIDIA_READY=1
        break
    fi
    sleep 1
done
(( NVIDIA_READY != 0 )) || die \
    "nvidia-smi did not recover within ${SYSFS_TIMEOUT}s; a cold power-off may be required."

FINAL_SNAPSHOT="$(read_snapshot)" || die 'unable to read final PCIe capability registers.'
printf 'Final PCIe state: %s\n' "$FINAL_SNAPSHOT"
read -r FINAL_CAP2 FINAL_CTL2 FINAL_LNKSTA <<< "$FINAL_SNAPSHOT"
read -r FINAL_CAP2_NUM FINAL_SPEED FINAL_WIDTH FINAL_HAS_GEN2 <<< \
    "$(snapshot_values "${FINAL_CAP2#CAP2=}" "${FINAL_LNKSTA#LNKSTA=}")"
lspci -Dvv -s "$GPU" | grep -E 'LnkCap:|LnkSta:|LnkCap2:|LnkCtl2:|LnkSta2:' || true

if (( FINAL_HAS_GEN2 != 0 && FINAL_SPEED >= 2 )); then
    printf 'SUCCESS: endpoint advertises Gen2 and the link is active at Gen%u x%u.\n' \
        "$FINAL_SPEED" "$FINAL_WIDTH"
    nvidia-smi
    exit 0
fi

printf '%s\n' \
    'FAIL: the driver recovered, but Gen2 was not confirmed.' \
    '      Keep the diagnostic dmesg and consider a cold power-off before retrying.' >&2
exit 1
