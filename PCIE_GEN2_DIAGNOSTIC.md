# CMP 40HX PCIe Gen2 diagnostic patch

`0002-cmp40hx-pcie2-diagnostic.patch` is a complete replacement for
`0002-cmp40hx-pcie2-unlock.patch`. Do not apply both patches to the same source
tree.

The diagnostic variant performs the normal Gen2 policy setup and retrain, but
also records the endpoint XVE/BAR0 mirrors and the PCI configuration-space
view at each important phase.

The retrain sequence disables the upstream link for 200 ms, reapplies the
Gen2 target to both sides, re-enables the link, waits 50 ms, and then asserts
Retrain Link. This full Link Disable cycle is required by at least one Intel
Alder Lake root port. Running it from userspace after driver initialization
can detach the GSP device, so it is performed during early driver startup.

## Install

Use a clean NVIDIA 610.57.04 source tree or let `install.sh` download one:

```bash
sudo ./install.sh --pcie-diagnostic
```

For an already downloaded clean source archive/tree:

```bash
sudo ./install.sh --no-download --pcie-diagnostic
```

The installer applies this order:

```text
0001-cmp40hx-unlock.patch
0002-cmp40hx-pcie2-diagnostic.patch
0003-cmp40hx-rebar-unlock.patch
```

As with the normal unlock, perform a full cold power-off after installation.

## Collected state

The diagnostic prefix is:

```text
CMP40_PCIE_GEN2_DIAG_V1
```

Each major phase produces one BAR0 line and one PCI configuration-space line:

```text
before_ovr
after_ovr_immediate
after_ovr_50ms
link_disabled
after_target_writes
link_enabled
retrain_pass or retrain_fail
```

The BAR0 line records:

| Field | BAR0 offset | Meaning |
|---|---:|---|
| `OVR` | `0x8872c` | XVE override/control written with value `6` |
| `CAP` | `0x88084` | Endpoint Link Capabilities mirror |
| `LNK` | `0x88088` | Endpoint Link Control/Status mirror |
| `CAP2` | `0x880a4` | Supported Link Speeds Vector mirror |
| `CTL2` | `0x880a8` | Link Control 2/Status 2 mirror |
| `MISC1` | `0x8841c` | Private PCIe policy/override state |
| `CFG` | `0x8c040` | Internal maximum-rate policy |
| `PL` | `0x8c1c0` | Physical-link rate policy |
| `CYA` | `0x8c2c0` | Includes the `DIS_G2` control bit |

For a working Gen2 x16 endpoint, the important PCI configuration values are
expected to end approximately as follows:

```text
GPU_CAP=00453d02
GPU_LNKSTA=1102
GPU_CAP2=00000006
GPU_CTL2=0002
```

The failing system reported the stock Gen1 capability state:

```text
GPU_CAP=00453d01
GPU_LNKSTA=1101
GPU_CAP2=00000002
GPU_CTL2=0001
```

## Collect the report

After the cold boot, collect the complete ordered diagnostic log:

```bash
sudo dmesg | grep -E 'CMP40_PCIE_GEN2_(DIAG_V1|V2)'
```

Also record the GPU VBIOS, subsystem ID, motherboard BIOS and final PCIe
capabilities:

```bash
nvidia-smi --query-gpu=vbios_version --format=csv,noheader
lspci -Dnn -s 01:00.0
sudo dmidecode -s bios-version
sudo dmidecode -s bios-release-date
sudo lspci -Dvv -s 01:00.0 | grep -E 'LnkCap:|LnkSta:|LnkCap2:|LnkCtl2:'
```

Replace `01:00.0` if the CMP 40HX uses another BDF.

The currently known working ASUS `1043:8804` comparison system uses VBIOS
`90.06.67.00.06`. Another known ASUS image is `90.06.67.00.04`; recording the
failing card's exact version will show whether VBIOS revision correlates with
the XVE capability state.

The Alder Lake failure was reproduced and then resolved on the same ASUS
subsystem. The successful final state was Gen2 x16 (`LnkCap2=00000006`, `LnkSta=5GT/s x16`). 
This points to the root-port retrain sequence, rather than a VBIOS revision difference, as the relevant platform dependency.

## Interpretation

- If `OVR` does not retain `00000006`, the XVE override write was rejected or
  overwritten.
- If `OVR=00000006` but `CAP/CAP2/CTL2` remain at Gen1, the current policy
  writes are insufficient to materialize the endpoint Gen2 capability state.
  Compare `MISC1` between working and failing systems.
- If the mirrors become Gen2 and later return to Gen1, GSP/RM or another late
  policy path is restoring the restriction.
- If the endpoint advertises Gen2 but retraining still ends at `1101`, only
  then investigate link training, platform timing and physical-layer errors.

On the affected Alder Lake system, a userspace Link Disable sequence succeeded:
set upstream `LNKCTL.LD`, wait 200 ms, rewrite both Gen2 target speeds, clear
`LD`, wait 50 ms, then set `Retrain Link`. The kernel patch now performs this
same sequence during `nv_start_device`; runtime unbind/retrain after the
driver is active is not supported and may make `nvidia-smi` report
`Unknown Error`.

This patch is diagnostic instrumentation, not a new bypass. It adds read-only
state capture around the existing `OVR=6`, PCI target-speed writes and root-port
retrain sequence.
