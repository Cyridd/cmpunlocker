// SPDX-License-Identifier: GPL-2.0-only
/* CMP40HX-only pre-driver unlock; exact target and explicit mode required. */
#include <linux/init.h>
#include <linux/module.h>
#include <linux/pci.h>
#include <linux/pm_runtime.h>
#include <linux/string.h>

#include "inspect.h"
#include "gen2.h"
#include "unlock.h"

static char *target;
module_param(target, charp, 0444);
MODULE_PARM_DESC(target, "Exact PCI address of one CMP40HX, e.g. 0000:10:00.0");

static char *mode = "inspect";
module_param(mode, charp, 0444);
MODULE_PARM_DESC(mode, "inspect/sec2_timeline: read only; sec2_ports/sec2_vm: diagnostic reads with restored ports/window; direct: SS0/SS1; unlock: Booter; gen2_runtime: explicit idle CPU-RM PCIe experiment");
static bool timeline_armed;
module_param(timeline_armed, bool, 0444);
MODULE_PARM_DESC(timeline_armed, "Read-only SEC2 timeline is sampling; start the first native query now");

static int unlock_error;
module_param(unlock_error, int, 0444);
MODULE_PARM_DESC(unlock_error, "Operation errno; always check before loading NVIDIA");
static bool resources_retained;
module_param(resources_retained, bool, 0444);
MODULE_PARM_DESC(resources_retained, "Firmware could not stop; keep module resident and power off");
static bool cpu_rm_handoff = true;
module_param(cpu_rm_handoff, bool, 0444);
MODULE_PARM_DESC(cpu_rm_handoff, "Reset stopped SEC2, clear DMEM, restore stock FECS protection before CPU-RM");
static bool stock_handoff = true;
module_param(stock_handoff, bool, 0444);
MODULE_PARM_DESC(stock_handoff, "Use published CMP40HX GPU-side selector and stock-FECS-relock payload");
static bool locked_control;
module_param(locked_control, bool, 0444);
MODULE_PARM_DESC(locked_control, "Diagnostic stock-handoff control with SS0/SS1 kept zero");
static bool signed_unload;
module_param(signed_unload, bool, 0444);
MODULE_PARM_DESC(signed_unload, "Diagnostic: force production Booter Unload after WPR2 teardown, before CPU-RM");
static bool unload_before_down;
module_param(unload_before_down, bool, 0444);
MODULE_PARM_DESC(unload_before_down, "Diagnostic: preserve the measured FWSEC WPR2 range in the payload, then run signed Unload before teardown");
static bool unload_full_wpr;
module_param(unload_full_wpr, bool, 0444);
MODULE_PARM_DESC(unload_full_wpr, "Diagnostic: substitute complete planned Booter WPR bounds, rather than FRTS-only, before signed Unload");
static bool small_wpr_heap = true;
module_param(small_wpr_heap, bool, 0444);
MODULE_PARM_DESC(small_wpr_heap, "CPU-RM handoff: 1-MiB Booter WPR heap instead of 88 MiB; required for the tested proprietary path");
static bool loader_control;
module_param(loader_control, bool, 0444);
MODULE_PARM_DESC(loader_control, "Diagnostic: run an internal DMEM marker/EXIT NS probe through the native generic SEC2 loader");
static bool loader_fb_control;
module_param(loader_fb_control, bool, 0444);
MODULE_PARM_DESC(loader_fb_control, "Diagnostic: source the NS marker probe from temporary physical local FB instead of coherent sysmem");
static bool loader_virtual_control;
module_param(loader_virtual_control, bool, 0444);
MODULE_PARM_DESC(loader_virtual_control, "Diagnostic: use separate temporary local-FB instance/page tables and ctxDma 1 for the NS marker probe");
static bool loader_native_fbif;
module_param(loader_native_fbif, bool, 0444);
MODULE_PARM_DESC(loader_native_fbif, "Diagnostic: repeat the virtual NS probe using the exact native-capture FBIF_CTL value 0x10");
static bool loader_native_addresses;
module_param(loader_native_addresses, bool, 0444);
MODULE_PARM_DESC(loader_native_addresses, "Diagnostic: NS probe at native instance 0x1fdf96000 / first code PA 0x1fdf97000, with WPR2 down");
static bool loader_native_physical;
module_param(loader_native_physical, bool, 0444);
MODULE_PARM_DESC(loader_native_physical, "Diagnostic: physical FB NS probe from first native code PA 0x1fdf97000, with WPR2 down");
static unsigned int loader_boundary;
module_param(loader_boundary, uint, 0444);
MODULE_PARM_DESC(loader_boundary, "Diagnostic physical NS probe: 1 last page before former WPR, 2 first WPR page, 3 first heap page");
static bool mmu_invalidate;
module_param(mmu_invalidate, bool, 0444);
MODULE_PARM_DESC(mmu_invalidate, "Diagnostic: invalidate all MMU PDB/VA mappings with global ACK after quiesced CPU-RM handoff");

