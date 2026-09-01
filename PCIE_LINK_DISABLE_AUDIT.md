# PCIe Link Disable audit

This project originally attempted to integrate the following sequence into
the CMP 40HX kernel patch:

1. set both endpoint and upstream `LnkCtl2.Target Link Speed` fields to Gen2;
2. set `LnkCtl.Link Disable` on the upstream port;
3. wait for the link to go down;
4. clear `Link Disable` and request `Retrain Link`.

The sequence is able to change the physical link state on some platforms, but
it is not a safe operation after NVIDIA RM/GSP has initialized the GPU.

## Observed results

### CMP 40HX on the project maintainer's system

The compute and GSP PCIe policy parts completed successfully. The integrated
Link Disable variant then left the card with POST-like maximum fan speed and
`nvidia-smi` unable to enumerate the GPU. This happened even though the
ordinary PCIe path had already logged a successful Gen2 retrain (`status=1102`)
and the compute unlock was working.

### CMP 40HX on Intel Alder Lake

The same ASUS CMP 40HX subsystem (`1043:8804`) with VBIOS
`90.06.67.00.06` reached a real Gen2 x16 link with the manual sequence:

```text
LnkCap:  Speed 5GT/s, Width x16
LnkSta:  Speed 5GT/s, Width x16
LnkCap2: Supported Link Speeds: 2.5-5GT/s
LnkCtl2: Target Link Speed: 5.0GT/s
```

However, after the runtime operation the NVIDIA device detached from GSP/RM;
`nvidia-smi` reported `Unknown Error`, and a runtime PCI unbind could hang on
active driver references. The result therefore proves link training, not a
usable persistent driver configuration.

### CMP 50HX audit

The independent [`CMP50HX-PCIE-LINK-DISABLE-AUDIT.md`](https://github.com/xrip/cmp50hx-unlock/blob/f323ca22710699781e34d858d64a86f58f019c3e/docs/CMP50HX-PCIE-LINK-DISABLE-AUDIT.md) investigation found the
same boundary failure. A Link Disable cycle after `rm_init_adapter()` reached
Gen2 but produced BAR0/PCI reads of `ffffffff`, Xid 79/Xid 154, and loss of
`nvidia-smi`. A cycle before RM initialization did not produce a usable RM
device either. The safe delayed service used on that project performs only a
normal upstream Retrain Link after the card's policy registers are unlocked;
it does not use Link Disable.

## Decision

The regular `0002-cmp40hx-pcie2-unlock.patch` and the diagnostic replacement
now use only the proven sequence:

```text
GSP/RM policy setup -> set both Gen2 targets -> upstream Retrain Link
```

The Link Disable cycle is deliberately excluded from the kernel module. Longer
delays cannot repair the ownership/state loss caused by disconnecting a live
GSP-managed endpoint. `status=1102` is only a PCIe link-status value (Gen2,
x16); it does not certify that RM/GSP remained healthy afterward.

## Manual reproducer

`tools/cmp40hx-alder-lake-link-disable-test.sh` reproduces the Alder Lake
experiment for research. It is not an unlock and is not installed by
`install.sh`. It requires an explicit confirmation, refuses ambiguous GPU
selection, clears `Link Disable` on errors, and checks `nvidia-smi` afterward.
Run it only from a system with console or SSH recovery access and no GPU
workload. A failed run may require a full cold power-off.

Example:

```bash
sudo ./tools/cmp40hx-alder-lake-link-disable-test.sh \
  --i-understand-gsp-may-be-lost
```

Use `--gpu 0000:01:00.0` when more than one NVIDIA GPU is present.

There is intentionally no enabled systemd unit for this sequence. Running it
automatically after driver initialization would turn a known GSP-detaching
experiment into a boot-time failure mode.
