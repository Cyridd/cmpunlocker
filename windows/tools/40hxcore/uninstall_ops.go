package hxcore

// v2.6.0: 卸载操作上提 40hxcore — GUI 组件级卸载与 40HXUninstaller.exe 共用同一实现,
// 消除两份漂移副本(原 uninstall40x 私有函数)。
// 全部为破坏性操作, 调用方(GUI 卸载页/卸载器)负责确认与提权。
// 电源设置(快速启动/ASPM)属用户偏好, 刻意不提供回滚操作 — 恢复方法见 README。

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"
)

const bootDesc40 = "40HX Unlock"

// UninstallTaskNames: 本工具历史上用过的全部计划任务名(含 40HXGen2Retry 重试任务)
var UninstallTaskNames = []string{"40HXGen2", "40HX PCIe Gen2 Bring-up", "40HXGen2Retry", "40HXGspEnsure"}

// UninstallTasks: 删除计划任务, 返回实际删掉的名字
func UninstallTasks() []string {
	var removed []string
	for _, tn := range UninstallTaskNames {
		out, err := RunOut("schtasks.exe", "/delete", "/tn", tn, "/f")
		if err == nil || strings.Contains(out, "成功") || strings.Contains(strings.ToLower(out), "success") {
			fmt.Printf(T("  Removed scheduled task %s\n", "  Удалена задача планировщика %s\n", "  已删除计划任务 %s\n"), tn)
			removed = append(removed, tn)
		}
	}
	return removed
}

// UninstallRunKey: 删 HKCU Run 值 40HXGen2
func UninstallRunKey() {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err == nil {
		k.DeleteValue("40HXGen2")
		k.Close()
	}
}

// UninstallBootEntry: 删固件启动项 '40HX Unlock', 返回是否删除过
func UninstallBootEntry() bool {
	out, err := RunOut("bcdedit.exe", "/enum", "firmware")
	if err != nil {
		return false
	}
	curGuid := ""
	re := regexp.MustCompile(`\{([0-9a-fA-F-]{36})\}`)
	removed := false
	for _, ln := range strings.Split(out, "\n") {
		if m := re.FindStringSubmatch(ln); len(m) > 1 {
			if strings.Contains(ln, "{") && !strings.Contains(ln, "displayorder") &&
				!strings.Contains(ln, "bootsequence") {
				curGuid = m[1]
			}
		}
		if strings.Contains(ln, bootDesc40) && curGuid != "" {
			RunOut("bcdedit.exe", "/delete", "{"+curGuid+"}", "/f")
			fmt.Printf(T("  Removed boot entry %s\n", "  Удалена запись загрузки %s\n", "  已删除启动项 %s\n"), curGuid)
			removed = true
			curGuid = ""
		}
	}
	return removed
}

// UninstallEspEfi: 删 \EFI\40HX\40HXUNLK.EFI; bootx64.efi 用"覆盖写+校验"从
// .40hx.bak 还原(不用 删→rename — 中间窗口会让机器起不来)。返回是否动过 ESP。
func UninstallEspEfi() bool {
	esp := MountESP()
	if esp == "" {
		return false
	}
	defer RunOut("mountvol.exe", esp+":", "/D")
	removed := false
	target := esp + ":\\EFI\\40HX\\40HXUNLK.EFI"
	if err := os.Remove(target); err == nil {
		removed = true
	}
	if entries, err := os.ReadDir(esp + ":\\EFI\\40HX"); err == nil && len(entries) == 0 {
		os.Remove(esp + ":\\EFI\\40HX")
	}
	// v3.0.0: EFI 运行时写的历史日志一并清除 — 否则卸载后 40HXCheck 会把它
	// 当本次日志分析, 给没装 EFI 的用户派无关引导。
	if err := os.Remove(esp + ":\\40hx_log.txt"); err == nil {
		fmt.Println(T("    Removed the stale EFI log 40hx_log.txt", "    Удален устаревший журнал EFI 40hx_log.txt", "    已删除历史 EFI 日志 40hx_log.txt"))
	}
	std := esp + ":\\EFI\\Boot\\bootx64.efi"
	bak := esp + ":\\EFI\\Boot\\bootx64.efi.40hx.bak"
	if data, berr := os.ReadFile(bak); berr == nil {
		if werr := os.WriteFile(std, data, 0o644); werr != nil {
			fmt.Println(T("  [!] Failed to write the restored bootx64.efi:", "  [!] Не удалось записать восстановленный bootx64.efi:", "  [!] 还原 bootx64.efi 写失败:"), werr)
			fmt.Println(T("      The backup is still at bootx64.efi.40hx.bak and can be restored by hand.", "      Резервная копия по-прежнему лежит в bootx64.efi.40hx.bak — ее можно восстановить вручную.", "      原备份仍保留在 bootx64.efi.40hx.bak, 可手动还原"))
			return removed
		}
		if rb, rerr := os.ReadFile(std); rerr == nil && len(rb) == len(data) {
			os.Remove(bak)
			fmt.Println(T("    Restored the original bootx64.efi from .40hx.bak (verified)", "    Исходный bootx64.efi восстановлен из .40hx.bak (проверка пройдена)", "    已还原原 bootx64.efi (来自 .40hx.bak, 校验 OK)"))
		} else {
			fmt.Println(T("  [!] bootx64.efi did not verify after restore — the .bak is kept for manual recovery", "  [!] bootx64.efi не прошел проверку после восстановления — .bak оставлен для ручного восстановления", "  [!] bootx64.efi 还原后校验不一致 — 保留 .bak 供手动处理"))
		}
		removed = true
	}
	return removed
}

