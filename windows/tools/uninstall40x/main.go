// 40HX 一键卸载工具 v2.0.1 (CMP 40HX Windows Unlock Uninstaller)
// GUI 无窗口版: 双击不弹黑框, 输出入 %TEMP%\40HX_uninstaller.log, 结束弹消息框
// 移除: 计划任务(含 Gen2 重试任务) / Gen2 Run 键 / 固件启动项 "40HX Unlock"
//
//	/ ESP 解锁 EFI(含 \EFI\Boot\bootx64.efi 回退副本并还原 .bak)
//	/ 驱动服务与文件 / EnableGpuFirmware(GSP 恢复默认关)
//
// v2.6.0: 全部清理实现上提 40hxcore/uninstall_ops.go — 与安装器 GUI 的
// 组件级卸载共用同一实现, 消除两份漂移副本。本文件仅保留编排与交互。
// 电源设置(快速启动/ASPM)不回滚 — 属用户电源偏好, 恢复方法见 README。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"40hxcore"

	"golang.org/x/sys/windows"
)

var silent bool

// tr is a thin shim over the shared hxcore translator so call sites stay short.
func tr(en, ru, zh string) string { return hxcore.T(en, ru, zh) }

// ---- GUI helpers (无 console, 消息框 + 日志) ----

const (
	mbIconInfo  = 0x40
	mbIconError = 0x10
)

var (
	procMsgBoxW       = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	procCreateMutex   = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")
	procShellExecuteW = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")
)

func msgbox(title, text string, icon uint) {
	// -y / -silent(自动化) 时不弹框
	if silent {
		return
	}
	t, _ := syscall.UTF16PtrFromString(title)
	b, _ := syscall.UTF16PtrFromString(text)
	procMsgBoxW.Call(0, uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(t)), uintptr(icon))
}

func setupLog(defName string) {
	p := filepath.Join(os.TempDir(), defName)
	if i := argIndex("-log"); i >= 0 && i+1 < len(os.Args) {
		p = os.Args[i+1]
	}
	if f, err := os.Create(p); err == nil {
		os.Stdout = f
		os.Stderr = f
		fmt.Fprintf(f, "==== 40HX tool %s ====\n", time.Now().Format("2006-01-02 15:04:05"))
	}
}

func lockOnce(name string) func() {
	n, _ := syscall.UTF16PtrFromString(name)
	h, _, e := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(n)))
	if h == 0 {
		return nil
	}
	if e == syscall.ERROR_ALREADY_EXISTS {
		syscall.CloseHandle(syscall.Handle(h))
		return nil
	}
	return func() { syscall.CloseHandle(syscall.Handle(h)) }
}

// selfElevate: 非管理员时 ShellExecute "runas" 重启自身(触发 UAC), 父进程退出
func selfElevate() {
	exe, _ := os.Executable()
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	args := append([]string{}, os.Args[1:]...)
	args = append(args, "-elevated")
	params, _ := syscall.UTF16PtrFromString(hxcore.JoinWindowsArgs(args))
	r, _, _ := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)), 0, 1)
	if r <= 32 {
		msgbox(tr("40HX Uninstaller", "Деинсталлятор 40HX", "40HX 卸载工具"), fmt.Sprintf(tr("Elevation failed (error code %d).\nRight-click this program -> Run as administrator.", "Не удалось повысить права (код ошибки %d).\nЩелкните программу правой кнопкой -> Запуск от имени администратора.", "提权失败(错误码 %d)。\n请右键本程序 -> 以管理员身份运行。"), r), mbIconError)
	}
	os.Exit(0)
}

