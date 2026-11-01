// SPDX-License-Identifier: GPL-2.0-only
/* Compile the production Gen2 implementation with PCI/MMIO fault injection. */
#include <assert.h>
#include <stdio.h>
#include <string.h>
#include "gen2_shim.h"
#include "../kernel/gen2.c"

static struct pci_dev gpu, port;
static struct pci_driver native_driver = { "nvidia" }, other_driver = { "vfio-pci" };
static u32 mmio[CHECK_SIZE / 4];
static u16 command;
static unsigned writes, retrains, maps, enables, slept, mode;
static u32 reject_offset;
static bool lose_device, fail_gpu_target, fail_gpu_restore, fail_map;
static size_t bar_len;

static u16 get16(struct pci_dev *d, int off) { u16 v; memcpy(&v, d->config + off, 2); return v; }
static void put16(struct pci_dev *d, int off, u16 v) { memcpy(d->config + off, &v, 2); }
static void put32(struct pci_dev *d, int off, u32 v) { memcpy(d->config + off, &v, 4); }
struct pci_dev *pci_upstream_bridge(struct pci_dev *d) { return d->parent; }
bool pci_is_pcie(struct pci_dev *d) { return d->pcie; }
int pci_pcie_type(struct pci_dev *d) { return d->type; }
const char *pci_name(struct pci_dev *d) { return d == &gpu ? "0012:34:05.0" : "0012:33:00.0"; }
void mock_log(struct pci_dev *dev, const char *fmt, ...) { (void)dev; (void)fmt; }
int pcie_capability_read_word(struct pci_dev *d, int off, u16 *v)
{
	*v = lose_device && d == &gpu ? 0xffff : get16(d, off);
	return 0;
}
int pcie_capability_read_dword(struct pci_dev *d, int off, u32 *v)
{
	memcpy(v, d->config + off, 4);
	if (lose_device && d == &gpu) *v = ~0u;
	return 0;
}
int pcie_capability_write_word(struct pci_dev *d, int off, u16 v)
{
	writes++;
	if (lose_device && d == &gpu) return -EIO;
	if (d == &gpu && off == PCI_EXP_LNKCTL2 && fail_gpu_restore &&
	    (v & PCI_EXP_LNKCTL2_TLS) == 1) return -EIO;
	put16(d, off, v);
	if (d == &gpu && off == PCI_EXP_LNKCTL2 && fail_gpu_target) {
		fail_gpu_target = false;
		return -EIO; /* Simulate a write taking effect before an error. */
	}
	if (off == PCI_EXP_LNKCTL) {
		assert(d == &port && !(v & PCI_EXP_LNKCTL_LD));
		if (v & PCI_EXP_LNKCTL_RL) {
			retrains++;
			assert((get16(&gpu, PCI_EXP_LNKCTL2) & 15) == 2);
			assert((get16(&port, PCI_EXP_LNKCTL2) & 15) == 2);
			if (mode == 0) {
				put16(&gpu, PCI_EXP_LNKSTA, 0x1102);
				put16(&port, PCI_EXP_LNKSTA, 0x1102);
			} else if (mode == 2) lose_device = true;
			else if (mode == 3) put16(&gpu, PCI_EXP_LNKSTA, 0x1082);
			else if (mode == 4) put16(&gpu, PCI_EXP_LNKSTA, 0x1902);
		}
	}
	return 0;
}
int pci_read_config_word(struct pci_dev *d, int off, u16 *v)
{
	assert(d == &gpu && off == PCI_COMMAND);
	*v = lose_device ? 0xffff : command;
	return 0;
}
int pci_write_config_word(struct pci_dev *d, int off, u16 v)
{
	assert(d == &gpu && off == PCI_COMMAND);
	/* Count PL0/link writes separately from restoring PCI COMMAND. */
	command = v; return 0;
}
unsigned long pci_resource_flags(struct pci_dev *d, int b) { (void)d; assert(!b); return IORESOURCE_MEM; }
size_t pci_resource_len(struct pci_dev *d, int b) { (void)d; assert(!b); return bar_len; }
uintptr_t pci_resource_start(struct pci_dev *d, int b) { (void)d; assert(!b); return (uintptr_t)mmio; }
int pci_enable_device_mem(struct pci_dev *d) { assert(d == &gpu); enables++; command |= PCI_COMMAND_MEMORY; return 0; }
void pci_disable_device(struct pci_dev *d) { assert(d == &gpu && enables); enables--; command &= ~PCI_COMMAND_MASTER; }
void *ioremap(uintptr_t base, size_t size)
{
	assert(size <= sizeof(mmio));
	if (fail_map) return NULL;
	maps++; return (void *)base;
}
void iounmap(void *p) { assert(p == mmio && maps); maps--; }
u32 readl(const void *p) { return lose_device ? ~0u : *(const u32 *)p; }
void writel(u32 v, void *p)
{
	size_t offset = (u8 *)p - (u8 *)mmio;
	writes++;
	assert(!retrains); /* Every policy write must precede the retrain. */
	if (offset != reject_offset) *(u32 *)p = v;
}
void msleep(unsigned int ms) { slept += ms; }

