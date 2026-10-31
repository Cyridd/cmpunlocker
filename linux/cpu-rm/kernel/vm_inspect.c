// SPDX-License-Identifier: GPL-2.0-only
/* Explicit PRAMIN-window diagnostic during the primary native SEC2 wait.
 * Only BAR0_WINDOW is written and restored; GPU memory is never written.
 * Tables are followed only in local FB and only inside this board's 8 GiB.
 * A fault-buffer leaf may use PRAMIN's documented coherent/noncoherent
 * sysmem target, restricted to the one pending record's validated PTE.
 */
#include <linux/io.h>
#include <linux/pci.h>
#include <linux/slab.h>
#include <linux/string.h>

#include "inspect.h"

#define VM_WINDOW 0x1700u
#define VM_PRAMIN 0x700000u
#define VM_FB_SIZE (8ull << 30)

struct vm_view {
	struct pci_dev *pdev;
	void __iomem *bar;
	u32 saved_window;
};

static int vm_read_target(struct vm_view *v, u64 addr, u32 *out, u32 size, u32 target)
{
	u32 at, mapped = ~0u;
	u64 limit = target ? (1ull << 40) : VM_FB_SIZE;

	if ((target != 0 && target != 2 && target != 3) || !size ||
	    (size | addr) & 3 || addr >= limit || size > limit - addr)
		return -EINVAL;
	for (at = 0; at < size; at += 4) {
		u64 off = addr + at;
		u32 window = (v->saved_window & ~0x03ffffffu) |
			     (u32)(off >> 16) | (target << 24);

		if (mapped != window) {
			writel(window, v->bar + VM_WINDOW);
			if (readl(v->bar + VM_WINDOW) != window)
				return -EACCES;
			mapped = window;
		}
		out[at / 4] = readl(v->bar + VM_PRAMIN + (off & 0xffff));
	}
	return 0;
}

static int vm_dump_target(struct vm_view *v, const char *name, u64 addr,
			  u32 *out, u32 size, u32 target)
{
	u32 at;
	int ret = vm_read_target(v, addr, out, size, target);

	pci_info(v->pdev, "cmp40_preboot: SEC2_VM_BLOB %s addr=%#llx size=%#x status=%d\n",
		name, (unsigned long long)addr, size, ret);
	if (ret)
		return ret;
	/* Compact raw-byte rows keep the full image below the 128 KiB kernel
	 * ring. %*phN supports at most 64 bytes per conversion.
	 */
	for (at = 0; at < size; at += 128) {
		u32 bytes = min_t(u32, 128, size - at);
		u8 *p = (u8 *)out + at;

		if (bytes > 64)
			pci_info(v->pdev, "cmp40_preboot: SEC2_VM_HEX %s %04x: %*phN%*phN\n",
				name, at, 64, p, bytes - 64, p + 64);
		else
			pci_info(v->pdev, "cmp40_preboot: SEC2_VM_HEX %s %04x: %*phN\n",
				name, at, bytes, p);
	}
	return 0;
}

static int vm_dump(struct vm_view *v, const char *name, u64 addr, u32 *out, u32 size)
{
	return vm_dump_target(v, name, addr, out, size, 0);
}

static u64 vm_entry(const u32 *p)
{
	return (u64)p[0] | ((u64)p[1] << 32);
}

/* GP100/TU10x format v2: PDE/PTE local-FB address is bits 32:8, <<12. */
static u64 vm_entry_addr(u64 e)
{
	return ((e >> 8) & 0x1ffffffull) << 12;
}

