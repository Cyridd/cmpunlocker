// SPDX-License-Identifier: GPL-2.0-only
/* Opt-in pre-driver Gen2 experiment. Firmware/DMA cleanup must finish first. */
#include <linux/delay.h>
#include <linux/io.h>
#include <linux/pci.h>
#include <linux/string.h>

#include "gen2.h"

#include "gen2_core.h"

void cmp40_gen2_snapshot(struct pci_dev *pdev, const char *phase)
{
	struct pci_dev *upstream = pci_upstream_bridge(pdev);
	void __iomem *bar;
	u16 command;

	/* Live config, rather than Linux's enumeration-time supported_speeds. */
	cmp40_g2_link_note(pdev, pdev, phase);
	if (upstream)
		cmp40_g2_link_note(pdev, upstream, phase);
	if (pci_read_config_word(pdev, PCI_COMMAND, &command) ||
	    command == 0xffff || !(command & PCI_COMMAND_MEMORY) ||
	    !(pci_resource_flags(pdev, 0) & IORESOURCE_MEM) ||
	    pci_resource_len(pdev, 0) < GEN2_BAR_SIZE)
		return;
	bar = ioremap(pci_resource_start(pdev, 0), GEN2_BAR_SIZE);
	if (!bar)
		return;
	if ((readl(bar) & 0xffff0000u) == 0x16600000u)
		cmp40_g2_bar_note(pdev, bar, phase);
	iounmap(bar);
}

static int run_gen2(struct pci_dev *pdev, unsigned int action, bool windows_policy,
		    bool runtime, struct cmp40_gen2_result *result)
{
	struct cmp40_g2_result outcome;
	void __iomem *bar = NULL;
	u16 original_command = 0, command;
	bool enabled = false;
	int ret, cleanup;

	result->device_ok = false;
	result->link_status = 0;
	if ((!runtime && pdev->driver) ||
	    (runtime && (!pdev->driver || strcmp(pdev->driver->name, "nvidia"))) ||
	    pdev->current_state != PCI_D0 ||
	    pdev->vendor != PCI_VENDOR_ID_NVIDIA || pdev->device != 0x1f0b)
		return -EBUSY;
	if (!(pci_resource_flags(pdev, 0) & IORESOURCE_MEM) ||
	    pci_resource_len(pdev, 0) < CHECK_SIZE)
		return -ENODEV;
	ret = pci_read_config_word(pdev, PCI_COMMAND, &original_command);
	if (ret || original_command == 0xffff) return ret ? ret : -ENODEV;
	/* Runtime diagnostics use existing decoding and never change ownership or
	 * the native driver's PCI enable count. Pre-driver firmware is quiesced.
	 */
	if (runtime) {
		if (!(original_command & PCI_COMMAND_MEMORY)) return -ENODEV;
	} else {
		ret = pci_enable_device_mem(pdev);
		if (ret) return ret;
		enabled = true;
	}
	bar = ioremap(pci_resource_start(pdev, 0), CHECK_SIZE);
	if (!bar) { ret = -ENOMEM; goto out; }
	if ((readl(bar) & 0xffff0000u) != 0x16600000u ||
	    readl(bar + REG_SS0) != 0x88888888 || readl(bar + REG_SS1) != 8 ||
	    (runtime && readl(bar + 0x110100) != 0x10)) {
		ret = -EIO;
		goto out;
	}
	ret = cmp40_g2_run(pdev, bar, action, windows_policy, false, &outcome);
	result->device_ok = outcome.device_ok;
	result->link_status = outcome.link_status;
out:
	if (bar) iounmap(bar);
	if (enabled) pci_disable_device(pdev);
	/* pci_disable_device() can clear MASTER; restore original MEMORY/MASTER. */
	cleanup = pci_read_config_word(pdev, PCI_COMMAND, &command);
	if (!cleanup && command != 0xffff) {
		u16 mask = PCI_COMMAND_MEMORY | PCI_COMMAND_MASTER;
		u16 want = (command & ~mask) | (original_command & mask);

		if (want != command) {
			if (runtime) cleanup = -EIO;
			else cleanup = pci_write_config_word(pdev, PCI_COMMAND, want);
		}
		if (!cleanup) cleanup = pci_read_config_word(pdev, PCI_COMMAND, &command);
		if (!cleanup && (command == 0xffff || (command & mask) != (original_command & mask)))
			cleanup = -EIO;
	} else if (!cleanup) {
		cleanup = -ENODEV;
	}
	if (cleanup) { result->device_ok = false; ret = cleanup; }
	return ret;
}

int cmp40_gen2_prepare(struct pci_dev *pdev, unsigned int action, bool windows_policy,
		       struct cmp40_gen2_result *result)
{
	return run_gen2(pdev, action, windows_policy, false, result);
}

int cmp40_gen2_runtime(struct pci_dev *pdev, bool windows_policy,
		      struct cmp40_gen2_result *result)
{
	return run_gen2(pdev, CMP40_G2_RETRAIN, windows_policy, true, result);
}