// UninstallDriverServices: 停止并删除历史驱动服务(v2.5 BYOVD + 旧版 bridge/early)
func UninstallDriverServices() {
	for _, name := range []string{"ThrottleStop", "40hx_bridge", "40hx_early", "40hx_early-d", "WinRing0_1_2_0", "WinRing0x64", "WinRing0"} {
		RunOut("sc.exe", "stop", name)
		time.Sleep(300 * time.Millisecond)
		out, err := RunOut("sc.exe", "delete", name)
		switch {
		case err == nil || strings.Contains(strings.ToLower(out), "success") || strings.Contains(out, "成功"):
			fmt.Printf(T("  Service %s removed\n", "  Служба %s удалена\n", "  服务 %s 已删除\n"), name)
		case strings.Contains(out, "不存在") || strings.Contains(strings.ToLower(out), "not") || strings.Contains(out, "1060"):
			fmt.Printf(T("  Service %s does not exist (skipped)\n", "  Служба %s не существует (пропущено)\n", "  服务 %s 不存在(跳过)\n"), name)
		default:
			fmt.Printf(T("  Could not remove service %s: %s\n", "  Не удалось удалить службу %s: %s\n", "  服务 %s 删除失败: %s\n"), name, strings.TrimSpace(out))
		}
	}
}

// UninstallDriverFiles: 删 System32\drivers 下历史 .sys 与 System32\WinRing0x64.dll
func UninstallDriverFiles() {
	for _, name := range []string{"ThrottleStop.sys", "40hx_bridge.sys", "40hx_early-d.sys", "40hx_early.sys", "WinRing0x64.sys"} {
		p := os.Getenv("SystemRoot") + "\\System32\\drivers\\" + name
		if err := os.Remove(p); err != nil {
			if _, statErr := os.Stat(p); statErr == nil {
				fmt.Printf(T("  Could not delete %s (likely in use; it will be removable after a reboot)\n", "  Не удалось удалить %s (вероятно, файл занят; получится после перезагрузки)\n", "  %s 删除失败(可能被占用, 重启后自动可删)\n"), name)
			}
		} else {
			fmt.Printf(T("  Removed %s\n", "  Удалено: %s\n", "  已删除 %s\n"), name)
		}
	}
	os.Remove(os.Getenv("SystemRoot") + "\\System32\\WinRing0x64.dll")
}

// UninstallGspKey: 删 EnableGpuFirmware(恢复 GSP 默认关), 返回是否删除过
func UninstallGspKey() bool {
	key := FindGpuClassKey()
	if key == "" {
		return false
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.SET_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	if err := k.DeleteValue("EnableGpuFirmware"); err != nil {
		return false
	}
	return true
}

// UninstallProgramData: 清 ProgramData\40HXUnlock (gen2_status 历史缓存 + 驱动备份)。
// gen2_status.txt 必须删 — 诊断工具会把它当"上次结果"显示, 残留 ✅ 会误导用户。
func UninstallProgramData() {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	dir := base + "\\40HXUnlock"
	_ = os.Remove(dir + "\\gen2_status.txt")
	_ = os.RemoveAll(dir + "\\drivers")
	if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
		os.Remove(dir)
	}
	// v2.6.0 修复: 策略键一并删除 — 否则卸载后 DriverStrategy/Gen2AutoHard 等
	// 残留, 重装会继承旧策略而非默认(README §2.5 承诺"卸载器会一并删除")。
	DeleteConfig()
	fmt.Println(T("  Removed the policy key HKLM\\SOFTWARE\\40HXUnlock (a reinstall starts from defaults)", "  Удален ключ политики HKLM\\SOFTWARE\\40HXUnlock (переустановка начнется со значений по умолчанию)", "  策略键 HKLM\\SOFTWARE\\40HXUnlock 已删除(重装回到默认策略)"))
}