static int vm_range(struct vm_view *v, const char *name, u32 va, u32 size,
		    const u64 *ptes, bool small, u32 *buf)
{
	u32 at, page_size = small ? 0x1000 : 0x10000;

	for (at = 0; at < size;) {
		u32 page_va = va + at, index = page_va / page_size;
		u32 bytes = min_t(u32, 1024, size - at);
		u64 entry, addr;
		char label[32];
		int ret;

		if (index >= 16)
			return -ERANGE;
		entry = ptes[index];
		if (!(entry & 1) || ((entry >> 1) & 3) != 0)
			return -EOPNOTSUPP;
		bytes = min(bytes, page_size - (page_va & (page_size - 1)));
		addr = vm_entry_addr(entry) + (page_va & (page_size - 1));
		snprintf(label, sizeof(label), "%s_%04x", name, at);
		pci_info(v->pdev, "cmp40_preboot: SEC2_VM_MAP %s offset=%#x va=%#x pte=%08x:%08x phys=%#llx size=%#x\n",
			name, at, page_va, (u32)(entry >> 32), (u32)entry,
			(unsigned long long)addr, bytes);
		ret = vm_dump(v, label, addr, buf, bytes);
		if (ret)
			return ret;
		at += bytes;
	}
	return 0;
}

static int vm_walk(struct vm_view *v, u32 *buf)
{
	static const char *const levels[] = { "PD3", "PD2", "PD1" };
	u32 inst = readl(v->bar + 0x840620), lo, hi, i;
	u64 addr, entry, ptes[16];
	bool small;
	int ret;

	if (!inst || inst >> 28 || ((u64)inst << 12) >= VM_FB_SIZE)
		return -ERANGE;
	ret = vm_dump(v, "INST", (u64)inst << 12, buf, 1024);
	if (ret)
		return ret;
	/* Legacy non-subcontext RAMIN PDB record at 0x200. Preserve the
	 * entire first KiB so an unsupported layout can be audited offline.
	 */
	lo = buf[128];
	hi = buf[129];
	addr = ((u64)hi << 32) | (lo & 0xfffff000u);
	pci_info(v->pdev, "cmp40_preboot: SEC2_VM_PDB record=%08x:%08x addr=%#llx new_pt=%u target=%u\n",
		hi, lo, (unsigned long long)addr, !!(lo & (1u << 10)), lo & 3);
	if (!(lo & (1u << 10)) || (lo & 3) || !addr || addr >= VM_FB_SIZE)
		return -EOPNOTSUPP;
	/* All tested ACR virtual addresses are below 2 MiB, so the first
	 * entry of each upper directory is sufficient for this snapshot.
	 */
	for (i = 0; i < ARRAY_SIZE(levels); i++) {
		ret = vm_dump(v, levels[i], addr, buf, 64);
		if (ret)
			return ret;
		entry = vm_entry(buf);
		if (((entry >> 1) & 3) != 1)
			return -EOPNOTSUPP;
		addr = vm_entry_addr(entry);
		if (!addr || addr >= VM_FB_SIZE)
			return -ERANGE;
	}
	ret = vm_dump(v, "PD0", addr, buf, 64);
	if (ret)
		return ret;
	/* A 2 MiB leaf at PD0 is not decoded in this initial diagnostic. */
	if (buf[0] & 1)
		return -EOPNOTSUPP;
	small = ((vm_entry(buf + 2) >> 1) & 3) == 1;
	entry = vm_entry(buf + (small ? 2 : 0));
	if (((entry >> 1) & 3) != 1)
		return -EOPNOTSUPP;
	addr = small ? vm_entry_addr(entry) : ((entry >> 4) & 0x1fffffffull) << 8;
	ret = vm_dump(v, small ? "PT4K" : "PT64K", addr, buf, 128);
	if (ret)
		return ret;
	for (i = 0; i < ARRAY_SIZE(ptes); i++)
		ptes[i] = vm_entry(buf + i * 2);
	/* The audited 610 primary descriptor specifies code VA 0x1000,
	 * NS/SEC sizes 0x100/0x4100, data VA 0x5200, size 0x2300.
	 * Read all pages independently, without assuming contiguous FB.
	 * Original ciphertext/security state is preserved; no access control
	 * is changed if PRAMIN cannot read a protected image.
	 */
	ret = vm_range(v, "ACR_CODE", 0x1000, 0x4200, ptes, small, buf);
	if (ret)
		return ret;
	return vm_range(v, "ACR_DATA", 0x5200, 0x2300, ptes, small, buf);
}

