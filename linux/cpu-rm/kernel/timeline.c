// SPDX-License-Identifier: GPL-2.0-only
/* Bounded SEC2 register timeline. No GPU writes, ports or acknowledgements. */
#include <linux/delay.h>
#include <linux/io.h>
#include <linux/module.h>
#include <linux/pci.h>
#include <linux/slab.h>
#include <linux/sysfs.h>
#include <linux/timekeeping.h>

#include "inspect.h"

#define TL_CAPACITY 512
#define TL_DURATION_NS (4ULL * NSEC_PER_SEC)
#define TL_LOG_SIZE ((TL_CAPACITY + 3) * 1024)

static char *timeline_log;
static size_t timeline_length;
static bool timeline_published;

static ssize_t timeline_read(struct file *file, struct kobject *kobj,
		const struct bin_attribute *attr, char *buf, loff_t off, size_t count)
{
	if (off < 0)
		return -EINVAL;
	if (off >= timeline_length)
		return 0;
	count = min_t(size_t, count, timeline_length - off);
	memcpy(buf, timeline_log + off, count);
	return count;
}

static struct bin_attribute timeline_attr = {
	.attr = { .name = "sec2_timeline", .mode = 0400 },
	.read = timeline_read,
};

void cmp40_sec2_timeline_release(void)
{
	if (timeline_published) {
		sysfs_remove_bin_file(&THIS_MODULE->mkobj.kobj, &timeline_attr);
		timeline_published = false;
	}
	kvfree(timeline_log);
	timeline_log = NULL;
	timeline_length = 0;
}

/* Use only previously audited readback offsets. Region fields stay raw. */
static const u32 offsets[] = {
	0x840624, 0xb83094, 0x840100, 0x840040, 0x840044,
	0x840604, 0x84010c, 0x840008, 0x1fa824, 0x1fa828,
	0x840050, 0x840054, 0x84005c, 0x840620, 0x840240,
	0x110100, 0x840104, 0x8403c0, 0x8403c4,
	0x840630, 0x840634, 0x840638, 0x84063c,
	0x840640, 0x840644, 0x840648, 0x84064c,
};

struct tl_record {
	u64 begin_ns, end_ns;
	u32 values[ARRAY_SIZE(offsets)];
	u32 ctl_again, fault_again;
};

static void sample(void __iomem *bar, struct tl_record *r)
{
	u32 i;

	r->begin_ns = ktime_get_ns();
	for (i = 0; i < ARRAY_SIZE(offsets); i++)
		r->values[i] = readl(bar + offsets[i]);
	r->ctl_again = readl(bar + offsets[0]);
	r->fault_again = readl(bar + offsets[1]);
	r->end_ns = ktime_get_ns();
}

static bool changed(const struct tl_record *a, const struct tl_record *b)
{
	return memcmp(a->values, b->values, sizeof(a->values)) ||
		a->ctl_again != b->ctl_again || a->fault_again != b->fault_again;
}

static void dump(const char *kind, u32 index,
		 const struct tl_record *r)
{
	/* One line per record keeps a parseable bounded log. Values are raw. */
	timeline_length += scnprintf(timeline_log + timeline_length, TL_LOG_SIZE - timeline_length, "cmp40_preboot: SEC2_TIMELINE_%s index=%u begin_ns=%llu end_ns=%llu ctl=%08x fault=%08x cpu=%08x mb0=%08x mb1=%08x trans1=%08x dma=%08x irq=%08x wpr_lo=%08x wpr_hi=%08x current=%08x next=%08x pause=%08x inst=%08x sctl=%08x gsp_cpu=%08x bootvec=%08x engine=%08x reset_plm=%08x r630=%08x r634=%08x r638=%08x r63c=%08x r640=%08x r644=%08x r648=%08x r64c=%08x ctl_again=%08x fault_again=%08x\n",
		kind, index, r->begin_ns, r->end_ns,
		r->values[0], r->values[1], r->values[2], r->values[3],
		r->values[4], r->values[5], r->values[6], r->values[7],
		r->values[8], r->values[9], r->values[10], r->values[11],
		r->values[12], r->values[13], r->values[14], r->values[15],
		r->values[16], r->values[17], r->values[18], r->values[19],
		r->values[20], r->values[21], r->values[22], r->values[23],
		r->values[24], r->values[25], r->values[26],
		r->ctl_again, r->fault_again);
}

