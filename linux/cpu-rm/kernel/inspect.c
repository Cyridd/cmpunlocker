// SPDX-License-Identifier: GPL-2.0-only
#include <linux/io.h>
#include <linux/delay.h>
#include <linux/pci.h>
#include <linux/timekeeping.h>

#include "inspect.h"
#include "boot.h"

static void mmu_fault_snapshot(struct pci_dev *pdev, void __iomem *bar)
{
	u32 base = 0xb80000, status, info, addr_lo, addr_hi, inst_lo, inst_hi, i;

	/* TU102 dev_vm.h offsets plus gpuGetVirtRegPhysOffset_TU102's
	 * physical-function base. Read only: no W1C acknowledgements or GET
	 * updates. Preserve potentially stale raw fields even if VALID is zero.
	 */
	if (pci_resource_len(pdev, 0) < base + 0x3098 || readl(bar) != 0x166000a1)
		return;
	status = readl(bar + base + 0x3094);
	info = readl(bar + base + 0x3090);
	addr_lo = readl(bar + base + 0x3080);
	addr_hi = readl(bar + base + 0x3084);
	inst_lo = readl(bar + base + 0x3088);
	inst_hi = readl(bar + base + 0x308c);
	pci_info(pdev, "cmp40_preboot: SEC2_MMUFAULT status=%08x info=%08x addr=%08x:%08x inst=%08x:%08x stable=%u\n",
		status, info, addr_hi, addr_lo, inst_hi, inst_lo,
		status == readl(bar + base + 0x3094) && info == readl(bar + base + 0x3090));
	for (i = 0; i < 2; i++) {
		u32 offset = base + 0x3000 + i * 32;

		pci_info(pdev, "cmp40_preboot: SEC2_MMUFAULT_BUFFER index=%u lo=%08x hi=%08x get=%08x put=%08x size=%08x\n",
			i, readl(bar + offset), readl(bar + offset + 4),
			readl(bar + offset + 8), readl(bar + offset + 12), readl(bar + offset + 16));
	}
	/* Adjacent security/region fields are deliberately raw: published
	 * TU102 headers omit their semantics. Readback only, no field changes.
	 */
	pci_info(pdev, "cmp40_preboot: SEC2_REGION_RAW 630=%08x 634=%08x 638=%08x 63c=%08x 640=%08x 644=%08x 648=%08x 64c=%08x\n",
		readl(bar + 0x840630), readl(bar + 0x840634), readl(bar + 0x840638), readl(bar + 0x84063c),
		readl(bar + 0x840640), readl(bar + 0x840644), readl(bar + 0x840648), readl(bar + 0x84064c));
	pci_info(pdev, "cmp40_preboot: MMU_REGION_RAW 81c=%08x 820=%08x 824=%08x 828=%08x 830=%08x 834=%08x 838=%08x 83c=%08x\n",
		readl(bar + 0x1fa81c), readl(bar + 0x1fa820), readl(bar + 0x1fa824), readl(bar + 0x1fa828),
		readl(bar + 0x1fa830), readl(bar + 0x1fa834), readl(bar + 0x1fa838), readl(bar + 0x1fa83c));
}