static int vm_fault(struct vm_view *v, u32 *buf)
{
	static const char *const levels[] = { "FAULT_PD3", "FAULT_PD2", "FAULT_PD1" };
	u32 base = 0xb80000, bar2, lo, hi, get, put, size, i, target, index, buffer_lo, buffer_hi;
	u64 va, addr, entry;
	bool small;
	int ret;

	if (pci_resource_len(v->pdev, 0) < base + 0x3098)
		return -ENODEV;
	bar2 = readl(v->bar + base + 0xf48);
	lo = readl(v->bar + base + 0x3000);
	hi = readl(v->bar + base + 0x3004);
	buffer_lo = lo;
	buffer_hi = hi;
	get = readl(v->bar + base + 0x3008);
	put = readl(v->bar + base + 0x300c);
	size = readl(v->bar + base + 0x3010);
	pci_info(v->pdev, "cmp40_preboot: SEC2_VM_FAULT_BEGIN bar2=%08x buffer=%08x:%08x get=%08x put=%08x size=%08x\n",
		bar2, hi, lo, get, put, size);
	/* Buffer bases are BAR2 virtual addresses, not physical FB addresses:
	 * kgmmuEnableFaultBuffer_GV100 writes bar2FaultBufferAddr. Follow the
	 * BAR2 RAMIN/PDB and PTE before reading a pending NVC369 32-byte entry.
	 * No GET/VALID/status acknowledgement is performed.
	 */
	if (!(bar2 & (1u << 31)) || ((bar2 >> 28) & 3) ||
	    !(size & (1u << 31)) || get & ~0xfffffu || put & ~0xfffffu ||
	    get >= (size & 0xfffffu) || put >= (size & 0xfffffu))
		return -EOPNOTSUPP;
	if (get == put)
		return -ENODATA;
	va = ((u64)hi << 32 | lo) + (u64)get * 32;
	if (va >= (1ull << 29) || va & 31)
		return -ERANGE;
	addr = (u64)(bar2 & 0x0fffffffu) << 12;
	ret = vm_dump(v, "FAULT_INST", addr, buf, 1024);
	if (ret)
		return ret;
	lo = buf[128];
	hi = buf[129];
	addr = (u64)hi << 32 | (lo & 0xfffff000u);
	if (!(lo & (1u << 10)) || (lo & 3) || !addr || addr >= VM_FB_SIZE)
		return -EOPNOTSUPP;
	for (i = 0; i < ARRAY_SIZE(levels); i++) {
		ret = vm_dump(v, levels[i], addr, buf, 64);
		if (ret)
			return ret;
		entry = vm_entry(buf);
		if (((entry >> 1) & 3) != 1)
			return -EOPNOTSUPP;
		addr = vm_entry_addr(entry);
	}
	ret = vm_dump(v, "FAULT_PD0", addr + ((va >> 21) & 0xff) * 16, buf, 16);
	if (ret)
		return ret;
	if (buf[0] & 1)
		return -EOPNOTSUPP;
	small = ((vm_entry(buf + 2) >> 1) & 3) == 1;
	entry = vm_entry(buf + (small ? 2 : 0));
	if (((entry >> 1) & 3) != 1)
		return -EOPNOTSUPP;
	addr = small ? vm_entry_addr(entry) : ((entry >> 4) & 0x1fffffffull) << 8;
	index = small ? (va >> 12) & 0x1ff : (va >> 16) & 0x1f;
	ret = vm_dump(v, "FAULT_PTE", addr + index * 8, buf, 8);
	if (ret)
		return ret;
	entry = vm_entry(buf);
	target = (entry >> 1) & 3;
	if (!(entry & 1) || target == 1 || (entry & (1u << 4)))
		return -EOPNOTSUPP;
	addr = target ? ((entry >> 8) & 0x3fffffffffffull) << 12 : vm_entry_addr(entry);
	addr += va & (small ? 0xfff : 0xffff);
	pci_info(v->pdev, "cmp40_preboot: SEC2_VM_FAULT_MAP va=%#llx pte=%08x:%08x target=%u phys=%#llx\n",
		(unsigned long long)va, (u32)(entry >> 32), (u32)entry,
		target, (unsigned long long)addr);
	ret = vm_dump_target(v, "FAULT_RECORD", addr, buf, 32, target);
	if (ret)
		return ret;
	if (bar2 != readl(v->bar + base + 0xf48) ||
	    buffer_lo != readl(v->bar + base + 0x3000) || buffer_hi != readl(v->bar + base + 0x3004) ||
	    get != readl(v->bar + base + 0x3008) || put != readl(v->bar + base + 0x300c) ||
	    size != readl(v->bar + base + 0x3010))
		return -EAGAIN;
	return 0;
}