func main() {
	// Resolve the display language before any output: -lang flag / CMP40HX_LANG
	// env / the value the installer persisted to HKLM\SOFTWARE\40HXUnlock, else
	// English. This is what makes the uninstaller follow the installer's choice.
	hxcore.InitLanguage(os.Args)
	for _, a := range os.Args {
		if a == "-y" || a == "-silent" {
			silent = true
		}
	}
	// GUI 版: 输出镜像到日志
	setupLog("40HX_uninstaller.log")
	fmt.Println("==============================================")
	fmt.Println(tr("  CMP 40HX Windows Unlock Uninstaller "+hxcore.Version, "  Деинсталлятор CMP 40HX Windows Unlock "+hxcore.Version, "  CMP 40HX Windows Unlock 卸载工具 "+hxcore.Version))
	fmt.Println(tr("  Removes: scheduled tasks / unlock boot entry / ESP EFI / Gen2 autostart / drivers", "  Удаляет: задачи планировщика / запись загрузки / ESP EFI / автозапуск Gen2 / драйверы", "  移除: 计划任务 / 解锁启动项 / ESP EFI / Gen2 自启动 / 驱动"))
	fmt.Println("==============================================")
	if !isAdmin() {
		if hasArg("-elevated") {
			// 已提权过一次仍失败(静默提权策略/受限token) -> 禁止再循环, 直接报错
			msgbox(tr("40HX Uninstaller", "Деинсталлятор 40HX", "40HX 卸载工具"), tr("Elevation failed: this account cannot obtain administrator rights.\nRight-click this program -> Run as administrator.", "Не удалось повысить права: у этой учетной записи нет прав администратора.\nЩелкните программу правой кнопкой -> Запуск от имени администратора.", "提权失败：当前账户无法获得管理员权限。\n请右键本程序 -> 以管理员身份运行。"), mbIconError)
			return
		}
		selfElevate()
		return
	}
	if lockOnce(`Local\40HXUninstaller_v1`) == nil {
		msgbox(tr("40HX Uninstaller", "Деинсталлятор 40HX", "40HX 卸载工具"), tr("The uninstaller is already running; please do not click again.", "Деинсталлятор уже запущен, не нажимайте повторно.", "卸载程序已在运行, 请勿重复点击。"), mbIconInfo)
		return
	}

	// 1. 计划任务(旧版开机任务/Gen2 登录任务/重试任务/GSP 修正任务)
	fmt.Print(tr("[1/8] Removing scheduled tasks ... ", "[1/8] Удаление задач планировщика ... ", "[1/8] 删除计划任务 ... "))
	if delTasks() {
		fmt.Println(tr("Done", "Готово", "完成"))
	} else {
		fmt.Println(tr("not found (skipped)", "не найдено (пропущено)", "未找到(跳过)"))
	}

	// 2. Gen2 Run 键
	fmt.Print(tr("[2/8] Removing the Gen2 logon autostart ... ", "[2/8] Удаление автозапуска Gen2 при входе ... ", "[2/8] 删除 Gen2 登录自启动 ... "))
	delRunKey()
	fmt.Println(tr("Done", "Готово", "完成"))

	// 3. 固件启动项
	fmt.Print(tr("[3/8] Removing the firmware boot entry '40HX Unlock' ... ", "[3/8] Удаление записи загрузки прошивки '40HX Unlock' ... ", "[3/8] 删除固件启动项 '40HX Unlock' ... "))
	if delBootEntry() {
		fmt.Println(tr("Done", "Готово", "完成"))
	} else {
		fmt.Println(tr("not found (may already be removed)", "не найдено (возможно, уже удалено)", "未找到(可能已移除)"))
	}

	// 4. ESP 解锁 EFI 文件
	fmt.Print(tr("[4/8] Removing the unlock EFI from the ESP ... ", "[4/8] Удаление разблокировочного EFI с ESP ... ", "[4/8] 删除 ESP 解锁 EFI ... "))
	if delEspEfi() {
		fmt.Println(tr("Done", "Готово", "完成"))
	} else {
		fmt.Println(tr("not found / skipped", "не найдено / пропущено", "未找到/跳过"))
	}

	// 5. 驱动服务
	fmt.Println(tr("[5/8] Stopping and removing driver services...", "[5/8] Остановка и удаление служб драйверов...", "[5/8] 停止并删除驱动服务..."))
	hxcore.UninstallDriverServices()

	// 6. 驱动文件
	fmt.Println(tr("[6/8] Removing driver files...", "[6/8] Удаление файлов драйверов...", "[6/8] 删除驱动文件..."))
	hxcore.UninstallDriverFiles()

	// 6.5 GSP 注册表 (恢复默认关)
	fmt.Print(tr("[6.5/8] Removing EnableGpuFirmware (restoring the GSP default: off) ... ", "[6.5/8] Удаление EnableGpuFirmware (возврат GSP к значению по умолчанию: выкл) ... ", "[6.5/8] 删除 EnableGpuFirmware (恢复 GSP 默认关) ... "))
	if delGspKey() {
		fmt.Println(tr("Done", "Готово", "完成"))
	} else {
		fmt.Println(tr("not found (skipped)", "не найдено (пропущено)", "未找到(跳过)"))
	}

	// 6.6 ProgramData 残留: gen2_status.txt(诊断会当"结果"显示!) + drivers 备份源 + 策略键
	fmt.Print(tr("[6.6/8] Cleaning %ProgramData%\\40HXUnlock + policy key ... ", "[6.6/8] Очистка %ProgramData%\\40HXUnlock + ключа политики ... ", "[6.6/8] 清理 %ProgramData%\\40HXUnlock + 策略键 ... "))
	delProgramData()
	fmt.Println(tr("Done", "Готово", "完成"))

	// 6.7 Defender 排除清理(安装时加的白名单, 卸载需移除不留残留)
	fmt.Print(tr("[6.7/8] Cleaning Defender exclusions ... ", "[6.7/8] Очистка исключений Defender ... ", "[6.7/8] 清理 Defender 排除项 ... "))
	if err := removeDefenderExclusions(); err != nil {
		fmt.Println(tr("not performed (ignorable):", "не выполнено (можно игнорировать):", "未执行(可忽略):"), err)
	} else {
		fmt.Println(tr("Done", "Готово", "完成"))
	}

	// 7. 状态确认
	fmt.Println(tr("[7/8] Checking for leftovers...", "[7/8] Проверка остатков...", "[7/8] 检查残留..."))
	leftover := checkLeftover()

	fmt.Println()
	fmt.Println(tr("Uninstall complete. A reboot is recommended.", "Удаление завершено. Рекомендуется перезагрузка.", "卸载完成。建议重启电脑。"))
	// v2.6.0: 安装时关掉的快速启动/ASPM 属电源偏好, 卸载不回滚
	fmt.Println(tr("  Note: the power settings changed at install time (Fast Startup / PCIe link power saving) are left as-is; see the README to restore them.", "  Примечание: настройки электропитания, измененные при установке (быстрый запуск / энергосбережение канала PCIe), оставлены без изменений; способ восстановления см. в README.", "  注: 安装时调整的电源设置(快速启动/PCIe 链路省电)保留未动, 恢复方法见 README。"))
	if silent {
		return
	}
	icon := uint(mbIconInfo)
	txt := tr("Uninstall complete.\nA reboot is recommended.\n", "Удаление завершено.\nРекомендуется перезагрузка.\n", "卸载完成。\n建议重启电脑。\n") +
		tr("\nNote: the power settings changed at install time (Fast Startup / PCIe link power saving)\nare left as-is — they are power preferences; see README §2.4 to restore them.\n", "\nПримечание: настройки электропитания, измененные при установке (быстрый запуск / энергосбережение канала PCIe),\nоставлены без изменений — это ваши предпочтения питания; способ восстановления см. в README §2.4.\n", "\n注: 安装时调整的电源设置(快速启动/PCIe 链路省电)\n保留未动 — 属电源偏好, 恢复方法见 README §2.4。\n")
	if leftover != "" {
		icon = mbIconError
		txt += tr("\nLeftovers remain:\n", "\nОстались следы:\n", "\n仍有残留:\n") + leftover
	}
	txt += tr("\nDetailed log: ", "\nПодробный журнал: ", "\n详细日志: ") + filepath.Join(os.TempDir(), "40HX_uninstaller.log")
	msgbox(tr("40HX Uninstaller", "Деинсталлятор 40HX", "40HX 卸载工具"), txt, icon)
}

