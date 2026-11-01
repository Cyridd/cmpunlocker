/* SPDX-License-Identifier: GPL-2.0-only */
#ifndef GEN2_TEST_SHIM_H
#define GEN2_TEST_SHIM_H
#include <stdbool.h>
#include <stdint.h>
#include <stddef.h>
#include <errno.h>
#include <linux/pci_regs.h>

typedef uint8_t u8;
typedef uint16_t u16;
typedef uint32_t u32;
#define __iomem
#define BIT(n) (1u << (n))
#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))
#define PCI_VENDOR_ID_NVIDIA 0x10de
#define PCI_D0 0
#define IORESOURCE_MEM 0x200
struct pci_driver { const char *name; };
struct pci_dev {
	struct pci_driver *driver;
	u16 vendor, device;
	int current_state, type;
	bool pcie;
	u8 config[64];
	struct pci_dev *parent;
};
struct pci_dev *pci_upstream_bridge(struct pci_dev *d);
bool pci_is_pcie(struct pci_dev *d);
int pci_pcie_type(struct pci_dev *d);
const char *pci_name(struct pci_dev *d);
int pcie_capability_read_word(struct pci_dev *d, int offset, u16 *v);
int pcie_capability_read_dword(struct pci_dev *d, int offset, u32 *v);
int pcie_capability_write_word(struct pci_dev *d, int offset, u16 v);
int pci_read_config_word(struct pci_dev *d, int offset, u16 *v);
int pci_write_config_word(struct pci_dev *d, int offset, u16 v);
unsigned long pci_resource_flags(struct pci_dev *d, int bar);
size_t pci_resource_len(struct pci_dev *d, int bar);
uintptr_t pci_resource_start(struct pci_dev *d, int bar);
int pci_enable_device_mem(struct pci_dev *d);
void pci_disable_device(struct pci_dev *d);
void *ioremap(uintptr_t base, size_t size);
void iounmap(void *p);
u32 readl(const void *p);
void writel(u32 v, void *p);
void msleep(unsigned int ms);
/* Retain format checking while keeping test output compact. */
void mock_log(struct pci_dev *dev, const char *fmt, ...) __attribute__((format(printf, 2, 3)));
#define pci_info mock_log
#define pci_err mock_log
#endif