int cmp40_inspect(struct pci_dev *pdev)
{
	void __iomem *bar;
	u16 command;
	u32 boot0;
	int ret;

	/* Never enable decoding, wake a powered-down device or change ownership. */
	ret = pci_read_config_word(pdev, PCI_COMMAND, &command);
	if (ret)
		return ret;
	if (command == 0xffff || !(command & PCI_COMMAND_MEMORY) ||
	    !(pci_resource_flags(pdev, 0) & IORESOURCE_MEM) ||
	    pci_resource_len(pdev, 0) < 0x84138c)
		return -ENODEV;
	bar = ioremap(pci_resource_start(pdev, 0), pci_resource_len(pdev, 0));
	if (!bar)
		return -ENOMEM;
	boot0 = readl(bar);
	if (boot0 == 0xffffffff || (boot0 & 0xffff0000u) == 0xbadf0000u) {
		ret = -EIO;
		goto out;
	}
	pci_info(pdev, "cmp40_preboot: SNAPSHOT driver=%s command=%04x boot0=%08x\n",
		pdev->driver ? pdev->driver->name : "none", command, boot0);
	{
		u32 gfw_plm = readl(bar + 0x118128);
		u32 gfw_boot = (gfw_plm & 1) ? readl(bar + 0x118234) : ~0u;
		pci_info(pdev, "cmp40_preboot: SNAPSHOT GFW_PLM=%08x GFW_BOOT=%08x\n",
			gfw_plm, gfw_boot);
	}
	pci_info(pdev, "cmp40_preboot: SNAPSHOT PLM=%08x SS0=%08x READOUT=%08x SS1=%08x\n",
		readl(bar + 0x409650), readl(bar + 0x409664),
		readl(bar + 0x409668), readl(bar + 0x40966c));
	pci_info(pdev, "cmp40_preboot: SNAPSHOT WPR2=%08x:%08x GSP_CPUCTL=%08x SEC2_CPUCTL=%08x MB0=%08x MB1=%08x\n",
		readl(bar + 0x1fa828), readl(bar + 0x1fa824),
		readl(bar + 0x110100), readl(bar + 0x840100),
		readl(bar + 0x840040), readl(bar + 0x840044));
	pci_info(pdev, "cmp40_preboot: SNAPSHOT SEC2_RESET_PLM=%08x SEC2_ENGINE=%08x\n",
		readl(bar + 0x8403c4), readl(bar + 0x8403c0));
	pci_info(pdev, "cmp40_preboot: SNAPSHOT SEC2_SCTL=%08x DEBUGINFO=%08x BOOTVEC=%08x HWCFG=%08x EXCI=%08x IMEMC=%08x DMEMC=%08x FALCON2_388=%08x\n",
		readl(bar + 0x840240), readl(bar + 0x840094),
		readl(bar + 0x840104), readl(bar + 0x840108), readl(bar + 0x8400d0),
		readl(bar + 0x840180), readl(bar + 0x8401c0), readl(bar + 0x841388));
	pci_info(pdev, "cmp40_preboot: SNAPSHOT SEC2_FBIF_CTL=%08x TRANS0=%08x TRANS4=%08x DMACTL=%08x DMACMD=%08x IRQSTAT=%08x IRQMASK=%08x\n",
		readl(bar + 0x840624), readl(bar + 0x840600),
		readl(bar + 0x840610), readl(bar + 0x84010c),
		readl(bar + 0x840118), readl(bar + 0x840008), readl(bar + 0x840018));
	pci_info(pdev, "cmp40_preboot: SNAPSHOT GSP_FBIF_CTL=%08x TRANS4=%08x DMACTL=%08x\n",
		readl(bar + 0x110624), readl(bar + 0x110610), readl(bar + 0x11010c));
	mmu_fault_snapshot(pdev, bar);
out:
	iounmap(bar);
	return ret;
}

/* Explicit port diagnostic, separate from inspect/no-writes mode. During
 * the captured native HALT poll, RM only reads CPUCTL. Accessing memory
 * index ports changes their controls, so save/restore both even on failure.
 * No memory writes, CPU/reset changes or firmware execution are performed.
 */
