// SPDX-License-Identifier: GPL-2.0-only
/* Opt-in pre-driver Gen2 experiment. Firmware/DMA cleanup must finish first. */
#include <linux/delay.h>
#include <linux/io.h>
#include <linux/pci.h>
#include <linux/string.h>

#include "gen2.h"

#define GEN2_BAR_SIZE 0x90000
#define REG_OVR      0x8872c
#define REG_CFG      0x8c040
#define REG_PL       0x8c1c0
#define REG_MISC     0x8841c
#define REG_CYA      0x8c2c0
#define REG_SS0      0x409664
#define REG_SS1      0x40966c
#define CHECK_SIZE   (REG_SS1 + 4)

struct link_state {
	u32 cap, cap2;
	u16 ctl, ctl2, status;
};

static int link_read(struct pci_dev *dev, struct link_state *s)
{
	int ret;

	ret = pcie_capability_read_dword(dev, PCI_EXP_LNKCAP, &s->cap);
	if (ret) return ret;
	ret = pcie_capability_read_dword(dev, PCI_EXP_LNKCAP2, &s->cap2);
	if (ret) return ret;
	ret = pcie_capability_read_word(dev, PCI_EXP_LNKCTL, &s->ctl);
	if (ret) return ret;
	ret = pcie_capability_read_word(dev, PCI_EXP_LNKCTL2, &s->ctl2);
	if (ret) return ret;
	ret = pcie_capability_read_word(dev, PCI_EXP_LNKSTA, &s->status);
	if (ret) return ret;
	if (s->cap == ~0u || s->cap2 == ~0u || s->ctl == 0xffff ||
	    s->ctl2 == 0xffff || s->status == 0xffff)
		return -ENODEV;
	return 0;
}

static bool link_up(u16 status)
{
	return status != 0xffff && (status & PCI_EXP_LNKSTA_CLS) &&
		(status & PCI_EXP_LNKSTA_NLW) && !(status & PCI_EXP_LNKSTA_LT);
}

static void link_note(struct pci_dev *gpu, struct pci_dev *dev,
		      const char *phase)
{
	struct link_state s;
	int ret = link_read(dev, &s);

	if (ret)
		pci_info(gpu, "cmp40_preboot: GEN2 phase=%s pci=%s read_error=%d\n",
			 phase, pci_name(dev), ret);
	else
		pci_info(gpu, "cmp40_preboot: GEN2 phase=%s pci=%s CAP=%08x CAP2=%08x CTL=%04x CTL2=%04x STA=%04x\n",
			 phase, pci_name(dev), s.cap, s.cap2, s.ctl, s.ctl2, s.status);
}

static void bar_note(struct pci_dev *dev, void __iomem *bar, const char *phase)
{
	pci_info(dev, "cmp40_preboot: GEN2 phase=%s OVR=%08x CFG=%08x PL=%08x MISC=%08x CYA=%08x CAP=%08x CAP2=%08x CTL2=%08x\n",
		 phase, readl(bar + REG_OVR), readl(bar + REG_CFG),
		 readl(bar + REG_PL), readl(bar + REG_MISC), readl(bar + REG_CYA),
		 readl(bar + 0x88084), readl(bar + 0x880a4), readl(bar + 0x880a8));
}

void cmp40_gen2_snapshot(struct pci_dev *pdev, const char *phase)
{
	struct pci_dev *upstream = pci_upstream_bridge(pdev);
	void __iomem *bar;
	u16 command;

	/* Live config, rather than Linux's enumeration-time supported_speeds. */
	link_note(pdev, pdev, phase);
	if (upstream)
		link_note(pdev, upstream, phase);
	if (pci_read_config_word(pdev, PCI_COMMAND, &command) ||
	    command == 0xffff || !(command & PCI_COMMAND_MEMORY) ||
	    !(pci_resource_flags(pdev, 0) & IORESOURCE_MEM) ||
	    pci_resource_len(pdev, 0) < GEN2_BAR_SIZE)
		return;
	bar = ioremap(pci_resource_start(pdev, 0), GEN2_BAR_SIZE);
	if (!bar)
		return;
	if ((readl(bar) & 0xffff0000u) == 0x16600000u)
		bar_note(pdev, bar, phase);
	iounmap(bar);
}

static int policy_write(struct pci_dev *pdev, void __iomem *bar,
			u32 offset, u32 value, u32 mask)
{
	u32 before = readl(bar + offset), after;

	if (before == ~0u || (before & 0xffff0000u) == 0xbadf0000u)
		return -EIO;
	value = (before & ~mask) | (value & mask);
	writel(value, bar + offset);
	after = readl(bar + offset);
	pci_info(pdev, "cmp40_preboot: GEN2 write=%05x before=%08x want=%08x readback=%08x mask=%08x\n",
		 offset, before, value, after, mask);
	return after == ~0u || (after & 0xffff0000u) == 0xbadf0000u ||
		((after ^ value) & mask) ? -EIO : 0;
}