static char *gen2 = "off";
module_param(gen2, charp, 0444);
MODULE_PARM_DESC(gen2, "Opt-in pre-driver Gen2 experiment: off/prepare/retrain (normal CPU-RM unlock only)");
static char *gen2_policy = "rmw";
module_param(gen2_policy, charp, 0444);
MODULE_PARM_DESC(gen2_policy, "Gen2 register policy: rmw preserves unrelated bits; windows uses the Windows full MISC/CYA constants");
static int gen2_error;
module_param(gen2_error, int, 0444);
MODULE_PARM_DESC(gen2_error, "Separate Gen2 errno; never causes compute-unlock FLR");
static bool gen2_device_ok;
module_param(gen2_device_ok, bool, 0444);
MODULE_PARM_DESC(gen2_device_ok, "Gen2 experiment ended with accessible GPU, active link and full compute selectors");
static unsigned int gen2_link_status;
module_param(gen2_link_status, uint, 0444);
MODULE_PARM_DESC(gen2_link_status, "Live PCIe Link Status after the Gen2 experiment");

static int __init cmp40_preboot_init(void)
{
    struct pci_dev *pdev = NULL;
    bool unlock, do_gen2, runtime_gen2;
    int ret;

    /* No wildcard selection, implicit writes, detach or background work. */
    if (!target || strlen(target) != 12)
        return -EINVAL;
    if (!mode || (strcmp(mode, "inspect") && strcmp(mode, "unlock") && strcmp(mode, "direct") && strcmp(mode, "sec2_ports") && strcmp(mode, "sec2_vm") && strcmp(mode, "sec2_timeline") && strcmp(mode, "gen2_runtime")))
        return -EINVAL;
    runtime_gen2 = !strcmp(mode, "gen2_runtime");
    unlock = !strcmp(mode, "unlock") || !strcmp(mode, "direct");
    if (!gen2 || (strcmp(gen2, "off") && strcmp(gen2, "prepare") && strcmp(gen2, "retrain")) ||
        !gen2_policy || (strcmp(gen2_policy, "rmw") && strcmp(gen2_policy, "windows")))
        return -EINVAL;
    do_gen2 = strcmp(gen2, "off") != 0;
    if (runtime_gen2 && strcmp(gen2, "retrain"))
        return -EINVAL;
    if (do_gen2 && ((!runtime_gen2 && strcmp(mode, "unlock")) || !stock_handoff || !cpu_rm_handoff ||
                   !small_wpr_heap || locked_control || signed_unload || loader_control ||
                   mmu_invalidate || unload_before_down || unload_full_wpr ||
                   loader_fb_control || loader_virtual_control || loader_native_fbif ||
                   loader_native_addresses || loader_native_physical || loader_boundary))
        return -EINVAL;
    if (locked_control && (strcmp(mode, "unlock") || !stock_handoff || !cpu_rm_handoff))
        return -EINVAL;
    if (signed_unload && (strcmp(mode, "unlock") || !stock_handoff || !cpu_rm_handoff))
        return -EINVAL;
    if (unload_before_down && (!signed_unload || !locked_control || loader_control))
        return -EINVAL;
    if (unload_full_wpr && !unload_before_down)
        return -EINVAL;
    if (!strcmp(mode, "unlock") && small_wpr_heap && (!stock_handoff || !cpu_rm_handoff || signed_unload || loader_boundary))
        return -EINVAL;
    if (loader_control && (signed_unload || strcmp(mode, "unlock") || !stock_handoff || !cpu_rm_handoff))
        return -EINVAL;
    if (loader_fb_control && (!loader_control || !locked_control || signed_unload))
        return -EINVAL;
    if (loader_virtual_control && !loader_fb_control)
        return -EINVAL;
    if (loader_native_fbif && !loader_virtual_control)
        return -EINVAL;
    if (loader_native_addresses && (!loader_virtual_control || loader_native_fbif))
        return -EINVAL;
    if (loader_native_physical && (!loader_fb_control || loader_virtual_control || loader_native_addresses))
        return -EINVAL;
    if (loader_boundary > 3 || (loader_boundary && (!loader_fb_control || loader_virtual_control || loader_native_addresses || loader_native_physical)))
        return -EINVAL;
    if (mmu_invalidate && (strcmp(mode, "unlock") || !stock_handoff || !cpu_rm_handoff))
        return -EINVAL;
    while ((pdev = pci_get_device(PCI_VENDOR_ID_NVIDIA, 0x1f0b, pdev)))
        if (!strcmp(pci_name(pdev), target))
            break;
    if (!pdev)
        return -ENODEV;
    /* PCI locking does not prevent runtime suspend while firmware is read.
     * Resume before taking the PCI lock, because resume callbacks may lock it.
     * Inspection pins only an already active device and never wakes it.
     */
    if (unlock && READ_ONCE(pdev->driver)) {
        ret = -EBUSY;
        goto put_device;
    }
    ret = unlock ? pm_runtime_resume_and_get(&pdev->dev)
                 : pm_runtime_get_if_active(&pdev->dev);
    if (ret < 0 || (!unlock && !ret)) {
        if (!ret)
            ret = -EHOSTDOWN;
        goto put_device;
    }
    pci_dev_lock(pdev);
    if (pdev->current_state != PCI_D0) {
        ret = -EHOSTDOWN;
        goto out;
    }
    if (unlock && pdev->driver) {
        pci_err(pdev, "cmp40_preboot: refusing unlock while driver %s owns the card\n",
                pdev->driver->name);
        ret = -EBUSY;
        goto out;
    }
    if (runtime_gen2) {
        struct cmp40_gen2_result result;

        /* No firmware runs, DMA allocation, detach, PCI enable or FLR here.
         * The external diagnostic helper checks idle proprietary GSP OFF;
         * gen2.c additionally requires halted GSP and full selectors.
         */
        gen2_error = cmp40_gen2_runtime(pdev, !strcmp(gen2_policy, "windows"), &result);
        gen2_device_ok = result.device_ok;
        gen2_link_status = result.link_status;
        pci_info(pdev, "cmp40_preboot: GEN2_RUNTIME_RESULT error=%d device_ok=%u status=%04x\n",
                 gen2_error, gen2_device_ok, gen2_link_status);
        ret = 0;
    } else if (!strcmp(mode, "sec2_timeline")) {
        /* Keep the device/active-power references, but do not hold the PCI
         * device lock across sampling: native initialization must proceed.
         * This mode performs no GPU writes or ownership transitions.
         */
        pci_dev_unlock(pdev);
        ret = cmp40_sec2_timeline(pdev, &timeline_armed);
        goto release_power;
    } else if (!strcmp(mode, "sec2_vm")) {
        ret = cmp40_sec2_vm(pdev);
        if (!ret)
            pci_info(pdev, "cmp40_preboot: SEC2_VM_CAPTURE (window restored; see walk_status)\n");
    } else if (!strcmp(mode, "sec2_ports")) {
        ret = cmp40_sec2_ports(pdev);
        if (!ret)
            pci_info(pdev, "cmp40_preboot: SEC2_PORTS_PASS (index ports restored; memory unchanged)\n");
    } else if (!unlock) {
        ret = cmp40_inspect(pdev);
        if (!ret)
            cmp40_gen2_snapshot(pdev, "inspect");
        if (!ret)
            pci_info(pdev, "cmp40_preboot: INSPECT_PASS (no writes)\n");
    } else if (!strcmp(mode, "direct")) {
        ret = cmp40_direct_selectors(pdev);
        if (!ret)
            pci_info(pdev, "cmp40_preboot: PRE_DRIVER_DIRECT_PASS (driver compatibility untested)\n");
    } else {
        if (do_gen2)
            cmp40_gen2_snapshot(pdev, "before_compute");
        ret = turing_unlock_card(pdev, &resources_retained, cpu_rm_handoff,
                                 stock_handoff, locked_control, signed_unload, loader_control, mmu_invalidate, unload_before_down, loader_fb_control, loader_virtual_control, loader_native_fbif, loader_native_addresses, loader_native_physical, loader_boundary, unload_full_wpr, small_wpr_heap);
        if (!ret)
            pci_info(pdev, "cmp40_preboot: PRE_DRIVER_%s_PASS (verify native driver and workload next)\n",
                     locked_control ? "CONTROL" : "UNLOCK");
        /* turing_unlock_card has already freed DMA and restored device state.
         * Keep link errors out of its FLR cleanup and unlock_error entirely.
         */
        if (!ret && !resources_retained && do_gen2) {
            struct cmp40_gen2_result result;

            gen2_error = cmp40_gen2_prepare(pdev, !strcmp(gen2, "retrain"),
                                           !strcmp(gen2_policy, "windows"), &result);
            gen2_device_ok = result.device_ok;
            gen2_link_status = result.link_status;
            pci_info(pdev, "cmp40_preboot: GEN2_RESULT error=%d device_ok=%u status=%04x\n",
                     gen2_error, gen2_device_ok, gen2_link_status);
        }
    }
out:
    pci_dev_unlock(pdev);
release_power:
    if (!resources_retained)
        pm_runtime_put(&pdev->dev);
    /* Retained DMA requires both the module and the power reference to live. */
put_device:
    pci_dev_put(pdev);
    unlock_error = ret;
    if (resources_retained) {
        /* A failed module_init frees the module even if it holds a reference.
         * Keep this pinned module alive while firmware still owns DMA buffers.
         * This is a recorded failure, never a successful unlock.
         */
        pr_err("cmp40_preboot: RETAINED_ERROR errno=%d; power off before loading a GPU driver\n", ret);
        return 0;
    }
    return ret;
}

static void __exit cmp40_preboot_exit(void)
{
    cmp40_sec2_timeline_release();
}

module_init(cmp40_preboot_init);
module_exit(cmp40_preboot_exit);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("CMP40HX CPU-RM pre-driver unlock and inspection");
MODULE_VERSION("0.16.0");
