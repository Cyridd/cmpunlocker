// SPDX-License-Identifier: GPL-2.0-only
/* Exercise the same parsers and WPR layout using actual pinned assets and ROM. */
#include <assert.h>
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "kernel/boot.h"
#include "kernel/prep.h"
#include "kernel/vbios.h"

static u8 *load(const char *path, size_t *size)
{
    FILE *f = fopen(path, "rb");
    long bytes;
    u8 *data;
    if (!f) { perror(path); exit(1); }
    assert(!fseek(f, 0, SEEK_END));
    bytes = ftell(f);
    assert(bytes > 0 && bytes <= 128 * 1024 * 1024);
    *size = (size_t)bytes;
    rewind(f);
    data = malloc(*size);
    assert(data && fread(data, 1, *size, f) == *size);
    assert(!fclose(f));
    return data;
}

int main(int argc, char **argv)
{
    struct turing_unlock_fwsec fwsec;
    struct turing_unlock_booter_hdr hdr;
    struct turing_unlock_boot_desc boot;
    struct turing_unlock_span span;
    struct turing_unlock_radix_plan radix;
    struct turing_unlock_layout_in in = {0};
    struct turing_unlock_layout_out layout;
    struct turing_unlock_wpr_args args = {0};
    struct firmware bundle;
    u8 meta[GU_WPR_META_SIZE], table[GU_RADIX_PAGE];
    u64 child = 0x100000;
    u8 *rom = NULL, *gsp, *booter, *desc;
    size_t rom_size = 0, gsp_size, booter_size, desc_size;

    if (argc > 2) { fprintf(stderr, "usage: test_offline [CMP40HX.rom]\n"); return 2; }
    if (argc == 2) rom = load(argv[1], &rom_size);
    gsp = load("assets/gsp_tu10x.bin", &gsp_size);
    booter = load("assets/booter_load_tu102.bin", &booter_size);
    desc = load("assets/gsp_boot_desc_tu102.bin", &desc_size);

    bundle.size = booter_size;
    bundle.data = booter;
    assert(!turing_unlock_booter_parse(&bundle, &hdr));
    assert(!turing_unlock_booter_patch(booter + 72, TU_BOOTER_IMAGE_SIZE,
                &hdr, booter + 72 + TU_BOOTER_IMAGE_SIZE, TU_BOOTER_SIG_TOTAL, 0));
    {
        size_t size;
        u8 *unload = load("assets/booter_unload_tu102.bin", &size);
        struct firmware ub = { .size = size, .data = unload };
        struct turing_unlock_booter_hdr uh;
        assert(!turing_unlock_unload_parse(&ub, &uh));
        assert(turing_unlock_booter_parse(&ub, &uh) == -EINVAL);
        assert(!turing_unlock_booter_patch(unload + 72, TU_UNLOAD_IMAGE_SIZE,
                    &uh, unload + 72 + TU_UNLOAD_IMAGE_SIZE, TU_BOOTER_SIG_TOTAL, 0));
        ub.size--;
        assert(turing_unlock_unload_parse(&ub, &uh) == -EINVAL);
        ub.size++;
        unload[40] ^= 1; /* app_code_off must be the pinned 256-byte boundary */
        assert(turing_unlock_unload_parse(&ub, &uh) == -EINVAL);
        free(unload);
    }
    assert(!turing_unlock_boot_desc_v4(desc, desc_size, 4096, &boot));
    assert(!turing_unlock_elf_section(gsp, gsp_size, ".fwimage", &span));
    assert(!turing_unlock_radix_plan(span.size, &radix));
    assert(!turing_unlock_radix_page(table, &child, 1));
    assert(table[2] == 0x10 && table[8] == 0);

    if (rom) {
        assert(!turing_unlock_vbios_fwsec(rom, rom_size, &fwsec));
        assert(fwsec.desc_version == 2);
    }
    in.usable_fb = 8ull << 30;
    in.vga_base = in.usable_fb - (1ull << 20);
    in.frts_size = in.nonwpr_size = 1ull << 20;
    in.boot_size = boot.image_size;
    in.image_size = span.size;
    in.heap_request = 88ull << 20;
    assert(!turing_unlock_layout(&in, &layout));
    args.fb_size = in.usable_fb;
    args.vga_offset = in.vga_base;
    args.frts_size = in.frts_size;
    args.signature_dma = 0x100000;
    args.signature_size = 0xfa00;
    args.radix_dma = 0x200000;
    args.image_size = span.size;
    args.boot_dma = 0x300000;
    args.boot = boot;
    assert(!turing_unlock_wpr_meta_pack(meta, &layout, &args));
    /* The small heap must move the native first ACR page outside WPR. */
    assert(0x1fdf97000ull >= layout.wpr_start &&
           0x1fdf97000ull < layout.heap_offset + layout.heap_size);
    in.heap_request = 1ull << 20;
    assert(!turing_unlock_layout(&in, &layout));
    assert(layout.nonwpr_start == 0x1fdf00000ull);
    assert(layout.wpr_start == 0x1fe000000ull);
    assert(layout.heap_offset == 0x1fe100000ull);
    assert(layout.heap_size == 0x100000ull);
    assert(layout.gsp_offset == 0x1fe200000ull);
    assert(layout.wpr_end == 0x1fff00000ull);
    assert(0x1fdf97000ull < layout.wpr_start);
    assert(!turing_unlock_wpr_meta_pack(meta, &layout, &args));
    assert(meta[128] == 0 && meta[129] == 0 && meta[130] == 0x10 &&
           meta[131] == 0 && meta[132] == 0 && meta[135] == 0);
    in.heap_request = 88ull << 20;
    assert(!turing_unlock_layout(&in, &layout));
    if (rom) assert(!turing_unlock_fwsec_patch_frts(rom + fwsec.image_off, &fwsec,
                layout.frts_offset));

    /* The saved board ROM carries IFR; a normalized PCI image must agree. */
    if (rom) {
        struct turing_unlock_fwsec normalized;
        assert(!turing_unlock_vbios_fwsec(rom + 0x28600,
                    rom_size - 0x28600, &normalized));
        assert(normalized.image_off + 0x28600 == fwsec.image_off);
        assert(normalized.imem_size == fwsec.imem_size);
        assert(normalized.dmem_size == fwsec.dmem_size);
        rom[0] ^= 1;
        assert(turing_unlock_vbios_fwsec(rom, rom_size, &normalized) != 0);
        rom[0] ^= 1;
        rom[0x4000] ^= 1;
        assert(turing_unlock_vbios_fwsec(rom, rom_size, &normalized) != 0);
        rom[0x4000] ^= 1;
    }

    /* Truncated firmware, impossible geometry and unaligned DMA are refused. */
    bundle.size = 71;
    assert(turing_unlock_booter_parse(&bundle, &hdr) == -EINVAL);
    assert(turing_unlock_boot_desc_v4(desc, 75, 4096, &boot) == -EINVAL);
    assert(turing_unlock_elf_section(gsp, 63, ".fwimage", &span) == -EINVAL);
    {
        u8 invalid_rom[26] = {0};
        assert(turing_unlock_vbios_fwsec(invalid_rom, sizeof(invalid_rom), &fwsec) != 0);
    }
    child++;
    assert(turing_unlock_radix_page(table, &child, 1) == -EINVAL);
    args.radix_dma++;
    assert(turing_unlock_wpr_meta_pack(meta, &layout, &args) == -EINVAL);
    in.vga_base = 0x10000;
    assert(turing_unlock_layout(&in, &layout) != 0);
    booter[0] ^= 1;
    bundle.size = booter_size;
    assert(turing_unlock_booter_parse(&bundle, &hdr) == -EINVAL);

    puts("OFFLINE_PASS: pinned Booter/GSP assets, 8GiB WPR layout, malformed-input rejection");
    if (rom) puts("ROM_PASS: saved CMP40HX production FWSEC and IFR normalization");
    free(desc); free(booter); free(gsp); free(rom);
    return 0;
}
