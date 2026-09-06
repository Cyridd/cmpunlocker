# CMP 40HX PCIe Gen3 research report

Status: **Gen3 unlock not achieved.** The validated production path remains
PCIe Gen2 x16. This document records the Gen3 investigation so that future
work can start from the observed hardware boundary instead of repeating the
same runtime experiments.

## Scope and test systems

The target was an ASUS CMP 40HX 8 GB (`10de:1f0b`, subsystem `1043:8804`)
with VBIOS `90.06.67.00.06` and NVIDIA open kernel modules `610.57.04`.
The compute unlock, 8 GiB ReBAR unlock and ordinary Gen2 retrain were already
working before this investigation.

Two host configurations were relevant:

- AMD Ryzen 5 5600G / MSI MPG B550 Gaming Plus: the normal Gen2 path is
  healthy; an experimental Link Disable sequence left the card with POST-like
  fan speed and made `nvidia-smi` lose the GPU.
- Intel Core i3-12100F / MSI MAG B660M MORTAR MAX DDR4 (Alder Lake): the
  upstream port accepted a Gen2 target, and a manual Link Disable sequence
  reached a real Gen2 x16 link, but the live GSP/RM device detached afterward.

The Alder Lake result is important because it separates root-port behavior
from the endpoint capability problem. The upstream port can accept the
requested speed; the CMP endpoint is the side that refuses to advertise Gen3.

## PCIe status values used in this report

The low speed field in the PCIe link status values is the negotiated speed:

| Value | Meaning |
|---|---|
| `0x1` (`LnkSta` shown as `1101`) | Gen1, x16 |
| `0x2` (`LnkSta` shown as `1102`) | Gen2, x16 |
| `0x3` | Gen3 target/status code |

Therefore `status=1102` is a successful Gen2 x16 link status. It is not a
generic retrain handshake code and does not by itself prove that RM/GSP stayed
healthy after a link-disconnect experiment.

## Experimental path

The experiments were kept opt-in through the NVIDIA `RMPcieLinkSpeed`
registry value. The Gen3 candidate path was entered only when the value's
Gen3 selector was requested; the default path remained the known-good Gen2
policy and normal upstream `Retrain Link` operation.

The source-level experiments progressed in this order:

1. Add Gen3/Gen2 automatic selection and a Gen2 fallback.
2. Gate the probe on both endpoint and upstream `LnkCap`/`LnkCap2`.
3. Add readback of the GSP policy registers and the PCI capability mirrors.
4. Match the stock write ordering (`LC2`, physical-link rate, CYA, link
   configuration).
5. Test the endpoint capability shadow and several XVE policy/gate registers.
6. Correct the `LnkCap2` speed-vector mask.
7. Trace writes immediately before the stock Booter and test the earliest
   writable-looking gate.

The corrected PCIe speed vector is important. The individual capability bits
are `0x02` (Gen1), `0x04` (Gen2), and `0x08` (Gen3). A continuous Gen1+Gen2+
Gen3 vector is therefore `0x0e`; the earlier diagnostic request `0x0a`
accidentally omitted Gen2 and was discarded. Fixing that mask did not change
the endpoint's final behavior.

## Earliest observed endpoint state

The pre-Booter trace was taken before the stock GSP Booter load, while the
compute unlock was active:

```text
CMP40_PCIE_AUTO_V1: PRE_BOOTER_CAP
  LCAP=00453d01 LCAP2=00000002 LC2=00000001 LNKSTA=11010140
  XVE2241C=00000081 XVE88708=0987e0ed XVE88050=00000002
  XVE88DCC=80000007 CFG=80045800 PL=00120036
  CYA=068711b3 LTSSM=00000000
```

At this point the endpoint already advertises only Gen1:

- `LCAP=...01`: maximum link speed Gen1;
- `LCAP2=...02`: only the Gen1 speed bit;
- `LC2=...01`: Gen1 target;
- `LNKSTA=...1101`: current Gen1 x16 state.

This is earlier than the host-side retrain and earlier than the final RM/GSP
policy callback. It rules out a simple retrain delay as the cause of the
missing Gen3 capability.

## Capability-shadow write tests

The runtime diagnostic attempted to create the Gen3 capability state expected
by a normal Turing endpoint. The trace immediately after the writes was:

```text
CAP_TRACE phase=after_xve_writes
  LCAP=00453d01 LCAP2=00000002 LC2=00000001
CAP_TRACE phase=after_lcap_write
  LCAP=00453d01 LCAP2=00000002 LC2=00000001
CAP_TRACE phase=after_lcap2_write
  LCAP=00453d01 LCAP2=00000002 LC2=00000001
```

The internal mirrors are at these BAR0 offsets:

| Register | BAR0 offset | Observed behavior |
|---|---:|---|
| `LCAP` (Link Capabilities) | `0x88084` | Read-only in this path |
| `LNKSTA` / control-status mirror | `0x88088` | Status/control related |
| `LCAP2` (supported speed vector) | `0x880a4` | Read-only in this path |
| `LC2` (Link Control 2 / target) | `0x880a8` | Writable target field |