static int apply_policy(struct pci_dev *pdev, void __iomem *bar, bool windows)
{
	const u32 offsets[] = { REG_OVR, REG_CFG, REG_PL, REG_MISC, REG_CYA };
	u32 misc_mask = BIT(11) | BIT(12) | BIT(13) | BIT(14);
	u32 i, value;
	int ret;

	/* Validate every register before the first write. */
	for (i = 0; i < ARRAY_SIZE(offsets); i++) {
		value = readl(bar + offsets[i]);
		if (value == ~0u || (value & 0xffff0000u) == 0xbadf0000u)
			return -EIO;
	}
	ret = policy_write(pdev, bar, REG_OVR, 6, ~0u);
	if (ret) return ret;
	ret = policy_write(pdev, bar, REG_CFG, 0x80000, 0xc0000);
	if (ret) return ret;
	ret = policy_write(pdev, bar, REG_PL, 0x40000, 0x60000);
	if (ret) return ret;
	ret = policy_write(pdev, bar, REG_MISC, windows ? 0xe0b42d00 : BIT(11) | BIT(13),
			   windows ? ~0u : misc_mask);
	if (ret) return ret;
	return policy_write(pdev, bar, REG_CYA, windows ? 0x068731b3 : 0,
			    windows ? ~0u : BIT(2));
}

static int set_target(struct pci_dev *dev, u16 target)
{
	u16 before, after;
	int ret;

	/* Change only TLS, including when rolling back a failed attempt. */
	ret = pcie_capability_read_word(dev, PCI_EXP_LNKCTL2, &before);
	if (ret || before == 0xffff) return ret ? ret : -ENODEV;
	ret = pcie_capability_write_word(dev, PCI_EXP_LNKCTL2,
		(before & ~PCI_EXP_LNKCTL2_TLS) | (target & PCI_EXP_LNKCTL2_TLS));
	if (ret) return ret;
	ret = pcie_capability_read_word(dev, PCI_EXP_LNKCTL2, &after);
	if (ret) return ret;
	return after == 0xffff || (after & PCI_EXP_LNKCTL2_TLS) !=
		(target & PCI_EXP_LNKCTL2_TLS) ? -EIO : 0;
}

static int train(struct pci_dev *gpu, struct pci_dev *upstream,
		 const struct link_state *initial_gpu,
		 const struct link_state *initial_upstream)
{
	u16 ctl, status;
	bool targets_changed = false;
	int ret, attempt, cleanup;

	/* Avoid changing TLS during an existing link-training operation. */
	for (attempt = 0; attempt < 20; attempt++) {
		ret = pcie_capability_read_word(upstream, PCI_EXP_LNKSTA, &status);
		if (ret || status == 0xffff) {
			ret = ret ? ret : -ENODEV;
			goto out;
		}
		if (!(status & PCI_EXP_LNKSTA_LT)) break;
		msleep(100);
	}
	if (attempt == 20) { ret = -ETIMEDOUT; goto out; }
	ret = set_target(gpu, PCI_EXP_LNKCTL2_TLS_5_0GT);
	/* A failed write/readback may still have changed the target. */
	targets_changed = true;
	if (ret) goto out;
	ret = set_target(upstream, PCI_EXP_LNKCTL2_TLS_5_0GT);
	if (ret) goto out;
	ret = pcie_capability_read_word(upstream, PCI_EXP_LNKCTL, &ctl);
	if (ret || ctl == 0xffff || (ctl & PCI_EXP_LNKCTL_LD)) {
		ret = ret ? ret : -EIO;
		goto out;
	}
	ret = pcie_capability_write_word(upstream, PCI_EXP_LNKCTL,
					ctl | PCI_EXP_LNKCTL_RL);
	if (ret) goto out;
	for (attempt = 0; attempt < 20; attempt++) {
		msleep(100);
		ret = pcie_capability_read_word(gpu, PCI_EXP_LNKSTA, &status);
		if (!ret && link_up(status) &&
		    (status & PCI_EXP_LNKSTA_CLS) == PCI_EXP_LNKSTA_CLS_5_0GB &&
		    (status & PCI_EXP_LNKSTA_NLW) == (initial_gpu->status & PCI_EXP_LNKSTA_NLW))
			return 0;
	}
	ret = -ETIMEDOUT;
out:
	if (targets_changed) {
		cleanup = set_target(gpu, initial_gpu->ctl2);
		/* Always try both, even when the endpoint became inaccessible. */
		attempt = set_target(upstream, initial_upstream->ctl2);
		if (!cleanup) cleanup = attempt;
		if (cleanup) {
			pci_err(gpu, "cmp40_preboot: GEN2 target restore failed: %d\n", cleanup);
			ret = cleanup;
		}
	}
	return ret;
}