static void reset(void)
{
	memset(&gpu, 0, sizeof(gpu)); memset(&port, 0, sizeof(port)); memset(mmio, 0, sizeof(mmio));
	gpu.vendor = 0x10de; gpu.device = 0x1f0b; gpu.parent = &port;
	gpu.pcie = port.pcie = true; port.type = PCI_EXP_TYPE_ROOT_PORT;
	put16(&gpu, PCI_EXP_FLAGS, 2); put16(&port, PCI_EXP_FLAGS, 2);
	put32(&gpu, PCI_EXP_LNKCAP, 0x00453d01); put32(&gpu, PCI_EXP_LNKCAP2, 2);
	put32(&port, PCI_EXP_LNKCAP, 0x00453d03); put32(&port, PCI_EXP_LNKCAP2, 14);
	put16(&gpu, PCI_EXP_LNKSTA, 0x1101); put16(&port, PCI_EXP_LNKSTA, 0x1101);
	put16(&gpu, PCI_EXP_LNKCTL2, 0xa041); put16(&port, PCI_EXP_LNKCTL2, 0x3021);
	put16(&port, PCI_EXP_LNKCTL, 0x1043);
	mmio[0] = 0x166000a1; mmio[REG_SS0 / 4] = 0x88888888; mmio[REG_SS1 / 4] = 8;
	mmio[0x110100 / 4] = 0x10;
	mmio[REG_CFG / 4] = 0x80145800; mmio[REG_PL / 4] = 0x01210080;
	mmio[REG_MISC / 4] = 0xa5125555; mmio[REG_CYA / 4] = 0x87654327;
	command = 0x1407; writes = retrains = maps = enables = slept = mode = 0;
	reject_offset = ~0u; lose_device = fail_gpu_target = fail_gpu_restore = fail_map = false;
	bar_len = sizeof(mmio);
}

