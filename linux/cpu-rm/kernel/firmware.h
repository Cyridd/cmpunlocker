// SPDX-License-Identifier: GPL-2.0-only
#ifndef TURING_UNLOCK_FIRMWARE_H
#define TURING_UNLOCK_FIRMWARE_H

#include <linux/device.h>
#include <linux/firmware.h>
#include <linux/slab.h>
#include <linux/string.h>
#include <crypto/hash.h>
#include <crypto/sha2.h>

#include "asset_hashes.h"

static inline int cmp40_verify_asset(const struct firmware *fw, const char *name)
{
	const char *leaf = strrchr(name, '/');
	u8 digest[SHA256_DIGEST_SIZE];
	size_t i;

	leaf = leaf ? leaf + 1 : name;
	for (i = 0; i < ARRAY_SIZE(cmp40_asset_hashes); i++) {
		const struct cmp40_asset_hash *entry = &cmp40_asset_hashes[i];
		struct crypto_shash *tfm;
		struct shash_desc *desc;
		int ret;

		if (strcmp(leaf, entry->name))
			continue;
		if (!fw || fw->size != entry->size)
			return -EBADMSG;
		tfm = crypto_alloc_shash("sha256", 0, 0);
		if (IS_ERR(tfm))
			return PTR_ERR(tfm);
		desc = kmalloc(sizeof(*desc) + crypto_shash_descsize(tfm), GFP_KERNEL);
		if (!desc) {
			crypto_free_shash(tfm);
			return -ENOMEM;
		}
		desc->tfm = tfm;
		ret = crypto_shash_digest(desc, fw->data, fw->size, digest);
		kfree(desc);
		crypto_free_shash(tfm);
		if (ret)
			return ret;
		return memcmp(digest, entry->digest, sizeof(digest)) ? -EBADMSG : 0;
	}
	return -EBADMSG;
}

/* CONFIG_FW_CACHE attaches name records to devres even though this is not
 * devm_request_firmware(). release_firmware() does not remove them, and
 * really_probe() refuses a device with any existing devres. Scope each
 * synchronous request so only its cache records are released; the returned
 * firmware remains owned by the caller.
 */
static inline int turing_unlock_request_scoped(struct device *dev,
					    const struct firmware **fw, const char *name)
{
	void *group;
	int ret, released;

	group = devres_open_group(dev, NULL, GFP_KERNEL);
	if (!group)
		return -ENOMEM;
	*fw = NULL;
	dev_info(dev, "asset request: %s\n", name);
	ret = request_firmware(fw, name, dev);
	released = devres_release_group(dev, group);
	dev_info(dev, "asset result: %s error=%d bytes=%zu\n",
		name, ret, ret ? (size_t)0 : (*fw)->size);
	dev_info(dev, "asset cache cleanup: %s devres_released=%d\n", name, released);
	if (!ret) {
		ret = cmp40_verify_asset(*fw, name);
		dev_info(dev, "asset SHA-256 check: %s error=%d\n", name, ret);
		if (ret) {
			release_firmware(*fw);
			*fw = NULL;
		}
	}
	return ret;
}

static inline int turing_unlock_check_devres(struct device *dev, const char *phase)
{
	unsigned long flags;
	bool empty;

	spin_lock_irqsave(&dev->devres_lock, flags);
	empty = list_empty(&dev->devres_head);
	spin_unlock_irqrestore(&dev->devres_lock, flags);
	dev_info(dev, "driver probe resources: phase=%s empty=%u\n", phase, empty);
	return empty ? 0 : -EBUSY;
}

#endif
