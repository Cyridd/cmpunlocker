/* SPDX-License-Identifier: GPL-2.0-only */
#ifndef CMP40_INSPECT_H
#define CMP40_INSPECT_H
struct pci_dev;
int cmp40_sec2_timeline(struct pci_dev *pdev, bool *armed);
void cmp40_sec2_timeline_release(void);
int cmp40_inspect(struct pci_dev *pdev);
int cmp40_direct_selectors(struct pci_dev *pdev);
int cmp40_sec2_ports(struct pci_dev *pdev);
int cmp40_sec2_vm(struct pci_dev *pdev);
#endif