int cmp40_sec2_timeline(struct pci_dev *pdev, bool *armed)
{
	struct tl_record *records, previous = {}, latest = {};
	void __iomem *bar;
	u64 start_ns, finish_ns;
	u32 count = 0, samples = 0, changes = 0, dropped = 0, i;
	u16 command;
	int ret;

	WRITE_ONCE(*armed, false);
	ret = pci_read_config_word(pdev, PCI_COMMAND, &command);
	if (ret || command == 0xffff || !(command & PCI_COMMAND_MEMORY) ||
	    !READ_ONCE(pdev->driver) || strcmp(pdev->driver->name, "nvidia") ||
	    !(pci_resource_flags(pdev, 0) & IORESOURCE_MEM) ||
	    pci_resource_len(pdev, 0) < 0xb83098)
		return ret ? ret : -ENODEV;
	records = kvcalloc(TL_CAPACITY, sizeof(*records), GFP_KERNEL);
	if (!records)
		return -ENOMEM;
	timeline_log = kvzalloc(TL_LOG_SIZE, GFP_KERNEL);
	if (!timeline_log) {
		ret = -ENOMEM;
		goto free_records;
	}
	bar = ioremap(pci_resource_start(pdev, 0), pci_resource_len(pdev, 0));
	if (!bar) {
		ret = -ENOMEM;
		goto free_records;
	}
	if (readl(bar) != 0x166000a1) {
		ret = -ENODEV;
		goto unmap;
	}
	start_ns = ktime_get_ns();
	timeline_length += scnprintf(timeline_log + timeline_length, TL_LOG_SIZE - timeline_length, "cmp40_preboot: SEC2_TIMELINE_BEGIN start_ns=%llu duration_ns=%llu capacity=%u sleep_min_us=100 sleep_max_us=200 gpu_writes=0\n",
		start_ns, TL_DURATION_NS, TL_CAPACITY);
	pci_info(pdev, "cmp40_preboot: SEC2_TIMELINE_BEGIN start_ns=%llu duration_ns=%llu capacity=%u sleep_min_us=100 sleep_max_us=200 gpu_writes=0\n",
		start_ns, TL_DURATION_NS, TL_CAPACITY);
	/* Sysfs parameters exist during module_init. Publish only once all
	 * guards/allocation/mapping have succeeded. The wrapper waits for Y.
	 */
	WRITE_ONCE(*armed, true);
	do {
		sample(bar, &latest);
		samples++;
		if (!count || changed(&latest, &previous)) {
			changes++;
			if (count < TL_CAPACITY)
				records[count++] = latest;
			else
				dropped++;
		}
		previous = latest;
		usleep_range(100, 200);
	} while (ktime_get_ns() - start_ns < TL_DURATION_NS);
	finish_ns = ktime_get_ns();
	WRITE_ONCE(*armed, false);
	for (i = 0; i < count; i++)
		dump("CHANGE", i, &records[i]);
	dump("LAST", samples - 1, &latest);
	timeline_length += scnprintf(timeline_log + timeline_length, TL_LOG_SIZE - timeline_length, "cmp40_preboot: SEC2_TIMELINE_END start_ns=%llu finish_ns=%llu samples=%u changes=%u stored=%u dropped=%u gpu_writes=0\n",
		start_ns, finish_ns, samples, changes, count, dropped);
	pci_info(pdev, "cmp40_preboot: SEC2_TIMELINE_END start_ns=%llu finish_ns=%llu samples=%u changes=%u stored=%u dropped=%u gpu_writes=0\n",
		start_ns, finish_ns, samples, changes, count, dropped);
	timeline_attr.size = timeline_length;
	ret = sysfs_create_bin_file(&THIS_MODULE->mkobj.kobj, &timeline_attr);
	if (!ret)
		timeline_published = true;
unmap:
	iounmap(bar);
free_records:
	kvfree(records);
	if (ret)
		cmp40_sec2_timeline_release();
	return ret;
}