static int run_gen2(struct pci_dev *pdev, bool retrain, bool windows_policy,
		    bool runtime, struct cmp40_gen2_result *result)
{
	struct pci_dev *upstream = pci_upstream_bridge(pdev);
	struct link_state gpu_link, upstream_link, final, final_upstream;
	void __iomem *bar = NULL;
	u16 original_command = 0, command, flags;
	bool enabled = false;
	int ret, cleanup;

	result->device_ok = false;
	result->link_status = 0;
	if ((!runtime && pdev->driver) ||
	    (runtime && (!pdev->driver || strcmp(pdev->driver->name, "nvidia"))) ||
	    pdev->current_state != PCI_D0 ||
	    pdev->vendor != PCI_VENDOR_ID_NVIDIA || pdev->device != 0x1f0b)
		return -EBUSY;
	if (!upstream || !pci_is_pcie(pdev) || !pci_is_pcie(upstream) ||
	    (pci_pcie_type(upstream) != PCI_EXP_TYPE_ROOT_PORT &&
	     pci_pcie_type(upstream) != PCI_EXP_TYPE_DOWNSTREAM))
		return -EOPNOTSUPP;
	ret = pcie_capability_read_word(pdev, PCI_EXP_FLAGS, &flags);
	if (ret || (flags & PCI_EXP_FLAGS_VERS) < 2) return ret ? ret : -EOPNOTSUPP;
	ret = pcie_capability_read_word(upstream, PCI_EXP_FLAGS, &flags);
	if (ret || (flags & PCI_EXP_FLAGS_VERS) < 2) return ret ? ret : -EOPNOTSUPP;
	ret = link_read(pdev, &gpu_link);
	if (ret) return ret;
	ret = link_read(upstream, &upstream_link);
	if (ret) return ret;
	if (!(upstream_link.cap2 & PCI_EXP_LNKCAP2_SLS_5_0GB) ||
	    !link_up(gpu_link.status) || !link_up(upstream_link.status) ||
	    (upstream_link.ctl & PCI_EXP_LNKCTL_LD))
		return -EOPNOTSUPP;
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
	bar_note(pdev, bar, runtime ? "before_runtime_policy" : "after_compute_cleanup");
	link_note(pdev, pdev, runtime ? "before_runtime_policy" : "after_compute_cleanup");
	link_note(pdev, upstream, runtime ? "before_runtime_policy" : "after_compute_cleanup");
	pci_info(pdev, "cmp40_preboot: GEN2 policy=%s retrain=%u upstream=%s\n",
		 windows_policy ? "windows" : "rmw", retrain, pci_name(upstream));
	ret = apply_policy(pdev, bar, windows_policy);
	msleep(50);
	bar_note(pdev, bar, "after_policy");
	link_note(pdev, pdev, "after_policy");
	link_note(pdev, upstream, "after_policy");
	if (!ret && retrain)
		ret = train(pdev, upstream, &gpu_link, &upstream_link);
	bar_note(pdev, bar, "final");
	link_note(pdev, pdev, "final");
	link_note(pdev, upstream, "final");
	cleanup = link_read(pdev, &final);
	if (!cleanup) result->link_status = final.status;
	if (!cleanup) cleanup = link_read(upstream, &final_upstream);
	result->device_ok = !cleanup && link_up(final.status) && link_up(final_upstream.status) &&
		(final.status & (PCI_EXP_LNKSTA_CLS | PCI_EXP_LNKSTA_NLW)) ==
		(final_upstream.status & (PCI_EXP_LNKSTA_CLS | PCI_EXP_LNKSTA_NLW)) &&
		(final.status & PCI_EXP_LNKSTA_NLW) == (gpu_link.status & PCI_EXP_LNKSTA_NLW) &&
		(readl(bar) & 0xffff0000u) == 0x16600000u &&
		readl(bar + REG_SS0) == 0x88888888 && readl(bar + REG_SS1) == 8;
	if (!result->device_ok) ret = -ENODEV;
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

int cmp40_gen2_prepare(struct pci_dev *pdev, bool retrain, bool windows_policy,
		       struct cmp40_gen2_result *result)
{
	return run_gen2(pdev, retrain, windows_policy, false, result);
}

int cmp40_gen2_runtime(struct pci_dev *pdev, bool windows_policy,
		      struct cmp40_gen2_result *result)
{
	return run_gen2(pdev, true, windows_policy, true, result);
}
