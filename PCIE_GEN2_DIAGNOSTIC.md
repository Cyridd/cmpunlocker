# CMP 40HX PCIe Gen2 diagnostic patch

`0002-cmp40hx-pcie2-diagnostic.patch` is a complete replacement for
`0002-cmp40hx-pcie2-unlock.patch`. Do not apply both patches to the same source
tree.

The diagnostic variant performs the normal Gen2 policy setup and retrain, but
also records the endpoint XVE/BAR0 mirrors and the PCI configuration-space
view at each important phase.

The kernel retrain sequence sets the Gen2 target on both sides and asserts
Retrain Link on the upstream bridge. It deliberately does not toggle Link
Disable. A full Link Disable experiment can train Gen2 on some Alder Lake
systems, but can detach GSP/RM after driver initialization; see
`PCIE_LINK_DISABLE_AUDIT.md`.

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
after_target_writes
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

The Alder Lake failure was reproduced on the same ASUS subsystem and VBIOS
`90.06.67.00.06`. A manual Link Disable sequence reached Gen2 x16, but the
driver subsequently detached. This is evidence about the physical training
sequence, not a validated kernel integration.

## Confirmed VBIOS `.04` driver reprobe recovery

A community tester repeatedly confirmed the recovery path on an ASUS CMP 40HX
(`1043:8804`) with VBIOS `90.06.67.00.04`, an ASUS PRIME Z370-P and Xeon
E-2174G. On Ubuntu 24.04 with kernel `6.8.0-31` and NVIDIA open modules
`610.57.04`, the normal cold-boot pass left the endpoint at Gen1:

```text
cold boot:
CAP=00453d01 CAP2=00000002 -> RETRAIN_FAIL status=1101
```

After an NVIDIA driver unbind/bind cycle, the second bootstrap materialized the
Gen2 capability and retrained successfully:

```text
after driver unbind/bind:
CAP=00453d02 CAP2=00000006 -> RETRAIN_PASS status=1102
LnkSta: Speed 5GT/s, Width x16
```

The second bootstrap provided the important lifecycle result: its `before_ovr`
phase already saw the materialized capability, before that pass performed its
host-side OVR write:

```text
phase=before_ovr OVR=00000006 CAP=00453d02 CAP2=00000006 CTL2=00200002
phase=retrain_pass CAP=00453d02 CAP2=00000006 status=1102 attempt=1
```

This means that merely extending the first retrain timeout is unlikely to fix
the `.04` behavior. The first bootstrap appears to establish policy state that
does not become visible through the endpoint capability until the driver is
torn down and initialized again. The exact firmware lifecycle boundary remains
unknown, but the recovery was stable across repeated tests.

The tests also confirmed that `nvidia-smi`, the compute unlock state
(`SS0=88888888`, `SS1=00000008`) and ReBAR (`BAR1 Total: 8192 MiB`) remained
healthy after each cycle. This is a repeatable result on one `.04` platform,
not yet a universal production fix. An unbind can fail or block when the
driver has active references, and it can interrupt display or compute clients.

For an explicitly confirmed, one-shot recovery attempt, use the helper from a
root shell after stopping GPU workloads:

```bash
sudo ./tools/cmp40hx-driver-reprobe-gen2.sh --confirm-driver-reprobe
```

When more than one CMP 40HX is present, select the device explicitly:

```bash
sudo ./tools/cmp40hx-driver-reprobe-gen2.sh \
  --confirm-driver-reprobe --gpu 0000:01:00.0
```

The helper only targets a CMP 40HX whose endpoint does not advertise the Gen2
bit in `LnkCap2`; it does not toggle PCIe Link Disable. `--skip-usage-check`
exists only for a headless recovery console where the caller has independently
stopped all GPU users. Verify both the driver state and the final link:

```bash
sudo dmesg | grep -E \
  'CMP40_PCIE_GEN2_(DIAG_V1|V2)|CMP40_COMPUTE_UNLOCK|CMP40_GSP_READY'
sudo lspci -Dvv -s 01:00.0 | grep -E \
  'LnkCap:|LnkSta:|LnkCap2:|LnkCtl2:|LnkSta2:'
nvidia-smi
```

In particular, include the second pass's `before_ovr` state. The combination
`OVR=00000006` with `CAP2=00000002` is no longer conclusive evidence that the
card cannot reach Gen2: the `.04` report shows that a full driver reprobe can
materialize the capability later in the boot lifecycle.

### Experimental systemd boot unit

`tools/cmp40hx-driver-reprobe-gen2.service` packages the confirmed helper as an
experimental boot-time unit. It remains disabled by default and is not
installed by `install.sh`. The unit runs before `display-manager.service` and
`nvidia-persistenced.service`, while the helper retains its normal open-client
check. If another service has already opened the GPU, the helper refuses the
reprobe instead of bypassing that check.

Install the helper and unit without enabling automatic startup:

```bash
sudo install -m 0755 tools/cmp40hx-driver-reprobe-gen2.sh \
  /usr/local/sbin/cmp40hx-driver-reprobe-gen2.sh
sudo install -m 0644 tools/cmp40hx-driver-reprobe-gen2.service \
  /etc/systemd/system/cmp40hx-driver-reprobe-gen2.service
sudo systemctl daemon-reload
```

First stop the display manager and GPU workloads, then test one manual service
start and inspect its complete journal:

```bash
sudo systemctl start cmp40hx-driver-reprobe-gen2.service
sudo journalctl -u cmp40hx-driver-reprobe-gen2.service -b --no-pager
```

Only after the manual service test succeeds should the experimental boot path
be enabled:

```bash
sudo systemctl enable cmp40hx-driver-reprobe-gen2.service
```

After a cold boot, verify the service, PCIe link and GPU health:

```bash
systemctl status cmp40hx-driver-reprobe-gen2.service --no-pager
sudo lspci -Dvv -s 01:00.0 | grep -E \
  'LnkCap:|LnkSta:|LnkCap2:|LnkCtl2:|LnkSta2:'
nvidia-smi
```

Disable and remove the experimental installation with:

```bash
sudo systemctl disable cmp40hx-driver-reprobe-gen2.service
sudo rm -f /etc/systemd/system/cmp40hx-driver-reprobe-gen2.service
sudo rm -f /usr/local/sbin/cmp40hx-driver-reprobe-gen2.sh
sudo systemctl daemon-reload
```

The unit currently supports the helper's single-CMP auto-detection. Systems
with multiple CMP 40HX cards should continue to use the helper manually with
`--gpu` until per-device service instances are tested.

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

On the affected Alder Lake system, userspace Link Disable succeeded physically
but caused GSP/RM detachment. The kernel patch intentionally does not perform
that sequence. Use the explicitly-confirmed driver reprobe helper above for the
`.04` recovery experiment; keep the Link Disable reproducer in
`PCIE_LINK_DISABLE_AUDIT.md` limited to controlled research.

This patch is diagnostic instrumentation, not a new bypass. It adds read-only
state capture around the existing `OVR=6`, PCI target-speed writes and root-port
retrain sequence.
