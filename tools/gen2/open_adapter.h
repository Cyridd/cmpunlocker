/* SPDX-License-Identifier: GPL-2.0-only */
/* Included by nv.c; the shared policy engine is copied verbatim by the generator. */
#ifdef CMP40_GEN2_DIAGNOSTIC
#define CMP40_GEN2_LOG "CMP40_PCIE_GEN2_DIAG"
#else
#define CMP40_GEN2_LOG "CMP40_PCIE_GEN2_V2"
#endif
#include "gen2_core.h"

static unsigned int cmp40_gen2_early = 1;
module_param(cmp40_gen2_early, uint, 0444);
MODULE_PARM_DESC(cmp40_gen2_early,
    "CMP40 post-Booter Gen2: 0=policy, 1=policy+TLS (default), 2=also early retrain");

/* Called after the checked stock Booter, at GSP-ready, and after rm_init_adapter.
 * Keep this ABI identical in the RM and kernel-open os-interface.h copies.
 * RM callbacks use the existing full BAR mapping and never sleep or allocate.
 */
void NV_API_CALL os_cmp40_gen2(void *os_gpu, NvU32 stage)
{
    nv_state_t *nv = os_gpu;
    nv_linux_state_t *nvl;
    struct pci_dev *gpu;
    void __iomem *bar;
    struct cmp40_g2_result result;
    enum cmp40_g2_action action;
    u16 command;
    int ret;
    const char *phase;

    if (!nv || stage > 2)
        return;
    nvl = NV_GET_NVL_FROM_NV_STATE(nv);
    gpu = nvl->pci_dev;
    if (!gpu || gpu->vendor != PCI_VENDOR_ID_NVIDIA || gpu->device != 0x1f0b)
        return;
    phase = stage == 0 ? "post_booter" : stage == 1 ? "gsp_ready" : "post_rm_init";
    if (cmp40_gen2_early > 2) {
        pci_err(gpu, CMP40_GEN2_LOG " invalid cmp40_gen2_early=%u; use 0..2\n",
                cmp40_gen2_early);
        return;
    }
    if (!nv->regs || !nv->regs->map || nv->regs->size < CHECK_SIZE ||
        !(pci_resource_flags(gpu, 0) & IORESOURCE_MEM) ||
        pci_resource_len(gpu, 0) < CHECK_SIZE || gpu->current_state != PCI_D0 ||
        pci_read_config_word(gpu, PCI_COMMAND, &command) || command == 0xffff ||
        !(command & PCI_COMMAND_MEMORY)) {
        pci_err(gpu, CMP40_GEN2_LOG " phase=%s BAR0/power guard failed\n", phase);
        return;
    }
    bar = (void __iomem *)nv->regs->map;
    action = stage == 0 ? (enum cmp40_g2_action)cmp40_gen2_early :
             stage == 1 ? CMP40_G2_PREPARE : CMP40_G2_RETRAIN;
    pci_info(gpu, CMP40_GEN2_LOG " phase=%s action=%u\n", phase, action);
#ifdef CMP40_GEN2_DIAGNOSTIC
    pci_info(gpu, CMP40_GEN2_LOG " phase=%s HIER=%08x BAR_CAP=%08x BAR_CAP2=%08x BAR_CTL2=%08x\n",
             phase, readl(bar + 0x88610), readl(bar + 0x88084),
             readl(bar + 0x880a4), readl(bar + 0x880a8));
#endif
    ret = cmp40_g2_run(gpu, bar, action, false, stage != 2, &result);
    pci_info(gpu, CMP40_GEN2_LOG " phase=%s result=%d device_ok=%u STA=%04x\n",
             phase, ret, result.device_ok, result.link_status);
    /* Gen2 failure is independent of the completed compute unlock. */
}