Direct writes to `LCAP` and `LCAP2` were ignored. `LC2` could be changed,
but changing the target field did not create a Gen3 capability or train a
Gen3 link.

## Policy mismatch evidence

With the corrected `0x0e` Gen3 vector, the policy callback still reported a
canonicalized Gen2 endpoint. The two values separated by `/` are the observed
value and the requested value:

```text
POLICY_MISMATCH phase=post-booter mode=Gen3
  OVR=00000001/00000004 VAL=00000000/00200000 HIER=00000001
  PRIV=20340500/20340500 CYA=060711b2/060711b2
  CFG=800c5800/800c5800 PL=00130036/00130036
  XVE2241C=00000081/00000081 XVE88708=0987e1ed/0987e1ed
  XVE88050=00000002/00000000 XVE88DCC=00000007/00000007
  LCAP=00453d02/00453d03 LCAP2=00000006/0000000e
  LC2=00000002/00000003 LTSSM=00000006
```

The same mismatch occurred again at `gsp-ready`. The endpoint accepted the
policy changes that are writable, but canonicalized the capability state to:

- `LCAP=...02`: Gen2 maximum;
- `LCAP2=...06`: Gen1+Gen2 only;
- `LC2=...02`: Gen2 target.

The normal fallback then retrained successfully:

```text
CMP40_PCIE_AUTO_V1: RETRAIN_PASS mode=Gen2 status=1102 attempt=1
```

The final system state was healthy (`nvidia-smi` worked) and showed a real
5.0 GT/s x16 link.

On the AMD test host the upstream bridge reported Gen3 support while the CMP
endpoint reported only Gen2 (`GPU=2GT/s cap2=06`, `UP=3GT/s cap2=0e`). The
automatic policy correctly treated this as Gen2-only instead of issuing an
unsafe Gen3 request.

The Alder Lake userspace experiments also ruled out several lower-level
training hypotheses. For both tested de-emphasis settings (`-3.5 dB` and
`-6.0 dB`), the endpoint stayed at Gen1 and immediately restored
`LnkCtl2.Target Link Speed` to 2.5 GT/s. No AER, `RxErr`, `BadDLLP` or Replay
errors appeared. These tests do not prove that every reference-clock issue is
impossible, but they do show that de-emphasis or ordinary link-training
timing was not the limiting factor in this card.

## XVE gate tests

The diagnostic path tested the private values most strongly suggested by the
CMP initialization sequence:

| BAR0 register | Test result |
|---|---|
| `0x2241c` | Requested policy bit read back correctly |
| `0x88708` | Requested Gen3-related value read back correctly |
| `0x88050` | CMP value remained `0x00000002`; a pre-Booter write of `0` was rejected |
| `0x88dcc` | Requested value read back correctly |

The decisive pre-Booter test was:

```text
PRE_BOOTER_88050 BEFORE=00000002 AFTER=00000002
```

This shows that the obvious CMP-specific gate is already locked by the time
the stock Booter path begins. It is not a missing host delay or an ordinary
PCI configuration-space permission issue.

## VBIOS comparison

The supplied CMP VBIOS devinit stream contains these PCIe-related operations:

```text
R[0x0880a8] |= 0x00000002
R[0x088050] = 0x00000002
R[0x088708] ... |= 0x08000000
R[0x08c2c0] ... |= 0x00800002
R[0x088dcc] = 0x80000000
R[0x08c040] ... |= 0x00040000
R[0x08c1c0] ... |= 0x00020000
```

The available RTX 2060 Super ROM has different values at the corresponding
locations, including zeroes at `0x88050`, `0x8c040`, and `0x8c1c0`. This is a
useful hypothesis source, but it is not proof that copying those values would
be safe or sufficient: the boards have different board/SKU straps and
firmware assumptions, and a ROM diff does not reveal the live sampled state.

## What this does not prove

This result is specific to the tested CMP 40HX VBIOS, board and driver. It
does not prove that every CMP model has the same endpoint gate, nor that a
future NVIDIA driver or a real RTX reference card could not expose a useful
difference. It does show that a runtime patch must first demonstrate a
writable capability source before attempting link training.

## Conclusions

The following are established on the tested CMP 40HX:

1. The Alder Lake root port accepts Gen2; the endpoint is the limiting side.
2. The endpoint advertises Gen1 before the stock Booter and no higher than
   Gen2 after the runtime policy path.
3. `LCAP` and `LCAP2` are effectively read-only at the available host/GSP
   access level. `LC2` alone is not enough to unlock a speed that the endpoint
   does not advertise.
4. Correcting the Gen3 speed vector, changing write order, opening the tested
   XVE policy bits and extending delays do not alter this boundary.
5. A Link Disable cycle can make the physical link train on Alder Lake, but it
   can detach the live GSP/RM device. It is not a production unlock sequence.

The most likely remaining boundary is an earlier VBIOS/devinit/FWSEC state or
a hardware strap/fuse sampled before the runtime path. The evidence does not
prove that every possible VBIOS-level or silicon-level workaround is
impossible, but it does prove that the tested kernel/GSP/MMIO overrides do not
provide a safe Gen3 unlock.
