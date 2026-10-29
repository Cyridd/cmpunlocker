package main

// gen2_pl0_v2.go — патч Windows-части Gen2 форка до паритета с 3.2.0.
//
// Таблица PL0 ниже снята дизассемблированием main.gen2Main / main.gen2WritePL0
// из релиза 3.2.0 (sha256 568612ee...b7d4). Структура записи в 3.2.0:
//     { off u64, val u32, name string, rmw bool, mask u32 }
//     rmw:  new = (old &^ mask) | val      проверка: (readback & mask) == (new & mask)
//     !rmw: new = val                       проверка: readback == val

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"40hxcore"
)

const (
	regLtssmOvr    = 0x8872C
	regLinkConfig0 = 0x8C040
	regPlLinkRate  = 0x8C1C0 // NEW
	regPrivMisc1   = 0x8841C
	regCya0        = 0x8C2C0
)

type pl0Entry struct {
	off  uint64
	name string
	val  uint32
	rmw  bool
	mask uint32
}

// Ровно как в 3.2.0 (порядок записей тоже).
var pl0V320 = []pl0Entry{
	{regLtssmOvr, "XVE_OVR=6", 0x00000006, false, 0},
	{regLinkConfig0, "LINK_CONFIG_0(MAX_RATE=2)", 0x00080000, true, 0x000C0000},
	{regPlLinkRate, "PL_LINK_RATE([18:17]=2)", 0x00040000, true, 0x00060000},
	{regPrivMisc1, "PRIV_MISC_1", 0xE0B42D00, false, 0},
	{regCya0, "CYA_0", 0x068731B3, false, 0},
}

// Что делал форк: константа целиком, PL_LINK_RATE не трогал.
var pl0Fork = map[uint64]uint32{
	regLtssmOvr:    0x00000006,
	regLinkConfig0: 0x80085800,
	regPrivMisc1:   0xE0B42D00,
	regCya0:        0x068731B3,
}

func (e pl0Entry) next(old uint32) uint32 {
	if e.rmw {
		return (old &^ e.mask) | e.val
	}
	return e.val
}

// Опция: для PRIV_MISC_1 / CYA_0 использовать RMW как в EFI-приложении вместо
// констант 3.2.0. Констант 3.2.0 на машине репортёра работает без BSOD, поэтому
// по умолчанию выключено; включать только для A/B (reg Gen2Pl0EfiRmw=1).
func (e pl0Entry) nextEfi(old uint32) uint32 {
	switch e.off {
	case regPrivMisc1:
		return (old | 1<<11 | 1<<13) &^ (1<<12 | 1<<14)
	case regCya0:
		return old &^ (1 << 2)
	}
	return e.next(old)
}

func gen2BAR0Ok(th syscall.Handle, bar0Phys uint64) bool {
	if bar0Phys < 0x100000 {
		fmt.Printf("[Gen2][!] BAR0=0x%X implausible, refusing MMIO access\n", bar0Phys)
		return false
	}
	boot0, err := hxcore.TSRead(th, bar0Phys)
	if err != nil || boot0&0xFF000000 != 0x16000000 {
		fmt.Printf("[Gen2][!] BOOT_0 check failed at BAR0=0x%X: 0x%08X err=%v\n", bar0Phys, boot0, err)
		return false
	}
	return true
}

