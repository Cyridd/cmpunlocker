/* SPDX-License-Identifier: GPL-2.0-only */
#ifndef CMP40_GEN2_H
#define CMP40_GEN2_H

#include <linux/types.h>

struct pci_dev;

/* A link-training failure is independent of the completed compute unlock. */
struct cmp40_gen2_result {
	bool device_ok;
	u16 link_status;
};

void cmp40_gen2_snapshot(struct pci_dev *pdev, const char *phase);
int cmp40_gen2_prepare(struct pci_dev *pdev, bool retrain, bool windows_policy,
		       struct cmp40_gen2_result *result);
/* Explicit diagnostic only: helper must verify idle proprietary CPU-RM first. */
int cmp40_gen2_runtime(struct pci_dev *pdev, bool windows_policy,
		      struct cmp40_gen2_result *result);

#endif
