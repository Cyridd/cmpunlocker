/* Experiment 10: native TU102 FWSEC-SB (0x19), as prepared and checked by
 * kernel_gsp_frts_tu102.c. This header is compiled only for the new controls.
 * SB restores PreOsApps; it does not start GSP-RM or write the Windows registry.
 */
#ifndef U40X_FWSEC_SB_H
#define U40X_FWSEC_SB_H

#define U40X_SB_GFW_PLM 0x00118128U
#define U40X_SB_SCRATCH15 0x00001454U
#define U40X_SB_IRQSCLR (GSP_BASE + 0x04U)
#define U40X_SB_IRQSTAT (GSP_BASE + 0x08U)

static BOOLEAN u40x_fwsec_sb_patch(UINT8 *dmem)
{
    UINT32 *mapper = (UINT32 *)(dmem + FW40_MAPPER_OFF);
    UINT32 *cmd = (UINT32 *)(dmem + FW40_CMDIN_OFF);
    /* Pin the measured native interface, including its whole command buffer.
     * Refuse another layout before reset or any out-of-bounds patch. */
    static const UINT8 iface[] = {1,4,8,2,4,0,0,0,0x60,3,0,0};
    UINTN i;
    for (i = 0; i < sizeof(iface); i++)
        if (dmem[FW40_IFACE_OFF + i] != iface[i]) return FALSE;
    if (mapper[0] != 0x50414D44U || mapper[1] != 0x00400003U ||
        mapper[2] != FW40_CMDIN_OFF || mapper[3] != 0x40U ||
        FW40_CMDIN_OFF + 0x40U > FW40_DMEM_SIZE) return FALSE;
    SetMem(cmd, 0x40, 0);
    cmd[0] = 1; cmd[1] = 24; cmd[5] = 2;
    mapper[11] = 0x19;
    return TRUE;
}

static BOOLEAN u40x_fwsec_sb_prestart(void)
{
    UINT32 irq;
    /* Fresh HALT IRQ distinguishes completion of this launch from the
     * already halted FRTS/failed-start state. Clear only GSP's HALT bit. */
    mmio_write32(U40X_SB_IRQSCLR, 0x10);
    irq = mmio_read32(U40X_SB_IRQSTAT);
    Print(L"[sb] prestart IRQSTAT=%08x (HALT must be clear)\n", irq);
    return !u40x_mmio_unavailable(irq) && !(irq & 0x10U);
}

static BOOLEAN u40x_fwsec_sb_wait(void)
{
    UINTN i;
    Print(L"[sb] STARTCPU cmd=19; wait for fresh HALT and native SB status (5s)\n");
    for (i = 0; i < 5000; i++) {
        UINT32 cpu, irq;
        BS->Stall(1000);
        cpu = mmio_read32(GSP_CPUCTL);
        irq = mmio_read32(U40X_SB_IRQSTAT);
        if (u40x_mmio_unavailable(cpu) || u40x_mmio_unavailable(irq)) {
            Print(L"[sb] inaccessible GSP: CPU=%08x IRQ=%08x\n", cpu, irq);
            return FALSE;
        }
        if ((cpu & 0x10U) && (irq & 0x10U)) {
            UINT32 plm = mmio_read32(U40X_SB_GFW_PLM);
            UINT32 gfw, scratch, engine, dma, hwcfg;
            Print(L"[sb] halted after %dms CPU=%08x IRQ=%08x GFW_PLM=%08x\n",
                  (INTN)i + 1, cpu, irq, plm);
            /* NVIDIA checks read permission before accessing GFW progress. */
            if (u40x_mmio_unavailable(plm) || !(plm & 1U)) return FALSE;
            gfw = mmio_read32(REG_GFW_BOOT_OK);
            scratch = mmio_read32(U40X_SB_SCRATCH15);
            engine = mmio_read32(GSP_ENGINE);
            dma = mmio_read32(GSP_DMACTL);
            hwcfg = mmio_read32(GSP_HWCFG2);
            Print(L"[sb] RESULT GFW=%08x SCRATCH15=%08x SB_ERR=%04x ENGINE=%08x DMACTL=%08x HWCFG2=%08x\n",
                  gfw, scratch, scratch & 0xFFFFU, engine, dma, hwcfg);
            if (u40x_mmio_unavailable(gfw) || u40x_mmio_unavailable(scratch) ||
                u40x_mmio_unavailable(engine) || u40x_mmio_unavailable(dma) ||
                u40x_mmio_unavailable(hwcfg)) return FALSE;
            return (gfw & 0xFFU) == 0xFFU && !(scratch & 0xFFFFU) &&
                !(engine & 1U) && !(dma & 6U) && !(hwcfg & (1U << 12));
        }
    }
    Print(L"[sb] completion timeout: CPU=%08x IRQ=%08x DEBUG=%08x MB0=%08x MB1=%08x\n",
          mmio_read32(GSP_CPUCTL), mmio_read32(U40X_SB_IRQSTAT),
          mmio_read32(GSP_BASE + 0x94), mmio_read32(GSP_MAILBOX0),
          mmio_read32(GSP_MAILBOX1));
    return FALSE;
}
#endif
