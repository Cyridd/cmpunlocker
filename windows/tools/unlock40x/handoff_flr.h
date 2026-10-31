/* PCI configuration-only FLR for the TU106 handoff experiment.
 * The callbacks use the address encoding selected during GPU discovery.
 * Keep this independent of MMIO, console, filesystem and runtime services.
 */
#ifndef U40X_HANDOFF_FLR_H
#define U40X_HANDOFF_FLR_H

typedef struct {
    EFI_STATUS (*read32)(UINTN offset, UINT32 *value);
    EFI_STATUS (*write16)(UINTN offset, UINT16 value);
    void (*stall)(UINTN usec);
} U40X_FLR_IO;

typedef struct {
    UINTN cap;
    UINT16 devctl;
} U40X_FLR_PLAN;

static EFI_STATUS
u40x_flr_preflight(const U40X_FLR_IO *io, U40X_FLR_PLAN *plan)
{
    UINT32 v;
    UINTN cap, hops;
    EFI_STATUS st;
    plan->cap = 0;
    st = io->read32(0, &v);
    if (EFI_ERROR(st)) return st;
    if (v != 0x1F0B10DEU) return EFI_UNSUPPORTED;
    st = io->read32(0x04, &v);
    if (EFI_ERROR(st)) return st;
    if (v == 0xFFFFFFFFU) return EFI_DEVICE_ERROR;
    if (!(v & (1U << 20))) return EFI_UNSUPPORTED; /* Status.CapList */
    st = io->read32(0x34, &v);
    if (EFI_ERROR(st)) return st;
    cap = v & 0xFFU;
    /* A corrupt/cyclic capability list must never hang the firmware. */
    for (hops = 0; cap && hops < 48; hops++) {
        if (cap < 0x40 || cap > 0xFC || (cap & 3))
            return EFI_DEVICE_ERROR;
        st = io->read32(cap, &v);
        if (EFI_ERROR(st)) return st;
        if (v == 0xFFFFFFFFU) return EFI_DEVICE_ERROR;
        if ((v & 0xFFU) == 0x10) {
            if (cap > 0xF4 || ((v >> 20) & 0xFU) != 0)
                return EFI_UNSUPPORTED; /* PCIe endpoint only */
            st = io->read32(cap + 4, &v);
            if (EFI_ERROR(st)) return st;
            if (v == 0xFFFFFFFFU) return EFI_DEVICE_ERROR;
            if (!(v & (1U << 28))) return EFI_UNSUPPORTED;
            plan->cap = cap;
            return EFI_SUCCESS;
        }
        cap = (v >> 8) & 0xFFU;
    }
    return cap ? EFI_DEVICE_ERROR : EFI_NOT_FOUND;
}

/* SEC2 must already be stopped. Disable bus mastering, verify it, and drain
 * outstanding transactions before FLR. Use WORD writes: DWORD writes would
 * touch the adjacent write-one-to-clear PCI Status / PCIe Device Status.
 * On failure leave bus mastering disabled; do not resume a partial unlock.
 */
static EFI_STATUS
u40x_flr_drain(const U40X_FLR_IO *io, U40X_FLR_PLAN *plan)
{
    UINT32 v;
    UINTN poll;
    EFI_STATUS st;
    if (!plan->cap) return EFI_NOT_READY;
    st = io->read32(0x04, &v);
    if (EFI_ERROR(st)) return st;
    if (v == 0xFFFFFFFFU) return EFI_DEVICE_ERROR;
    st = io->write16(0x04, (UINT16)(v & ~4U));
    if (EFI_ERROR(st)) return st;
    st = io->read32(0x04, &v);
    if (EFI_ERROR(st)) return st;
    if (v == 0xFFFFFFFFU || (v & 4U)) return EFI_DEVICE_ERROR;
    for (poll = 0; poll < 100; poll++) {
        st = io->read32(plan->cap + 8, &v);
        if (EFI_ERROR(st)) return st;
        if (v == 0xFFFFFFFFU) return EFI_DEVICE_ERROR;
        if (!(v & (1U << 21))) { /* Device Status.Transactions Pending */
            plan->devctl = (UINT16)v;
            return EFI_SUCCESS;
        }
        io->stall(10000);
    }
    return EFI_TIMEOUT;
}

/* Terminal boundary: no access to the GPU after this write, including on
 * an ambiguous firmware error. This reports submission, not reset completion
 * or preservation of the unlock. Only Windows can validate those outcomes.
 */
static EFI_STATUS
u40x_flr_commit(const U40X_FLR_IO *io, const U40X_FLR_PLAN *plan)
{
    EFI_STATUS st = io->write16(plan->cap + 8, plan->devctl | 0x8000U);
    io->stall(300000);
    return st;
}
#endif