int cmp40_sec2_vm(struct pci_dev *pdev)
{
	struct vm_view v = { .pdev = pdev };
	u16 command;
	u32 *buf, instance_before;
	int ret, walk_status, fault_status;

	ret = pci_read_config_word(pdev, PCI_COMMAND, &command);
	if (ret || command == 0xffff || !(command & PCI_COMMAND_MEMORY) ||
	    !pdev->driver || strcmp(pdev->driver->name, "nvidia") ||
	    pci_resource_len(pdev, 0) < 0x840640)
		return ret ? ret : -ENODEV;
	buf = kmalloc(1024, GFP_KERNEL);
	if (!buf)
		return -ENOMEM;
	v.bar = ioremap(pci_resource_start(pdev, 0), pci_resource_len(pdev, 0));
	if (!v.bar) {
		kfree(buf);
		return -ENOMEM;
	}
	ret = -EAGAIN;
	if (readl(v.bar) != 0x166000a1 || readl(v.bar + 0x100ce0) != 0x208 ||
	    readl(v.bar + 0x840100) != 0 || readl(v.bar + 0x840040) != 0xfe ||
	    readl(v.bar + 0x840104) != 0xfd00)
		goto out;
	v.saved_window = readl(v.bar + VM_WINDOW);
	if (v.saved_window == ~0u || (v.saved_window & 0xffff0000u) == 0xbadf0000u)
		goto out;
	instance_before = readl(v.bar + 0x840620);
	pci_info(pdev, "cmp40_preboot: SEC2_VM_BEGIN window=%08x INSTBLK=%08x CHANNEL_CUR=%08x CHANNEL_NEXT=%08x STATUS_PAUSE=%08x\n",
		v.saved_window, instance_before, readl(v.bar + 0x840050),
		readl(v.bar + 0x840054), readl(v.bar + 0x84005c));
	walk_status = vm_walk(&v, buf);
	fault_status = vm_fault(&v, buf);
	pci_info(pdev, "cmp40_preboot: SEC2_VM_FAULT_END status=%d\n", fault_status);
	writel(v.saved_window, v.bar + VM_WINDOW);
	ret = (readl(v.bar + VM_WINDOW) == v.saved_window &&
	       readl(v.bar + 0x840620) == instance_before && readl(v.bar + 0x840100) == 0 &&
	       readl(v.bar + 0x840040) == 0xfe) ? 0 : -EAGAIN;
	pci_info(pdev, "cmp40_preboot: SEC2_VM_END window_restored=%u stable=%u walk_status=%d\n",
		readl(v.bar + VM_WINDOW) == v.saved_window, !ret, walk_status);
out:
	iounmap(v.bar);
	kfree(buf);
	return ret;
}
