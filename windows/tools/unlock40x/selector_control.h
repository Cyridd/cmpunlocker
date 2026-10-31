/* V67 negative control: change only the two selector data operands in the
 * copied payload. Never patch branch targets, addresses, PLM or WPR cleanup.
 * build_v70.sh additionally pins the entire input blob by SHA-256. */
#ifndef U40X_SELECTOR_CONTROL_H
#define U40X_SELECTOR_CONTROL_H

#if defined(SELECTOR_CONTROL_ZERO) && !defined(SELECTOR_CONTROL)
#error Zero selector expectation requires the dedicated control build
#endif

#ifdef SELECTOR_CONTROL_ZERO
#define U40X_EXPECTED_SS0 0U
#define U40X_EXPECTED_SS1 0U
#else
#define U40X_EXPECTED_SS0 0x88888888U
#define U40X_EXPECTED_SS1 8U
#endif

static EFI_STATUS
u40x_selector_payload(UINT8 *p, UINTN size, BOOLEAN zero)
{
    static const struct { UINTN off; UINT32 value; } anchors[] = {
        {0xF988, 0x88888888U}, {0xF98C, 0xCBD}, {0xF990, 0x10AA},
        {0xF994, 0xCBD}, {0xF998, 0x7D34}, {0xF9A4, 0x409664},
        {0xF9A8, 0xCBD}, {0xF9AC, 0x1F89}, {0xF9C0, 0x10AA},
        {0xBB20, 8}, {0xBB24, 0xCBD}, {0xBB28, 0xCBD},
        {0xBB38, 0x40966C}, {0xBB3C, 0xCBD}, {0xBB40, 0x1F89},
        {0xBB4C, 0xFFFFFF8FU}, {0xBB54, 0x10AA},
    };
    UINTN i, j;
    if (!p || size != 0xFA00) return EFI_BAD_BUFFER_SIZE;
    /* Validate everything before the first write, including both operands. */
    for (i = 0; i < sizeof(anchors) / sizeof(anchors[0]); i++) {
        UINT32 value = 0;
        for (j = 0; j < 4; j++) value |= (UINT32)p[anchors[i].off + j] << (8 * j);
        if (value != anchors[i].value) return EFI_COMPROMISED_DATA;
    }
    if (zero) {
        for (j = 0; j < 4; j++) {
            p[0xF988 + j] = 0;
            p[0xBB20 + j] = 0;
        }
    }
    return EFI_SUCCESS;
}
#endif