// gen2WritePL0V2 — замена gen2WritePL0 и inline-таблицы. false = ничего не писали.
// BOOT_0 проверяется при КАЖДОМ вызове (в т.ч. после Link Disable/PnP, где BAR0
// перечитывается; в 3.2.0 и в форке такой проверки там нет).
func gen2WritePL0V2(th syscall.Handle, bar0Phys uint64) bool {
	if !gen2BAR0Ok(th, bar0Phys) {
		return false
	}
	efi := hxcore.ConfigInt("Gen2Pl0EfiRmw", 0) != 0
	skipOvr := hxcore.ConfigInt("Gen2WriteLtssmOvr", 1) == 0
	skipRate := hxcore.ConfigInt("Gen2WritePlLinkRate", 1) == 0
	for _, e := range pl0V320 {
		if (skipOvr && e.off == regLtssmOvr) || (skipRate && e.off == regPlLinkRate) {
			fmt.Printf("  %s skipped by policy\n", e.name)
			continue
		}
		addr := bar0Phys + e.off
		old, err := hxcore.TSRead(th, addr)
		if err != nil {
			fmt.Printf("  [!] %s read failed: %v (skip)\n", e.name, err)
			continue
		}
		want := e.next(old)
		if efi {
			want = e.nextEfi(old)
		}
		if err := hxcore.TSWrite(th, addr, want); err != nil {
			fmt.Printf("  [!] %s write failed: %v\n", e.name, err)
			continue
		}
		rb, rerr := hxcore.TSRead(th, addr)
		cmpMask := uint32(0xFFFFFFFF)
		if e.rmw && !efi {
			cmpMask = e.mask
		}
		if rerr != nil || rb&cmpMask != want&cmpMask {
			fmt.Printf("  [warn] %s 0x%08X -> want 0x%08X, read back 0x%08X\n", e.name, old, want, rb)
		} else {
			fmt.Printf("  %s OK 0x%08X -> 0x%08X (rb 0x%08X)\n", e.name, old, want, rb)
		}
	}
	return true
}

// gen2PnpFallbackV2 — реконструкция main.gen2PnpFallbackRetry из 3.2.0 по
// последовательности вызовов (LinkSpeed -> PnpRecover -> waitForNvDriver ->
// ReopenDrivers -> FindGPUPCI -> BAR0 -> PL0 -> SetTLS x2 -> retrain-цикл ->
// RestoreGPULnkctl -> RestartNVDisplay). Тело — НЕ побайтовая копия, а сборка из
// существующих функций. Возвращает итоговую скорость.
func gen2PnpFallbackV2(th, wh *syscall.Handle, gpuBDF *uint32, root uint32) uint32 {
	fmt.Println("[Gen2] Stage1 failed -> PnP fallback: disable/enable 40HX, reload nvlddmkm, redo PL0+TLS+retrain")
	if !gen2PnpRecover40HX() {
		fmt.Println("[Gen2][!] PnP recovery failed")
	}
	waitForNvDriver(60 * time.Second)
	if !gen2ReopenDrivers(th, wh) {
		fmt.Println("[Gen2][!] driver reopen failed; aborting fallback")
		return hxcore.LinkSpeed(*wh, *gpuBDF)
	}
	if b, ok := hxcore.FindGPUPCI(*wh); ok {
		*gpuBDF = b
	}
	raw, _ := hxcore.PciRd(*wh, *gpuBDF, 0x10)
	bar0 := uint64(raw & 0xFFFFFFF0)
	if raw == 0 || raw == 0xFFFFFFFF || !gen2WritePL0V2(*th, bar0) {
		fmt.Println("[Gen2][!] BAR0 invalid after PnP reload; no MMIO writes")
		return hxcore.LinkSpeed(*wh, *gpuBDF)
	}
	gen2SetTLS(*wh, root, 2)
	gen2SetTLS(*wh, *gpuBDF, 2)
	cur := hxcore.LinkSpeed(*wh, *gpuBDF)
	for i := 0; i < 6 && cur < 2; i++ {
		bdf := *gpuBDF
		if root != 0xFFFFFFFF && i%2 == 0 {
			bdf = root
		}
		gen2RetrainPulse(*wh, bdf)
		time.Sleep(2200 * time.Millisecond)
		cur = hxcore.LinkSpeed(*wh, *gpuBDF)
	}
	gen2RestoreGPULnkctl(*wh, *gpuBDF)
	gen2RestartNVDisplay()
	return hxcore.LinkSpeed(*wh, *gpuBDF)
}