// CheckLeftover: 卸载收尾的残留清单(供 GUI/卸载器展示)
func CheckLeftover() []string {
	var rem []string
	if out, _ := RunOut("bcdedit.exe", "/enum", "firmware"); strings.Contains(out, bootDesc40) {
		rem = append(rem, T("- Firmware boot entry '40HX Unlock' (remove it by hand in firmware setup)", "- Запись загрузки прошивки '40HX Unlock' (удалите вручную в настройках прошивки)", "- 固件启动项 '40HX Unlock'(BIOS 手动删除)"))
		fmt.Println(T("  [!] Boot entry left over: bcdedit /delete {guid} /f, or remove it from the firmware menu", "  [!] Осталась запись загрузки: bcdedit /delete {guid} /f либо удалите ее в меню прошивки", "  [!] 启动项仍有残留: bcdedit /delete {guid} /f (见 BIOS 菜单)"))
	} else {
		fmt.Println(T("  Boot entry: clean", "  Запись загрузки: очищено", "  启动项: 已清理"))
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE); err == nil {
		if _, _, e := k.GetStringValue("40HXGen2"); e == nil {
			rem = append(rem, T("- Run key 40HXGen2", "- Ключ Run 40HXGen2", "- Run 键 40HXGen2"))
			fmt.Println(T("  [!] Run key left over", "  [!] Остался ключ Run", "  [!] Run 键仍有残留"))
		}
		k.Close()
	}
	taskLeft := false
	for _, tn := range UninstallTaskNames {
		if _, err := RunOut("schtasks.exe", "/query", "/tn", tn); err == nil {
			rem = append(rem, T("- Scheduled task ", "- Задача планировщика ", "- 计划任务 ")+tn)
			fmt.Println(T("  [!] Scheduled task ", "  [!] Задача планировщика ", "  [!] 计划任务 ") + tn + T(" left over", " осталась", " 仍有残留"))
			taskLeft = true
		}
	}
	if !taskLeft {
		fmt.Println(T("  Scheduled tasks: clean", "  Задачи планировщика: очищено", "  计划任务: 已清理"))
	}
	// v3.0.0: 补查驱动服务与 System32 驱动文件 — 常驻策略/文件被占用时
	// 卸载可能只删了服务注册、文件要重启后才能删, 不能假装干净。
	svcNames := []string{"ThrottleStop", "40hx_bridge", "40hx_early", "40hx_early-d", "WinRing0_1_2_0", "WinRing0x64", "WinRing0"}
	svcLeft := false
	for _, sn := range svcNames {
		if _, err := RunOut("sc.exe", "query", sn); err == nil {
			rem = append(rem, T("- Driver service ", "- Служба драйвера ", "- 驱动服务 ")+sn)
			fmt.Println(T("  [!] Driver service ", "  [!] Служба драйвера ", "  [!] 驱动服务 ") + sn + T(" left over (possibly still running; reboot and run the uninstaller again)", " осталась (возможно, еще работает; перезагрузитесь и запустите деинсталлятор снова)", " 仍有残留(可能仍在运行, 重启后重跑卸载器)"))
			svcLeft = true
		}
	}
	if !svcLeft {
		fmt.Println(T("  Driver services: clean", "  Службы драйверов: очищено", "  驱动服务: 已清理"))
	}
	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	fileLeft := false
	for _, fn := range []string{"ThrottleStop.sys", "40hx_bridge.sys", "40hx_early-d.sys", "40hx_early.sys", "WinRing0x64.sys"} {
		if _, err := os.Stat(sysRoot + "\\System32\\drivers\\" + fn); err == nil {
			rem = append(rem, T("- Driver file ", "- Файл драйвера ", "- 驱动文件 ") + fn)
			fmt.Println(T("  [!] Driver file ", "  [!] Файл драйвера ", "  [!] 驱动文件 ") + fn + T(" left over (possibly in use; reboot and run the uninstaller again)", " остался (возможно, занят; перезагрузитесь и запустите деинсталлятор снова)", " 仍有残留(可能被占用, 重启后重跑卸载器)"))
			fileLeft = true
		}
	}
	if !fileLeft {
		fmt.Println(T("  Driver files: clean", "  Файлы драйверов: очищено", "  驱动文件: 已清理"))
	}
	if esp := MountESP(); esp != "" {
		if _, err := os.Stat(esp + ":\\EFI\\40HX\\40HXUNLK.EFI"); err == nil {
			rem = append(rem, T("- Unlock EFI file on the ESP", "- Файл разблокировочного EFI на ESP", "- ESP 解锁 EFI 文件"))
			fmt.Println(T("  [!] Unlock EFI left over on the ESP", "  [!] На ESP остался разблокировочный EFI", "  [!] ESP 解锁 EFI 仍有残留"))
		} else {
			fmt.Println(T("  Unlock EFI on the ESP: clean", "  Разблокировочный EFI на ESP: очищено", "  ESP 解锁 EFI: 已清理"))
		}
		if _, err := os.Stat(esp + ":\\EFI\\Boot\\bootx64.efi.40hx.bak"); err == nil {
			rem = append(rem, T("- bootx64.efi.40hx.bak backup was not restored", "- Резервная копия bootx64.efi.40hx.bak не восстановлена", "- bootx64.efi.40hx.bak 备份未还原"))
			fmt.Println(T("  [!] The bootx64.efi.40hx.bak backup is still present", "  [!] Резервная копия bootx64.efi.40hx.bak все еще на месте", "  [!] bootx64.efi.40hx.bak 备份仍存在"))
		}
		if _, err := os.Stat(esp + ":\\40hx_log.txt"); err == nil {
			rem = append(rem, T("- 40hx_log.txt in the ESP root (stale EFI log)", "- 40hx_log.txt в корне ESP (устаревший журнал EFI)", "- ESP 根 40hx_log.txt (历史 EFI 日志)"))
			fmt.Println(T("  [!] The stale 40hx_log.txt is still there (run the uninstaller once more to clear it)", "  [!] Устаревший 40hx_log.txt все еще на месте (запустите деинсталлятор еще раз, чтобы его удалить)", "  [!] 40hx_log.txt 历史日志仍存在(再跑一次卸载器即清除)"))
		}
		RunOut("mountvol.exe", esp+":", "/D")
	}
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	pdDir := base + "\\40HXUnlock"
	if _, err := os.Stat(pdDir + "\\gen2_status.txt"); err == nil {
		rem = append(rem, T("- ProgramData\\40HXUnlock\\gen2_status.txt (diagnostic cache)", "- ProgramData\\40HXUnlock\\gen2_status.txt (кэш диагностики)", "- ProgramData\\40HXUnlock\\gen2_status.txt (诊断缓存)"))
		fmt.Println(T("  [!] gen2_status.txt left over", "  [!] Остался gen2_status.txt", "  [!] gen2_status.txt 仍有残留"))
	}
	if _, err := os.Stat(pdDir + "\\drivers"); err == nil {
		rem = append(rem, T("- ProgramData\\40HXUnlock\\drivers (driver backup)", "- ProgramData\\40HXUnlock\\drivers (резервная копия драйверов)", "- ProgramData\\40HXUnlock\\drivers (驱动备份)"))
		fmt.Println(T("  [!] The drivers backup is left over", "  [!] Осталась резервная копия drivers", "  [!] drivers 备份仍有残留"))
	}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, ConfigKeyPath, registry.QUERY_VALUE); err == nil {
		k.Close()
		rem = append(rem, T("- Policy key HKLM\\SOFTWARE\\40HXUnlock", "- Ключ политики HKLM\\SOFTWARE\\40HXUnlock", "- HKLM\\SOFTWARE\\40HXUnlock 策略键"))
		fmt.Println(T("  [!] The policy key HKLM\\SOFTWARE\\40HXUnlock is left over", "  [!] Остался ключ политики HKLM\\SOFTWARE\\40HXUnlock", "  [!] 策略配置键 HKLM\\SOFTWARE\\40HXUnlock 仍有残留"))
	}
	return rem
}
