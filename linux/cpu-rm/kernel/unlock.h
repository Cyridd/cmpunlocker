/* SPDX-License-Identifier: GPL-2.0-only */
#ifndef TURING_UNLOCK_UNLOCK_H
#define TURING_UNLOCK_UNLOCK_H

#include <linux/types.h>

struct pci_dev;

int turing_unlock_card(struct pci_dev *pdev, bool *retained, bool cpu_rm_handoff,
                       bool stock_handoff, bool locked_control, bool signed_unload,
                       bool loader_control, bool mmu_invalidate, bool unload_before_down,
                       bool loader_fb_control, bool loader_virtual_control,
                       bool loader_native_fbif, bool loader_native_addresses,
                       bool loader_native_physical, unsigned int loader_boundary,
                       bool unload_full_wpr, bool small_wpr_heap);

#endif