func argIndex(name string) int {
	for i, a := range os.Args {
		if a == name {
			return i
		}
	}
	return -1
}

func hasArg(name string) bool {
	for _, a := range os.Args {
		if a == name {
			return true
		}
	}
	return false
}

func isAdmin() bool {
	t, err := windows.OpenCurrentProcessToken()
	if err == nil {
		defer t.Close()
		var buf [4]byte
		var need uint32
		if err = windows.GetTokenInformation(t, windows.TokenElevation,
			&buf[0], uint32(len(buf)), &need); err == nil && buf[0] != 0 {
			return true
		}
		// TokenElevation 可能因受限环境(沙箱/服务)误报 0, 再试 SCM 全权
	}
	// 兜底: 能以 ALL_ACCESS 打开服务控制管理器 = 真管理员
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ALL_ACCESS)
	if err == nil {
		windows.CloseServiceHandle(scm)
		return true
	}
	return false
}

// ---- v2.6.0: 清理步骤实现已上提 40hxcore/uninstall_ops.go, 以下为编排薄包装 ----

func delTasks() bool        { return len(hxcore.UninstallTasks()) > 0 }
func delRunKey()            { hxcore.UninstallRunKey() }
func delBootEntry() bool    { return hxcore.UninstallBootEntry() }
func delEspEfi() bool       { return hxcore.UninstallEspEfi() }
func delGspKey() bool       { return hxcore.UninstallGspKey() }
func delProgramData()       { hxcore.UninstallProgramData() }
func checkLeftover() string { rem := hxcore.CheckLeftover(); return strings.Join(rem, "\n") }

func removeDefenderExclusions() error { return hxcore.RemoveDefenderExclusions() }