static void balanced(void) { assert(!maps && !enables); }
int main(void)
{
	struct cmp40_gen2_result r;
	unsigned cases = 0;
#define CASE_DONE() do { balanced(); cases++; } while (0)
	reset();
	assert(!cmp40_gen2_prepare(&gpu, true, false, &r));
	assert(r.device_ok && r.link_status == 0x1102 && retrains == 1);
	assert(mmio[REG_CFG / 4] == ((0x80145800 & ~0xc0000u) | 0x80000));
	assert(mmio[REG_PL / 4] == ((0x01210080 & ~0x60000u) | 0x40000));
	assert(mmio[REG_MISC / 4] == ((0xa5125555 | BIT(11) | BIT(13)) & ~(BIT(12) | BIT(14))));
	assert(mmio[REG_CYA / 4] == (0x87654327 & ~BIT(2)));
	assert(get16(&gpu, PCI_EXP_LNKCTL2) == 0xa042 && get16(&port, PCI_EXP_LNKCTL2) == 0x3022);
	assert(command == 0x1407); CASE_DONE();
	reset(); assert(!cmp40_gen2_prepare(&gpu, false, true, &r));
	assert(r.device_ok && !retrains && mmio[REG_MISC / 4] == 0xe0b42d00 && mmio[REG_CYA / 4] == 0x068731b3);
	assert(get16(&gpu, PCI_EXP_LNKCTL2) == 0xa041); CASE_DONE();
	reset(); mode = 1; assert(cmp40_gen2_prepare(&gpu, true, false, &r) == -ETIMEDOUT);
	assert(r.device_ok && r.link_status == 0x1101 && slept <= 4050);
	assert(get16(&gpu, PCI_EXP_LNKCTL2) == 0xa041 && get16(&port, PCI_EXP_LNKCTL2) == 0x3021); CASE_DONE();
	reset(); mode = 2; assert(cmp40_gen2_prepare(&gpu, true, false, &r) != 0);
	assert(!r.device_ok && get16(&port, PCI_EXP_LNKCTL2) == 0x3021); CASE_DONE();
	reset(); mode = 3; assert(cmp40_gen2_prepare(&gpu, true, false, &r) != 0);
	assert(!r.device_ok && r.link_status == 0x1082); CASE_DONE();
	reset(); mode = 4; assert(cmp40_gen2_prepare(&gpu, true, false, &r) != 0);
	assert(!r.device_ok); CASE_DONE();
	reset(); reject_offset = REG_PL; assert(cmp40_gen2_prepare(&gpu, true, false, &r) == -EIO);
	assert(r.device_ok && !retrains && mmio[REG_MISC / 4] == 0xa5125555); CASE_DONE();
	reset(); mmio[REG_CYA / 4] = 0xbadf1234; assert(cmp40_gen2_prepare(&gpu, true, false, &r) == -EIO);
	assert(!writes && !retrains && r.device_ok); CASE_DONE();
	reset(); fail_gpu_target = true; assert(cmp40_gen2_prepare(&gpu, true, false, &r) == -EIO);
	assert(!retrains && get16(&gpu, PCI_EXP_LNKCTL2) == 0xa041); CASE_DONE();
	reset(); mode = 1; fail_gpu_restore = true; assert(cmp40_gen2_prepare(&gpu, true, false, &r) == -EIO);
	assert(get16(&port, PCI_EXP_LNKCTL2) == 0x3021); CASE_DONE();
	reset(); gpu.driver = &native_driver; assert(cmp40_gen2_prepare(&gpu, true, false, &r) == -EBUSY);
	assert(!writes); CASE_DONE();
	reset(); gpu.current_state = 3; assert(cmp40_gen2_prepare(&gpu, true, false, &r) == -EBUSY);
	assert(!writes); CASE_DONE();
	reset(); gpu.parent = NULL; assert(cmp40_gen2_prepare(&gpu, true, false, &r) == -EOPNOTSUPP);
	assert(!writes); CASE_DONE();
	reset(); put32(&port, PCI_EXP_LNKCAP2, 2); assert(cmp40_gen2_prepare(&gpu, true, false, &r) == -EOPNOTSUPP);
	assert(!writes); CASE_DONE();
	reset(); put16(&port, PCI_EXP_LNKCTL, PCI_EXP_LNKCTL_LD); assert(cmp40_gen2_prepare(&gpu, true, false, &r) == -EOPNOTSUPP);
	assert(!writes); CASE_DONE();
	reset(); mmio[REG_SS0 / 4] = 0; assert(cmp40_gen2_prepare(&gpu, true, false, &r) == -EIO);
	assert(!writes && !r.device_ok); CASE_DONE();
	reset(); fail_map = true; assert(cmp40_gen2_prepare(&gpu, true, false, &r) == -ENOMEM);
	assert(!r.device_ok && command == 0x1407); CASE_DONE();
	reset(); command = 0x1401; assert(!cmp40_gen2_prepare(&gpu, false, false, &r));
	assert(r.device_ok && command == 0x1401); CASE_DONE();
	reset(); cmp40_gen2_snapshot(&gpu, "inspect"); assert(!writes); CASE_DONE();
	reset(); gpu.driver = &native_driver;
	assert(!cmp40_gen2_runtime(&gpu, false, &r));
	assert(r.device_ok && retrains == 1 && command == 0x1407); CASE_DONE();
	reset(); gpu.driver = &native_driver; mmio[0x110100 / 4] = 0;
	assert(cmp40_gen2_runtime(&gpu, false, &r) == -EIO && !writes); CASE_DONE();
	reset(); gpu.driver = &other_driver;
	assert(cmp40_gen2_runtime(&gpu, false, &r) == -EBUSY && !writes); CASE_DONE();
	reset(); assert(cmp40_gen2_runtime(&gpu, false, &r) == -EBUSY && !writes); CASE_DONE();
	reset(); gpu.driver = &native_driver; command &= ~PCI_COMMAND_MEMORY;
	assert(cmp40_gen2_runtime(&gpu, false, &r) == -ENODEV && !writes); CASE_DONE();
	printf("Gen2: %u production-code fault-injection cases passed\n", cases);
	return 0;
}