int cmp40_sec2_ports(struct pci_dev *pdev)
{
	void __iomem *bar;
	u16 command;
	u32 imemc, dmemc, hwcfg, dst, i, words[4];
	int ret = pci_read_config_word(pdev, PCI_COMMAND, &command);

	if (ret || command == 0xffff || !(command & PCI_COMMAND_MEMORY) ||
	    !pdev->driver || strcmp(pdev->driver->name, "nvidia") ||
	    pci_resource_len(pdev, 0) < 0x840640)
		return ret ? ret : -ENODEV;
	bar = ioremap(pci_resource_start(pdev, 0), pci_resource_len(pdev, 0));
	if (!bar)
		return -ENOMEM;
	/* Require the exact primary wait state captured on native CPU-RM 610. */
	if (readl(bar + 0x840100) != 0 || readl(bar + 0x840040) != 0xfe ||
	    readl(bar + 0x840104) != 0xfd00) {
		ret = -EAGAIN;
		goto out_ports;
	}
	imemc = readl(bar + 0x840180);
	dmemc = readl(bar + 0x8401c0);
	hwcfg = readl(bar + 0x840108);
	if (imemc == ~0u || dmemc == ~0u || (hwcfg & 0x1ffu) < 3) {
		ret = -EIO;
		goto out_ports;
	}
	dst = ((hwcfg & 0x1ffu) - 3) << 8;
	pci_info(pdev, "cmp40_preboot: SEC2_PORTS_BEGIN IMEMC=%08x DMEMC=%08x loader_dst=%08x\n",
		imemc, dmemc, dst);
	mmu_fault_snapshot(pdev, bar);
	pci_info(pdev, "cmp40_preboot: SEC2_CONTEXT TRANS1=%08x INSTBLK=%08x CTL=%08x DMACTL=%08x DMACMD=%08x ACHK0=%08x:%08x ACHK1=%08x:%08x\n",
		readl(bar + 0x840604), readl(bar + 0x840620), readl(bar + 0x840624),
		readl(bar + 0x84010c), readl(bar + 0x840118),
		readl(bar + 0x840630), readl(bar + 0x840634),
		readl(bar + 0x840638), readl(bar + 0x84063c));
	for (i = 0; i < 3; i++) {
		writel(dst + i * 256, bar + 0x840180);
		pci_info(pdev, "cmp40_preboot: SEC2_IMEM_TAG %04x: %08x\n",
			dst + i * 256, readl(bar + 0x840188));
	}
	writel(1u << 25, bar + 0x8401c0); /* DMEMC AINCR at offset zero */
	for (i = 0; i < 128; i += 16) {
		unsigned int j;

		for (j = 0; j < 4; j++)
			words[j] = readl(bar + 0x8401c4);
		pci_info(pdev, "cmp40_preboot: SEC2_DMEM %04x: %08x %08x %08x %08x\n",
			i, words[0], words[1], words[2], words[3]);
	}
	for (i = 0; i < 768; i += 16) {
		unsigned int j;

		/* TU102's published IMEMC has no AINCR field. Index each word. */
		for (j = 0; j < 4; j++) {
			writel(dst + i + j * 4, bar + 0x840180);
			words[j] = readl(bar + 0x840184);
		}
		pci_info(pdev, "cmp40_preboot: SEC2_IMEM %04x: %08x %08x %08x %08x\n",
			dst + i, words[0], words[1], words[2], words[3]);
	}
	/* The native descriptor's NS destination is IMEM 0, and the signed
	 * ACR data has its 16-byte authentication value at DMEM 0x2230.
	 * These reads locate transfer progress without resetting the Falcon.
	 */
	for (i = 0; i < 256; i += 16) {
		unsigned int j;

		for (j = 0; j < 4; j++) {
			writel(i + j * 4, bar + 0x840180);
			words[j] = readl(bar + 0x840184);
		}
		pci_info(pdev, "cmp40_preboot: SEC2_ACR_NS %04x: %08x %08x %08x %08x\n",
			i, words[0], words[1], words[2], words[3]);
	}
	writel((1u << 25) | 0x2200u, bar + 0x8401c0);
	for (i = 0x2200; i < 0x2300; i += 16) {
		unsigned int j;

		for (j = 0; j < 4; j++)
			words[j] = readl(bar + 0x8401c4);
		pci_info(pdev, "cmp40_preboot: SEC2_ACR_DMEM %04x: %08x %08x %08x %08x\n",
			i, words[0], words[1], words[2], words[3]);
	}
	writel(imemc, bar + 0x840180);
	writel(dmemc, bar + 0x8401c0);
	ret = (readl(bar + 0x840180) == imemc && readl(bar + 0x8401c0) == dmemc &&
	       readl(bar + 0x840100) == 0 && readl(bar + 0x840040) == 0xfe) ? 0 : -EAGAIN;
	pci_info(pdev, "cmp40_preboot: SEC2_PORTS_END restored=%u CPUCTL=%08x MB0=%08x status=%d\n",
		readl(bar + 0x840180) == imemc && readl(bar + 0x8401c0) == dmemc,
		readl(bar + 0x840100), readl(bar + 0x840040), ret);
out_ports:
	iounmap(bar);
	return ret;
}

/* Minimal turing-write hypothesis: no firmware, PLM changes or DMA. */
int cmp40_direct_selectors(struct pci_dev *pdev)
{
	void __iomem *bar;
	u16 command;
	u32 plm, ss0, ss1, readout;
	ktime_t deadline = ktime_add_us(ktime_get(), 3000000);
	int ret = pci_read_config_word(pdev, PCI_COMMAND, &command);

	if (ret || command == 0xffff || !(command & PCI_COMMAND_MEMORY) ||
	    pdev->driver || pci_resource_len(pdev, 0) < 0x409670)
		return ret ? ret : -ENODEV;
	bar = ioremap(pci_resource_start(pdev, 0), pci_resource_len(pdev, 0));
	if (!bar)
		return -ENOMEM;
	while (!turing_unlock_fb_range_ready(readl(bar + 0x100ce0))) {
		if (ktime_compare(ktime_get(), deadline) >= 0) {
			ret = -ETIMEDOUT;
			goto out_direct;
		}
		msleep(20);
	}
	plm = readl(bar + 0x409650);
	ss0 = readl(bar + 0x409664);
	ss1 = readl(bar + 0x40966c);
	if (plm != 0xffffff8f || ss0 || ss1) {
		ret = -EINVAL;
		goto out_direct;
	}
	writel(0x88888888, bar + 0x409664);
	writel(8, bar + 0x40966c);
	ss0 = readl(bar + 0x409664);
	ss1 = readl(bar + 0x40966c);
	readout = readl(bar + 0x409668);
	pci_info(pdev, "cmp40_preboot: DIRECT_RESULT PLM=%08x SS0=%08x SS1=%08x READOUT=%08x\n",
		readl(bar + 0x409650), ss0, ss1, readout);
	if (readl(bar + 0x409650) == plm && ss0 == 0x88888888 && ss1 == 8 && !readout) {
		ret = 0;
	} else {
		writel(0, bar + 0x409664);
		writel(0, bar + 0x40966c);
		ret = -EACCES;
		if (readl(bar + 0x409650) != plm || readl(bar + 0x409664) ||
		    readl(bar + 0x40966c))
			ret = -EIO;
	}
out_direct:
	iounmap(bar);
	return ret;
}
