// SPDX-License-Identifier: GPL-2.0-only
#ifdef __KERNEL__
#include <linux/firmware.h>
#include <linux/string.h>
#else
#include <errno.h>
#include <string.h>
#endif

#include "boot.h"
#include "diagnostic.h"
#include "prep.h"

#define BOOTER_MAGIC 0x314c5442u

static int parse_bundle(const struct firmware *fw,
			struct turing_unlock_booter_hdr *hdr, bool unload)
{
	const u8 *d;
	u32 magic, version, image_size, sig_bytes;

	if (!fw || !hdr)
		return GU_DIAG(-EINVAL);
	d = fw->data;
	if (!d || fw->size < 72)
		return GU_DIAG(-EINVAL);
	memcpy(&magic, d, 4);
	memcpy(&version, d + 4, 4);
	if (magic != BOOTER_MAGIC || version != 1)
		return GU_DIAG(-EINVAL);
	memcpy(hdr, d + 8, sizeof(*hdr));
	memcpy(&image_size, d + 8 + sizeof(*hdr), 4);
	memcpy(&sig_bytes, d + 12 + sizeof(*hdr), 4);
	if (image_size != (unload ? TU_UNLOAD_IMAGE_SIZE : TU_BOOTER_IMAGE_SIZE) || sig_bytes != TU_BOOTER_SIG_TOTAL ||
	    sig_bytes != hdr->sig_bytes || fw->size != 72u + image_size + sig_bytes)
		return GU_DIAG(-EINVAL);
	if (hdr->ucode_id != 13 || hdr->engine_id != 1 || hdr->num_apps != 1 ||
	    hdr->num_sigs != 1 || hdr->os_code_off != 0 ||
	    hdr->os_code_size != 256 || hdr->os_data_off != (unload ? 19456 : 34048) ||
	    hdr->os_data_size != (unload ? 19712 : 25088) || hdr->app_code_off != 256 ||
	    hdr->app_code_size != (unload ? 19200 : 33792) || hdr->app_data_off != 256 ||
	    hdr->app_data_size != 0 || hdr->patch_loc != (unload ? 19968 : 34560))
		return GU_DIAG(-EINVAL);
	GU_NOTE("booter geometry ok: image=%u sig=%u patch_loc=%#x\n",
		image_size, sig_bytes, hdr->patch_loc);
	return 0;
}

int turing_unlock_booter_parse(const struct firmware *fw,
				  struct turing_unlock_booter_hdr *hdr)
{
	return parse_bundle(fw, hdr, false);
}

int turing_unlock_unload_parse(const struct firmware *fw,
				  struct turing_unlock_booter_hdr *hdr)
{
	return parse_bundle(fw, hdr, true);
}

int turing_unlock_boot_desc_v4(const u8 *data, size_t size, u32 image_size,
				 struct turing_unlock_boot_desc *out)
{
	u32 version;

	if (!data || !out || size != 76 || !image_size || image_size & 4095)
		return GU_DIAG(-EINVAL);
	memcpy(&version, data, 4);
	if (version != 4)
		return GU_DIAG(-EINVAL);
	out->code_offset = 0;
	out->data_offset = 0;
	out->manifest_offset = 0;
	out->image_size = image_size;
	return 0;
}

bool turing_unlock_fb_range_ready(u32 range)
{
	return range != ~0u && (range & 0xffff0000u) != 0xbadf0000u &&
		((range >> 4) & 0x3fu);
}