// gen2DryRunMain: "-gen2dry". Только чтение (MMIO + PCI config). Печатает, какие
// биты изменила бы каждая из трёх схем: fork / 3.2.0 / EFI-RMW.
func gen2DryRunMain() {
	owned, release := gen2AcquireSingleInstance()
	if !owned {
		fmt.Println("[dry] another Gen2 instance is running")
		return
	}
	defer release()
	defer cleanupByovd()

	sysDir := os.Getenv("SystemRoot") + "\\System32\\drivers"
	for _, df := range []string{"ThrottleStop.sys", "WinRing0x64.sys"} {
		if _, err := os.Stat(filepath.Join(sysDir, df)); err != nil {
			copyEmbedTo(filepath.Join(sysDir, df), df)
		}
	}
	ensureSvcLoaded("ThrottleStop", "ThrottleStop.sys")
	ensureSvcLoaded("WinRing0_1_2_0", "WinRing0x64.sys")

	th, err := hxcore.OpenThrottleStop()
	if err != nil {
		fmt.Println("[dry] ThrottleStop not available:", err)
		return
	}
	wh, err := hxcore.OpenDevice(`\\.\WinRing0_1_2_0`)
	if err != nil {
		hxcore.CloseHandle(th)
		fmt.Println("[dry] WinRing0 not available:", err)
		return
	}
	defer func() { hxcore.CloseHandle(th); hxcore.CloseHandle(wh) }()

	bdf, ok := hxcore.FindGPUPCI(wh)
	if !ok {
		fmt.Println("[dry] 40HX not found")
		return
	}
	raw, _ := hxcore.PciRd(wh, bdf, 0x10)
	fmt.Printf("[dry] GPU bdf=0x%04X BAR0 raw=0x%08X (low nibble 0x%X; 0x4/0xC would mean 64-bit BAR)\n", bdf, raw, raw&0xF)
	bar0 := uint64(raw & 0xFFFFFFF0)
	if !gen2BAR0Ok(th, bar0) {
		return
	}

	fmt.Println("[dry] ---- PL0 registers (READ ONLY) ----")
	fmt.Println("[dry] name                       off      current     | v3.2.0 flips | fork flips  | EFI-RMW flips")
	for _, e := range pl0V320 {
		cur, e1 := hxcore.TSRead(th, bar0+e.off)
		if e1 != nil {
			fmt.Printf("[dry] %-26s 0x%05X  read error: %v\n", e.name, e.off, e1)
			continue
		}
		n320 := e.next(cur)
		nEfi := e.nextEfi(cur)
		forkCol := "     n/a   "
		if fv, has := pl0Fork[e.off]; has {
			forkCol = fmt.Sprintf("0x%08X", cur^fv)
		}
		fmt.Printf("[dry] %-26s 0x%05X  0x%08X  | 0x%08X   | %s  | 0x%08X\n",
			e.name, e.off, cur, cur^n320, forkCol, cur^nEfi)
	}
	fmt.Println("[dry] (flips = cur XOR value-that-would-be-written; 0x00000000 = no change)")
	fmt.Println("[dry] ---- context ----")
	for _, x := range []struct {
		off  uint64
		name string
	}{
		{0x88084, "XVE LINK_CAP"}, {0x88088, "XVE LINK_CTRL_STATUS"},
		{0x880A4, "XVE LINK_CAP2"}, {0x880A8, "XVE LINK_CTRL_2"},
		{0x8E110, "XP3G_OVR0"}, {0x8E11C, "XP3G_OVR3"},
		{0x8E120, "XP3G_VAL0"}, {0x8E12C, "XP3G_VAL3"},
		{0x88BC0, "ReBAR BAR1 CTL"}, {0x823804, "FEAT_OVR_PLM"},
		{0x82381C, "SS0"}, {0x823820, "SS1"},
	} {
		v, e1 := hxcore.TSRead(th, bar0+x.off)
		fmt.Printf("[dry] %-26s 0x%05X  0x%08X err=%v\n", x.name, x.off, v, e1)
	}
	root := hxcore.FindRootPort(wh, (bdf>>8)&0xFF)
	fmt.Printf("[dry] root port bdf=0x%04X (0xFFFFFFFF = not on bus 0)\n", root)
	if root != 0xFFFFFFFF {
		if c := hxcore.PcieCap(wh, root); c != 0 {
			a, _ := hxcore.PciRd(wh, root, c+0x0C)
			b, _ := hxcore.PciRd(wh, root, c+0x10)
			d, _ := hxcore.PciRd(wh, root, c+0x30)
			fmt.Printf("[dry] root LNKCAP=0x%08X LNKCTL/STA=0x%08X LNKCTL2/STA2=0x%08X\n", a, b, d)
		}
	}
	fmt.Println("[dry] done. Nothing was written.")
}

// logSyncLoop: fsync каждые 100 мс, чтобы последние строки до BSOD дожили до диска.
func logSyncLoop(f *os.File) {
	for {
		time.Sleep(100 * time.Millisecond)
		_ = f.Sync()
	}
}
