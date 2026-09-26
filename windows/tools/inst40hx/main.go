// 40HX 一键安装工具 v2.0.1 (CMP 40HX Windows Unlock Installer)
// 功能:
//
//	(默认) 安装: GSP 启用(EnableGpuFirmware=1) + ESP 双路部署 40HXUNLK.EFI (V70)
//	      + BootOrder 置顶 + 驱动 + Gen2 自启动
//	-gen2        立即执行 Gen2 解锁(供登录自启动调用, 幂等)
//	-uninstall   卸载(移除启动项/Run键/驱动服务/EnableGpuFirmware)
//	-status      状态检查
//
// 资源 embed (v2.5): 40HXUNLK.EFI (V70 解锁版) / ThrottleStop.sys / WinRing0x64.sys
// v2.6.0 关键修复(社区 #2/#5/#6/#7 + v2.4.5 时代排障结论):
//  1. EFI 部署失败不再中止安装 — Legacy/MBR(无 ESP)只跳过 EFI 两步, Gen2 任务
//     照常注册(此前 [5/8] 直接 return, 是"装了驱动开机却不跑 Gen2"的统一根因)
//  2. 引导模式检测(GetFirmwareType): Legacy → 弹窗给 mbr2gpt 无损转换完整指引
//  3. 计划任务创建后 schtasks query 二次校验 + 重试; -task 失败以非零码退出
//     (命令行调用时的 errorlevel 检查从死代码变为有效)
//  4. 自动关闭快速启动(混合休眠)与 PCIe 链路省电(ASPM) — 前者避免"关机再开
//     不走完整 UEFI 引导", 后者减少空闲降到 Gen1 被误读为解锁失败
//  5. Gen2 核心增强: LNKCTL2 读改写(不清高位) + root/GPU 交替重训最多 4 轮 +
//     以 TLS 目标速率判成败(空闲省电降速 Gen1 不再误报失败)
//
// v2.6.0 关键加固(自启动通道设计与并发安全, 回应"多自启动路径怕出问题"):
//  1. Gen2 单实例内核互斥体(Global\40HXGen2SingleInstance): SYSTEM 任务 / Run 键 /
//     手动 -gen2 即使并发触发, 也仅一个进程进入"加载-卸载 BYOVD 驱动 + 抢 BAR0"
//     临界区, 杜绝双进程争用驱动服务名与链路寄存器导致的状态错乱
//  2. 自启动通道收敛为"两路互斥串行": Run 键登录瞬间先试(可能 GPU 未就绪而失败,
//     静默交权), SYSTEM 任务延迟 30s 再确认; 其余 13 类路径(HKCU/HKLM Run 之外)
//     均运行于用户态、无法 sc start 内核驱动, 故不采用(详见设计文档)
//  3. 定位 40HX 失败重试最多 3 次(间隔 2s), 容忍慢速 GPU 初始化导致的假失败
//
// v2.4 关键变更(社区兼容):
//  1. embed EFI 回到 V70 原版 (793d765e, 用户实测解锁成功) — v2.1/v2.2 精简版失败教训
//  2. ESP 双路部署: \EFI\40HX\40HXUNLK.EFI (BCD 主路径)
//     + \EFI\Boot\bootx64.efi (UEFI 标准 fallback, 原文件备份 .40hx.bak)
//     解决部分主板不认非标准 EFI 路径/忽略 BCD displayorder 导致"装完重启没反应"
//  3. BootOrder 写入后从固件读回验证, 不在首位时明确弹窗提示 BIOS 手动置顶
//  4. 关键 BIOS 操作全部进消息框 (社区用户不看 README/日志)
//
// v2.3 关键: EnableGpuFirmware=1 启用 GSP — 40HX 默认 GSP 关(CPU-RM 模式)时,
//
//	EFI 解锁后 nvlddmkm 拒绝 SEC2 状态 -> Code43 黑屏; GSP-RM 模式能接受解锁.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"40hxcore"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

//go:embed embed/*
var embedded embed.FS

const (
	gpuVenDev = "VEN_10DE&DEV_1F0B"
	efiDir    = "\\EFI\\40HX"
	efiFile   = "40HXUNLK.EFI"
	bootDesc  = "40HX Unlock"
	// v2.4: UEFI 标准回退路径 (固件 BootOrder 全部无效/未签名时自动尝试此路径;
	// 解决部分主板忽略 BCD displayorder / 不认非标准 \EFI\40HX 目录)
	efiStdDir = "\\EFI\\Boot"
	efiStdF   = "bootx64.efi"
	efiBakExt = ".40hx.bak" // bootx64.efi.40hx.bak 原文件备份
	// v2.3: GSP 启用注册表 (EnableGpuFirmware=1) — 解锁不黑屏的关键!
	// 40HX 的显示适配器 Class 子键 (0001 = 40HX; 多卡时需按 AdapterString 找)
	gpuClassPath  = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`
	gpuClassGUID  = `{4d36e968-e325-11ce-bfc1-08002be10318}` // Driver 值反查用
	gpuEnableFw   = "EnableGpuFirmware"
	gpuAdapterStr = "HardwareInformation.AdapterString"
	gpuAdapter40  = "CMP 40HX"
	// v2.4.6: Gen2 的 SYSTEM 计划任务名(卸载时按名字删除)
	gen2TaskName = "40HX PCIe Gen2 Bring-up"
	// v2.6.0: Gen2 失败后的自动重试任务(一次性, 成功即删, 卸载链按名清理)
	gen2RetryTask = "40HXGen2Retry"
	// v3.1: Secure Boot 共存 — 用户用自己的 db 密钥签名后的 EFI 从哪来。
	// efiPayloadFlag 是显式指定; signedSidecarName 是放在安装器旁边即可自动识别的
	// 文件名(与 windows/tools/unlock40x/sign_efi.sh 的默认输出同名)。
	efiPayloadFlag    = "-efi"
	signedSidecarName = "40HXUNLK.signed.efi"
)

func main() {
	initLanguage()
	// GUI 无窗口版(v1.1): 输出全部镜像到日志(默认 %TEMP%\40HX_installer.log, 可 -log 指定)
	setupLog("40HX_installer.log")
	// -efi 的路径要在任何提权/转发之前定死为绝对路径(见 normalizeEFIArg)。
	normalizeEFIArg()
	// v2.6.0: 双击(无参数)或 UAC 提权重启(-elevated)默认进入 GUI 管理界面;
	// 命令行参数(-gen2/-task/-uninstall/-status/-silent/-hard)语义保持不变。
	if len(os.Args) <= 1 || (len(os.Args) == 2 && os.Args[1] == "-elevated") {
		runGUI()
		return
	}
	// install/-uninstall 需管理员: 非提升时自动 ShellExecute runas 弹 UAC 重启
	needAdmin := true
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-gen2", "-gspensure", "-status", "-h", "-help", "--help":
			needAdmin = false
		}
		// -task 需管理员(GUI 双击自动 UAC; gen2/status 等只读或 SYSTEM 任务调用无需)
		if os.Args[1] == "-task" {
			needAdmin = true
		}
	}
	if needAdmin && !isAdmin() {
		if hasArg("-elevated") {
			// 已提权过一次仍失败(如静默提权策略下受限token) -> 禁止再循环, 直接报错
			msgbox(tr("40HX Installer", "Установщик 40HX", "40HX 安装器"), tr("Elevation failed: this account cannot obtain administrator rights.\nRight-click this program -> Run as administrator.", "Не удалось повысить права: у этой учётной записи нет прав администратора.\nЩёлкните программу правой кнопкой -> Запуск от имени администратора.", "提权失败：当前账户无法获得管理员权限。\n请右键本程序 -> 以管理员身份运行。"), mbIconError)
			return
		}
		selfElevate()
		return
	}
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-gen2":
			gen2Main()
			// v3.0.1: 常驻守护 — 由登录任务带 -guard 启动; 驱动保留并每分钟自查 Gen2
			if hasArg("-guard") && hxcore.DriverStrategy() == hxcore.DriverStrategyResident {
				residentGuard()
			}
			return
		case "-gspensure":
			gspEnsureMain()
			return
		case "-uninstall":
			uninstall()
			return
		case "-status":
			status()
			return
		case "-task":
			// 仅注册 Gen2 登录自启任务(供 -task 模式调用;
			// 由 Go 构造 /TR 引号, 避免 bat 内嵌引号解析出错/闪退)
			regTaskOnly()
			return
		case "-rebar":
			// ReBAR 令牌开关(需管理员写 bcdedit loadoptions)。清除 norebar = 开。
			applyRebarLoadOption(true)
			return
		case "-norebar":
			applyRebarLoadOption(false)
			return
		case "-h", "-help", "--help":
			printHelp()
			return
		}
	}
	install()
}

// regTaskOnly: 只注册 Gen2 SYSTEM 任务(不安装驱动/EFI/GSP)。
// -task 模式的最后一步调用本模式 — Go 处理引号。
// v2.6.0: 失败以非零码退出 — bat 的 errorlevel 检查依赖它(此前恒为 0, 检查是死代码)。
func regTaskOnly() {
	if !isAdmin() {
		fmt.Println(tr("[!] Registering the scheduled task requires administrator rights.", "[!] Для регистрации задачи планировщика нужны права администратора.", "[!] 注册计划任务需要管理员权限。"))
		msgbox(tr("40HX Installer", "Установщик 40HX", "40HX 安装器"), tr("Registering the scheduled task requires administrator rights.\nRun as administrator.", "Для регистрации задачи планировщика нужны права администратора.\nЗапустите от имени администратора.", "注册计划任务需要管理员权限。\n请以管理员身份运行。"), mbIconError)
		os.Exit(1)
	}
	if err := setupGen2Task(); err != nil {
		fmt.Println("[!]", err)
		msgbox(tr("40HX Installer", "Установщик 40HX", "40HX 安装器"), tr("Gen2 logon-autostart task registration failed:\n", "Не удалось зарегистрировать задачу автозапуска Gen2 при входе:\n", "Gen2 登录自启任务注册失败:\n")+err.Error()+
			tr("\n\nPlease confirm you are running as administrator and retry.", "\n\nУбедитесь, что запускаете от имени администратора, и повторите.", "\n\n请确认以管理员身份运行后重试。"), mbIconError)
		os.Exit(1)
	}
	// v2.6.0: Run 键 = 登录瞬间先试一次(可能 GPU 未就绪而失败, 静默交权);
	// SYSTEM 任务延迟 30s 再确认。二者由 gen2Main 的单实例互斥体串行化, 不会并发抢驱动。
	setRunKey()
	msgbox(tr("40HX Installer", "Установщик 40HX", "40HX 安装器"), tr("The Gen2 logon-autostart task is registered.\nGen2 unlock runs automatically after logon (silent, unloaded when done).", "Задача автозапуска Gen2 при входе зарегистрирована.\nРазблокировка Gen2 выполняется автоматически после входа (тихо, выгружается по завершении).", "Gen2 登录自启任务已注册。\n登录后会自动执行 Gen2 解锁(静默, 用完即卸)。"), mbIconInfo)
}

// selfElevate: 非管理员时 ShellExecute "runas" 重启自身(触发 UAC), 父进程退出
// GUI 子系统下无黑窗; 提权失败以消息框提示
func selfElevate() {
	exe, _ := os.Executable()
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	// 追加 -elevated 标记: 新实例若仍非管理员则禁止再次提权(防无限循环)
	args := append([]string{}, os.Args[1:]...)
	args = append(args, "-elevated")
	params, _ := syscall.UTF16PtrFromString(hxcore.JoinWindowsArgs(args))
	r, _, _ := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)), 0, 1)
	if r <= 32 {
		msgbox(tr("40HX Installer", "Установщик 40HX", "40HX 安装器"), fmt.Sprintf(tr("Elevation failed (error code %d).\nRight-click this program -> Run as administrator.", "Не удалось повысить права (код ошибки %d).\nЩёлкните программу правой кнопкой -> Запуск от имени администратора.", "提权失败(错误码 %d)。\n请右键本程序 -> 以管理员身份运行。"), r), mbIconError)
	}
	os.Exit(0)
}

var (
	procShellExecuteW = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")
)

const (
	mbIconInfo  = 0x40
	mbIconError = 0x10
	mbIconWarn  = 0x30 // MB_ICONWARNING — v2.6.0: EFI 跳过/部分成功等"可继续但要注意"场景
	mbYesNo     = 0x04 // MB_YESNO → 返回 IDYES=6 / IDNO=7
)

var (
	procMsgBoxW     = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	procCreateMutex = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")
)

func msgbox(title, text string, icon uint) {
	// -y / -silent(自动化/自启动) 时不弹框
	if hasArg("-y") || hasArg("-silent") {
		return
	}
	t, _ := syscall.UTF16PtrFromString(title)
	b, _ := syscall.UTF16PtrFromString(text)
	procMsgBoxW.Call(0, uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(t)), uintptr(icon))
}

// msgboxYesNo: 是/否询问。自动模式: -y→true(全自动继续), -silent→false(不打扰)。
func msgboxYesNo(title, text string) bool {
	if hasArg("-y") {
		return true
	}
	if hasArg("-silent") {
		return false
	}
	t, _ := syscall.UTF16PtrFromString(title)
	b, _ := syscall.UTF16PtrFromString(text)
	r, _, _ := procMsgBoxW.Call(0, uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(t)), uintptr(mbYesNo|mbIconInfo))
	return r == 6 // IDYES
}

// setupLog: 输出镜像到日志文件(默认 %TEMP%/<name>, 命令行 -log <file> 优先)
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

// AttachLogSink: v2.6.0 GUI 用 — 用 os.Pipe 把后续 fmt.* 输出分流到 日志文件+UI。
// fmt.* 每次调用读 os.Stdout 变量; 但 os.Stdout 本身是 *os.File 具体类型,
// 不能赋 io.Writer, 故替换为管道写端, 由读协程同时写原文件与 GUI 日志面板。
//
// Reads are line-buffered. A fixed-size raw read used to cut multi-byte UTF-8
// characters in half whenever a message straddled the buffer boundary, which
// showed up as replacement characters in the log panel for Russian and Chinese.
func AttachLogSink(w io.Writer) {
	r, pw, err := os.Pipe()
	if err != nil {
		return
	}
	orig := os.Stdout // setupLog 建立的日志文件(或 GUI 下的无效控制台句柄)
	os.Stdout = pw
	os.Stderr = pw
	go func() {
		defer r.Close()
		br := bufio.NewReader(r)
		for {
			line, rerr := br.ReadString('\n')
			if len(line) > 0 {
				b := []byte(line)
				orig.Write(b) // 落日志文件(GUI 模式下失败可忽略)
				w.Write(b)    // 喂 GUI 日志面板
			}
			if rerr != nil {
				return
			}
		}
	}()
}

// lockOnce: 单实例互斥; 返回 nil 表示已有实例在跑
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

func hasArg(name string) bool {
	for _, a := range os.Args {
		if a == name {
			return true
		}
	}
	return false
}

func argIndex(name string) int {
	for i, a := range os.Args {
		if a == name {
			return i
		}
	}
	return -1
}

func printHelp() {
	fmt.Println(tr("CMP 40HX Windows Unlock Installer", "Установщик разблокировки CMP 40HX для Windows", "CMP 40HX Windows 解锁一键安装工具"))
	fmt.Println(tr("  Usage: 40HXInstaller.exe            # install (administrator required)", "  Использование: 40HXInstaller.exe            # установка (нужны права администратора)", "  用法: 40HXInstaller.exe            # 安装(需管理员)"))
	fmt.Println(tr("          40HXInstaller.exe -gen2      # run Gen2 unlock now", "          40HXInstaller.exe -gen2      # запустить разблокировку Gen2", "       40HXInstaller.exe -gen2      # 立即执行 Gen2 解锁"))
	fmt.Println(tr("          40HXInstaller.exe -uninstall # uninstall", "          40HXInstaller.exe -uninstall # удалить", "       40HXInstaller.exe -uninstall # 卸载"))
	fmt.Println(tr("          40HXInstaller.exe -status    # show status", "          40HXInstaller.exe -status    # показать состояние", "       40HXInstaller.exe -status    # 状态"))
	fmt.Println(tr("          40HXInstaller.exe -lang en|ru|zh # select language", "          40HXInstaller.exe -lang en|ru|zh # выбрать язык", "       40HXInstaller.exe -lang en|ru|zh # 选择语言"))
	fmt.Println(tr("          40HXInstaller.exe -rebar      # enable ReBAR (8 GB BAR1) on the boot entry", "          40HXInstaller.exe -rebar      # включить ReBAR (BAR1 8 ГБ) в записи загрузки", "       40HXInstaller.exe -rebar      # 在启动项启用 ReBAR (8 GB BAR1)"))
	fmt.Println(tr("          40HXInstaller.exe -norebar    # disable ReBAR (skip the BAR1 resize)", "          40HXInstaller.exe -norebar    # отключить ReBAR (пропустить увеличение BAR1)", "       40HXInstaller.exe -norebar    # 关闭 ReBAR (跳过 BAR1 放大)"))
	fmt.Println(tr("          40HXInstaller.exe -efi <path> # deploy your own signed 40HXUNLK.EFI (Secure Boot; see README)", "          40HXInstaller.exe -efi <путь> # установить собственный подписанный 40HXUNLK.EFI (Secure Boot; см. README)", "       40HXInstaller.exe -efi <路径> # 部署自己签名的 40HXUNLK.EFI (Secure Boot; 见 README)"))
	fmt.Println(tr("            (a 40HXUNLK.signed.efi next to the installer is picked up automatically)", "            (файл 40HXUNLK.signed.efi рядом с установщиком подхватывается автоматически)", "            (安装器同目录的 40HXUNLK.signed.efi 会自动被采用)"))
}

// ===================== 底层 =====================

func isAdmin() bool {
	var t windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &t)
	if err == nil {
		defer t.Close()
		var e uint32
		var n uint32
		if err = windows.GetTokenInformation(t, windows.TokenElevation,
			(*byte)(unsafe.Pointer(&e)), uint32(unsafe.Sizeof(e)), &n); err == nil && e != 0 {
			return true
		}
		// TokenElevation 可能因受限环境(沙箱/服务)误报 0, 再试 SCM 全权
	}
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ALL_ACCESS)
	if err == nil {
		windows.CloseServiceHandle(scm)
		return true
	}
	return false
}

// enableGsp: 设 EnableGpuFirmware=1 (需管理员)
func enableGsp() error {
	key := hxcore.FindGpuClassKey()
	if key == "" {
		return errors.New(tr("cannot find the 40HX device registry key (Class subkey)", "не найден раздел реестра устройства 40HX (подраздел Class)", "找不到 40HX 的设备注册表键 (Class 子键)"))
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetDWordValue(gpuEnableFw, 1)
}

// disableGsp: 删 EnableGpuFirmware (卸载用, 恢复默认关)
func disableGsp() {
	key := hxcore.FindGpuClassKey()
	if key == "" {
		return
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	k.DeleteValue(gpuEnableFw)
}

// ensureGspSilent: 确保 GSP 启用 (EnableGpuFirmware=1)。
// 供 -gen2(登录自启动)调用: 若 GSP 被改回(≠1)则重新启用。
// 写 HKLM 需管理员: 当前是管理员直接写; 否则注册一次性 SYSTEM 计划任务
// (SYSTEM 权限写 HKLM 无需 UAC, 无窗口)。
// 返回 true = GSP 已启用或已安排重设。
func ensureGspSilent() bool {
	if hxcore.GspEnabled() {
		return true // 已启用
	}
	fmt.Println(tr("[GSP] EnableGpuFirmware was reverted, re-enabling...", "[GSP] EnableGpuFirmware был возвращён обратно, повторное включение...", "[GSP] EnableGpuFirmware 被改回, 重新启用..."))
	if isAdmin() {
		if err := enableGsp(); err != nil {
			fmt.Println(tr("[GSP] Reset failed:", "[GSP] Сбой сброса:", "[GSP] 重设失败:"), err)
			return false
		}
		fmt.Println(tr("[GSP] EnableGpuFirmware=1 has been reset (GSP-RM takes effect after reboot)", "[GSP] EnableGpuFirmware=1 сброшено (GSP-RM вступит в силу после перезагрузки)", "[GSP] 已重设 EnableGpuFirmware=1 (重启后 GSP-RM 生效)"))
		return true
	}
	// 非管理员: 用 SYSTEM 计划任务一次性重设 (无 UAC 弹窗)
	exe, _ := os.Executable()
	abs, _ := filepath.Abs(exe)
	tn := "40HXGspEnsure"
	if out, err := hxcore.RunOut("schtasks.exe", "/create", "/tn", tn,
		"/tr", fmt.Sprintf("\"%s\" -gspensure -silent", abs),
		"/sc", "once", "/st", "00:00", "/ru", "SYSTEM", "/f"); err != nil {
		fmt.Printf(tr("[GSP] Scheduled task creation failed: %s\n", "[GSP] Не удалось создать задачу планировщика: %s\n", "[GSP] 计划任务创建失败: %s\n"), strings.TrimSpace(out))
		return false
	}
	hxcore.RunOut("schtasks.exe", "/run", "/tn", tn)
	hxcore.RunOut("schtasks.exe", "/delete", "/tn", tn, "/f")
	fmt.Println(tr("[GSP] EnableGpuFirmware=1 has been reset via a SYSTEM task", "[GSP] EnableGpuFirmware=1 сброшено через задачу SYSTEM", "[GSP] 已通过 SYSTEM 任务重设 EnableGpuFirmware=1"))
	return true
}

// gspEnsureMain: -gspensure 模式 (SYSTEM 计划任务调用, 只重设 GSP 后退出)
func gspEnsureMain() {
	if isAdmin() {
		if err := enableGsp(); err != nil {
			fmt.Println(tr("[GSP] gspensure reset failed:", "[GSP] Сбой сброса gspensure:", "[GSP] gspensure 重设失败:"), err)
			return
		}
		fmt.Println(tr("[GSP] gspensure: EnableGpuFirmware=1 has been set", "[GSP] gspensure: EnableGpuFirmware=1 установлено", "[GSP] gspensure: EnableGpuFirmware=1 已设置"))
	}
}

func copyEmbedTo(target string, src string) error {
	data, err := embedded.ReadFile("embed/" + src)
	if err != nil {
		return err
	}
	return os.WriteFile(target, data, 0o644)
}

// deployEspEfi: 双路部署 40HXUNLK.EFI 到已挂载的 ESP <esp>。
//
//	A. \EFI\40HX\40HXUNLK.EFI   — BCD 启动项引用路径
//	B. \EFI\Boot\bootx64.efi    — UEFI 标准回退路径 (固件无条件尝试的最后手段;
//	   解决社区大量"装完重启直接进 Windows 没跑解锁"——主板忽略非标准目录)
//
// 备份规则: 若目标 bootx64.efi 存在且不是本工具部署过的副本, 先备份为
//
//	bootx64.efi.40hx.bak (卸载时恢复)。已部署过(.bak 已存在)则直接覆盖。
//
// resolveEFIPayload 决定要部署到 ESP 的 EFI 字节, 优先级从高到低:
//
//	① -efi <path>                       显式指定 — 用户明确表达了意图
//	② 安装器同目录的 40HXUNLK.signed.efi  — 签完名放在旁边即可, GUI 双击也走这条
//	③ 内嵌副本(默认, 未签名)
//
// 为什么需要 ①②: 开了 Secure Boot 的固件不会执行未签名的内嵌副本。用户用自己的
// db 密钥签过之后(见 windows/tools/unlock40x/sign_efi.sh), 字节就与发布包里的不同,
// SHA256SUMS.txt 也不再对得上 —— 所以这条路必须显式, 并且要把来源和签名状态打印
// 出来, 否则用户分不清自己实际装进去的是哪一个。
//
// 显式指定读不到时绝不静默回退到内嵌副本: 那会让用户以为装的是签名版, 而重启后
// 固件静默拒绝执行, 排查代价极高。
func resolveEFIPayload() (data []byte, source string, err error) {
	if i := argIndex(efiPayloadFlag); i >= 0 {
		if i+1 >= len(os.Args) {
			return nil, "", errors.New(tr("-efi requires a path to a signed 40HXUNLK.EFI", "-efi требует путь к подписанному 40HXUNLK.EFI", "-efi 需要给出已签名 40HXUNLK.EFI 的路径"))
		}
		p := os.Args[i+1]
		d, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil, "", fmt.Errorf(tr("cannot read the EFI given with -efi (%s): %v", "не удалось прочитать EFI, указанный через -efi (%s): %v", "无法读取 -efi 指定的 EFI (%s): %v"), p, rerr)
		}
		return d, p, nil
	}
	if exe, eerr := os.Executable(); eerr == nil {
		p := filepath.Join(filepath.Dir(exe), signedSidecarName)
		if d, rerr := os.ReadFile(p); rerr == nil {
			return d, p, nil
		}
	}
	d, rerr := embedded.ReadFile("embed/" + efiFile)
	if rerr != nil {
		return nil, "", rerr
	}
	return d, tr("embedded copy (unsigned)", "встроенная копия (без подписи)", "内嵌副本(未签名)"), nil
}

// normalizeEFIArg 把 -efi 的路径在提权之前转成绝对路径。
// UAC 重启后的新进程工作目录通常是 %SystemRoot%\system32, 相对路径在那里解析不到;
// selfElevate 会原样转发 os.Args, 所以必须在这里就地改写。
func normalizeEFIArg() {
	i := argIndex(efiPayloadFlag)
	if i < 0 || i+1 >= len(os.Args) {
		return
	}
	if abs, err := filepath.Abs(os.Args[i+1]); err == nil {
		os.Args[i+1] = abs
	}
}

// 返回 fallback 是否新备份了原文件。
func deployEspEfi(esp string) (backedUp bool, err error) {
	// 读取一次, 两个路径共用
	data, source, rerr := resolveEFIPayload()
	if rerr != nil {
		return false, rerr
	}
	// 写盘前校验数据本身完整 (PE 头 + 长度合理, 防 embed 损坏或用户给错文件)
	if len(data) < 0x2000 { // < 8KB 的 EFI 文件必为损坏
		return false, fmt.Errorf(tr("the EFI payload from %s is abnormal (%d bytes)", "образ EFI из %s повреждён (%d байт)", "来自 %s 的 EFI 数据异常 (%d bytes)"), source, len(data))
	}
	if !bytes.HasPrefix(data, []byte("MZ")) {
		return false, fmt.Errorf(tr("the EFI payload from %s is not a valid PE image (missing MZ header)", "образ EFI из %s не является корректным PE (нет заголовка MZ)", "来自 %s 的 EFI 不是有效 PE 镜像(缺 MZ 头)"), source)
	}
	// 打印来源 + 哈希 + 签名状态: 开了 Secure Boot 时, 这三行是唯一能解释
	// "为什么重启后解锁没跑"的信息。哈希也让用户能与 sign_efi.sh 的输出对上。
	fmt.Printf(tr("    payload: %s\n", "    образ: %s\n", "    使用的 EFI: %s\n"), source)
	fmt.Printf("    sha256: %x\n", sha256.Sum256(data))
	if sig, perr := hxcore.ReadPESignature(data); perr == nil {
		fmt.Printf("    %s\n", hxcore.FormatSignatureState(sig))
		if !sig.Present && hxcore.SecureBootOn() {
			fmt.Println(tr("    WARNING: Secure Boot is enabled and this image is unsigned — the firmware will silently refuse to run it. Sign it with your own db key or turn Secure Boot off (see README, section Secure Boot).",
				"    ВНИМАНИЕ: Secure Boot включён, а образ без подписи — прошивка молча откажется его запускать. Подпишите его своим ключом db или отключите Secure Boot (см. README, раздел Secure Boot).",
				"    警告: Secure Boot 已开启而该镜像未签名 — 固件会静默拒绝执行。请用自己的 db 密钥签名, 或关闭 Secure Boot (见 README 的 Secure Boot 一节)。"))
		}
	}

	// A. 主路径
	dirA := esp + ":" + efiDir // Y:\EFI\40HX
	if merr := os.MkdirAll(dirA, 0o644); merr != nil {
		return false, merr
	}
	pA := filepath.Join(dirA, efiFile)
	if werr := writeVerified(pA, data); werr != nil {
		// 写失败或校验不一致 → 删掉可能半截的文件, 避免被 BCD 引用成坏引导
		os.Remove(pA)
		return false, werr
	}
	fmt.Printf(tr("    [A] %s  (%d bytes, verify OK)\n", "    [A] %s  (%d байт, проверка OK)\n", "    [A] %s  (%d bytes, 校验 OK)\n"), "\\EFI\\40HX\\"+efiFile, len(data))

	// B. 标准回退路径
	dirB := esp + ":" + efiStdDir // Y:\EFI\Boot
	if merr := os.MkdirAll(dirB, 0o644); merr != nil {
		return false, merr
	}
	pB := filepath.Join(dirB, efiStdF) // bootx64.efi
	pBak := pB + efiBakExt             // bootx64.efi.40hx.bak
	if _, berr := os.Stat(pBak); berr != nil {
		// 无备份记录 → 若目标存在且不是我们已部署的副本, 先备份
		if old, oerr := os.ReadFile(pB); oerr == nil && !bytes.Equal(old, data) {
			if cerr := os.Rename(pB, pBak); cerr != nil {
				return false, fmt.Errorf(tr("failed to back up the original %s: %v", "не удалось создать резервную копию исходного %s: %v", "备份原 %s 失败: %v"), pB, cerr)
			}
			fmt.Printf(tr("    [B] original %s backed up as %s\n", "    [B] исходный %s сохранён как %s\n", "    [B] 原 %s 已备份为 %s\n"), efiStdF, efiStdF+efiBakExt)
			backedUp = true
		} else if oerr != nil {
			// 目标不存在: 无备份(本来就是空位)
		}
	}
	if werr := writeVerified(pB, data); werr != nil {
		os.Remove(pB)
		return backedUp, werr
	}
	fmt.Printf(tr("    [B] %s  (%d bytes, verify OK)\n", "    [B] %s  (%d байт, проверка OK)\n", "    [B] %s  (%d bytes, 校验 OK)\n"), "\\EFI\\Boot\\"+efiStdF, len(data))
	return backedUp, nil
}

// writeVerified: 写文件后立即读回比对 — 防止写入中断/半截导致引导损坏。
// 不一致则删除并返回错误(调用方据此中止, 不让坏文件留在引导路径)。
func writeVerified(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	rb, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf(tr("post-write verification read failed %s: %v", "чтение при проверке после записи не удалось %s: %v", "写后校验读取失败 %s: %v"), path, err)
	}
	if !bytes.Equal(rb, data) {
		return fmt.Errorf(tr("post-write verification mismatch %s (%d ≠ %d bytes)", "несоответствие при проверке после записи %s (%d ≠ %d байт)", "写后校验不一致 %s (%d ≠ %d bytes)"), path, len(rb), len(data))
	}
	return nil
}

// alreadyInstalled: 检测是否已安装过(避免无意义/重复的覆盖安装)。
// 判据: ① 固件启动项 "40HX Unlock" 存在; ② ESP 上已有 \EFI\40HX\40HXUNLK.EFI。
// 任一命中即认为装过 — 用于重入提示(不会因此阻止用户, 仅弹确认)。
func alreadyInstalled() bool {
	// ① bcdedit 固件枚举(不挂 ESP, 快速)
	if out, _ := hxcore.RunOut("bcdedit.exe", "/enum", "firmware"); strings.Contains(out, bootDesc) {
		return true
	}
	// ② ESP 文件
	esp := hxcore.MountESP()
	if esp == "" {
		return false // 挂不上 ESP 时保守视为未装(后面 [5/8] 会报错引导)
	}
	defer hxcore.UnmountESP(esp)
	if _, err := os.Stat(esp + ":" + efiDir + "\\" + efiFile); err == nil {
		return true
	}
	return false
}

// verifyBootEntry: 读回 {fwbootmgr} displayorder, 确认 40HX Unlock 是否在首位。
// 返回 (exists, isFirst, displayOrder描述)。
// 用 bcdedit /enum firmware 读固件 NVRAM — 若固件忽略 bcdedit 的写入,
// 这里会如实反映(不在列表/不在首位), 从而让安装器给出 BIOS 手动指引。
// 注意: bcdedit 输出为 GBK, 中文系统"标识符/说明"是乱码; 但字段值
// (guid / displayorder / 40HX Unlock / path) 均为 ASCII, 按块解析可靠。
func verifyBootEntry() (bool, bool, string) {
	out, err := hxcore.RunOut("bcdedit.exe", "/enum", "firmware")
	if err != nil {
		return false, false, tr("(bcdedit read failed: ", "(сбой чтения bcdedit: ", "(bcdedit 读取失败: ") + err.Error() + ")"
	}
	lines := strings.Split(out, "\r\n")
	if len(lines) < 2 {
		lines = strings.Split(out, "\n")
	}

	// 1. 收集 displayorder 下的 GUID 序列(固件实际启动顺序)
	var order []string
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if strings.HasPrefix(t, "displayorder") {
			// 首个 GUID 可能同行: "displayorder {guid}"
			if m := guidRe().FindString(t); m != "" {
				order = append(order, strings.Trim(m, "{}"))
			}
			// 后续缩进行 {guid}
			for j := i + 1; j < len(lines); j++ {
				s := strings.TrimSpace(lines[j])
				if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
					order = append(order, strings.Trim(s, "{}"))
				} else if s != "" {
					break
				}
			}
			break // displayorder 只在 {fwbootmgr} 段, 取首个即可
		}
	}

	// 2. 找 description 为 "40HX Unlock" 的块的 GUID
	target := ""
	for i := 0; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "description") &&
			strings.Contains(lines[i], bootDesc) {
			// 往上找最近的 {guid} 行 = 该块 identifier
			for j := i - 1; j >= 0 && j > i-6; j-- {
				if m := guidRe().FindString(lines[j]); m != "" {
					target = strings.Trim(m, "{}")
					break
				}
			}
			break
		}
	}
	if target == "" {
		joined := strings.Join(order, " > ")
		if joined == "" {
			joined = tr("(firmware has no displayorder entries)", "(в прошивке нет записей displayorder)", "(固件无 displayorder 条目)")
		}
		return false, false, joined
	}
	if len(order) == 0 {
		return true, false, tr("(displayorder is empty)", "(displayorder пуст)", "(displayorder 为空)")
	}
	isFirst := order[0] == target
	return true, isFirst, strings.Join(order, " > ")
}

var _guidRe = regexp.MustCompile(`\{([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\}`)

func guidRe() *regexp.Regexp { return _guidRe }

// ===================== 安装 =====================

// applyPowerSettings: 快速启动 + PCIe ASPM 两项电源优化(v2.6.0 [3.6/8] 段抽取,
// v2.6.0 GUI 策略页复用)。幂等: 原本已关则不动; 返回逐项说明行。
func applyPowerSettings() []string {
	notes := []string{}
	if hxcore.FastStartupOn() {
		if err := hxcore.SetFastStartupOff(); err != nil {
			notes = append(notes, fmt.Sprintf(tr("Failed to disable Fast Startup: %v (does not affect the install; recommend disabling it manually in Power Options)", "Не удалось отключить быстрый запуск: %v (на установку не влияет; рекомендуется отключить вручную в параметрах электропитания)", "快速启动关闭失败: %v (不影响安装, 建议电源选项手动关)"), err))
		} else {
			notes = append(notes, tr("Fast Startup disabled (was on): shutdown will now do a full UEFI boot; can be restored in Power Options", "Быстрый запуск отключён (был включён): при выключении теперь выполняется полная загрузка UEFI; можно восстановить в параметрах электропитания", "快速启动已关闭(原为开): 关机将走完整 UEFI 引导; 电源选项可恢复"))
		}
	} else {
		notes = append(notes, tr("Fast Startup: already off (OK)", "Быстрый запуск: уже отключён (OK)", "快速启动: 原本已关(OK)"))
	}
	if ac, dc, ok := hxcore.ASPMSavings(); !ok {
		notes = append(notes, tr("PCIe ASPM: this machine does not expose the setting, skipped", "PCIe ASPM: эта машина не предоставляет данную настройку, пропущено", "PCIe ASPM: 本机未公开该设置, 跳过"))
	} else if ac == 0 && dc == 0 {
		notes = append(notes, tr("PCIe ASPM: already off (OK)", "PCIe ASPM: уже отключён (OK)", "PCIe ASPM: 原本已关(OK)"))
	} else {
		if err := hxcore.SetASPMOff(); err != nil {
			notes = append(notes, fmt.Sprintf(tr("Failed to disable ASPM: %v", "Не удалось отключить ASPM: %v", "ASPM 关闭失败: %v"), err))
		} else {
			notes = append(notes, fmt.Sprintf(tr("PCIe ASPM disabled (was AC=%d/DC=%d): reduces idle downshift to Gen1; to restore, see the powercfg commands in the README", "PCIe ASPM отключён (было AC=%d/DC=%d): уменьшает понижение до Gen1 в простое; для восстановления см. команды powercfg в README", "PCIe ASPM 已关闭(原 AC=%d/DC=%d): 减少空闲降到 Gen1; 恢复: powercfg 命令见 README"), ac, dc))
		}
	}
	return notes
}

// installEFI: ESP 双路部署 40HXUNLK.EFI + 固件启动项(v2.6.0 [5/8]+[6/8] 段抽取,
// v2.6.0 GUI 组件安装页复用)。返回 EFI 是否部署成功;
// [7/8] Gen2 任务注册不依赖此结果(EFI 失败只跳过 EFI 两步 — 社区 #2/#5/#6/#7 统一根因修复)。
func installEFI() bool {
	//    主路径  \EFI\40HX\40HXUNLK.EFI  — BCD 启动项引用
	//    fallback \EFI\Boot\bootx64.efi   — UEFI 标准回退路径, 解决部分主板
	//    忽略 BCD displayorder / 不认非标准目录(社区"装完重启没反应"主因)。
	//    原 bootx64.efi 备份为 bootx64.efi.40hx.bak, 卸载时恢复。
	efiOK := false
	fmt.Println(tr("    · Deploying the unlock EFI to the system EFI partition (dual-path)...", "    · Развёртывание разблокировочного EFI в системный раздел EFI (два пути)...", "    · 部署解锁 EFI 到系统 EFI 分区(双路)..."))
	esp := hxcore.MountESP()
	if esp == "" {
		if hxcore.FirmwareIsLegacy() {
			fmt.Println(tr("[!] This system uses Legacy BIOS+MBR boot — no EFI partition, so the unlock EFI cannot be deployed.", "[!] Эта система использует загрузку Legacy BIOS+MBR — нет раздела EFI, поэтому разблокировочный EFI развернуть нельзя.", "[!] 本系统为传统 BIOS(Legacy)+MBR 引导 — 没有 EFI 分区, 解锁 EFI 无法部署。"))
			fmt.Println(tr("    Compute unlock needs UEFI+GPT: first convert losslessly with Microsoft mbr2gpt (full steps in the dialog),", "    Разблокировка вычислений требует UEFI+GPT: сначала выполните преобразование без потерь через Microsoft mbr2gpt (полные шаги в диалоге),", "    算力解锁需要 UEFI+GPT: 请先用微软 mbr2gpt 无损转换(完整步骤见弹窗),"))
			fmt.Println(tr("    after converting and switching to UEFI boot, re-run this installer.", "    после преобразования и переключения на загрузку UEFI перезапустите этот установщик.", "    转换完成并改 UEFI 引导后重跑本安装器。"))
			fmt.Println(tr("    [i] Gen2 logon autostart is unaffected; continuing to register it (see [7/8]).", "    [i] Автозапуск Gen2 при входе не затронут; продолжаем его регистрацию (см. [7/8]).", "    [i] Gen2 登录自启不受影响, 继续注册(见 [7/8])。"))
			msgbox(tr("40HX Installer (convert the disk to GPT first)", "Установщик 40HX (сначала преобразуйте диск в GPT)", "40HX 安装器 (需要先转换硬盘为 GPT)"),
				tr("This system uses Legacy BIOS+MBR boot and has no EFI partition,\n", "Эта система использует загрузку Legacy BIOS+MBR и не имеет раздела EFI,\n", "本系统是传统 BIOS(Legacy)+MBR 引导, 没有 EFI 分区,\n")+
					tr("so the compute-unlock EFI cannot be deployed — this is why \"the EFI won\x27t install\".\n\n", "поэтому разблокировочный EFI развернуть нельзя — вот почему \"EFI не устанавливается\".\n\n", "算力解锁 EFI 无法部署 — 这就是\"EFI 装不上\"的原因。\n\n")+
					tr("First convert to UEFI+GPT (Microsoft official lossless conversion, keeps your data):\n", "Сначала преобразуйте в UEFI+GPT (официальное преобразование Microsoft без потерь, данные сохраняются):\n", "请先转成 UEFI+GPT(微软官方无损转换, 不动数据):\n")+
					tr("  1. Back up important data; make sure BitLocker is off (suspend it first if on)\n", "  1. Сделайте резервную копию важных данных; убедитесь, что BitLocker отключён (если включён, сначала приостановите)\n", "  1. 备份重要数据; 确认未启用 BitLocker(有则先暂停)\n")+
					tr("  2. In an admin Command Prompt run:  mbr2gpt /validate /allowfullos\n", "  2. В командной строке администратора выполните:  mbr2gpt /validate /allowfullos\n", "  2. 管理员命令提示符运行:  mbr2gpt /validate /allowfullos\n")+
					tr("  3. After it shows \"Validation completed successfully\" run:\n", "  3. После сообщения \"Validation completed successfully\" выполните:\n", "  3. 显示 Validation completed successfully 后运行:\n")+
					"        mbr2gpt /convert /allowfullos\n"+
					tr("  4. Reboot into BIOS and change boot mode from Legacy to UEFI (disable CSM)\n", "  4. Перезагрузитесь в BIOS и смените режим загрузки с Legacy на UEFI (отключите CSM)\n", "  4. 重启进 BIOS, 把启动模式从 Legacy 改为 UEFI(关 CSM)\n")+
					tr("  5. Boot into Windows and re-run this installer\n\n", "  5. Загрузитесь в Windows и перезапустите этот установщик\n\n", "  5. 进 Windows 后重新运行本安装器\n\n")+
					tr("Note: the conversion is irreversible; requires Win10 1703+ / Win11 and a UEFI-capable board.\n", "Внимание: преобразование необратимо; требуется Win10 1703+ / Win11 и материнская плата с поддержкой UEFI.\n", "注意: 转换不可逆; 需 Win10 1703+ / Win11 且主板支持 UEFI。\n")+
					tr("This install will continue with the Gen2 part (do compute unlock later by re-running the installer after converting).", "Эта установка продолжит часть Gen2 (разблокировку вычислений выполните позже, перезапустив установщик после преобразования).", "本次安装将继续完成 Gen2 部分(算力解锁等转换后重跑安装器)。"),
				mbIconWarn)
		} else {
			fmt.Println(tr("[!] Cannot mount the EFI partition (mountvol /S failed)", "[!] Не удаётся смонтировать раздел EFI (mountvol /S не выполнен)", "[!] 无法挂载 EFI 分区(mountvol /S 失败)"))
			fmt.Println(tr("    The system is UEFI; common causes: BitLocker/third-party encryption not suspended, or an abnormal ESP.", "    Система UEFI; частые причины: не приостановлено BitLocker/стороннее шифрование, либо аномальный ESP.", "    系统是 UEFI, 常见原因: BitLocker/第三方加密未暂停、ESP 分区异常。"))
			fmt.Println(tr("    Manual: mountvol S: /S, copy 40HXUNLK.EFI to S:\\EFI\\40HX\\, mountvol S: /D", "    Вручную: mountvol S: /S, скопируйте 40HXUNLK.EFI в S:\\EFI\\40HX\\, mountvol S: /D", "    可手动: mountvol S: /S, 复制 40HXUNLK.EFI 到 S:\\EFI\\40HX\\, mountvol S: /D"))
			msgbox(tr("40HX Installer (EFI partition mount failed)", "Установщик 40HX (не удалось смонтировать раздел EFI)", "40HX 安装器 (EFI 分区挂载失败)"),
				tr("Cannot mount the EFI partition (mountvol /S failed); the unlock EFI was not deployed this time.\n", "Не удаётся смонтировать раздел EFI (mountvol /S не выполнен); разблокировочный EFI в этот раз не развёрнут.\n", "无法挂载 EFI 分区 (mountvol /S 失败), 解锁 EFI 本次未部署。\n")+
					tr("System boot is unaffected.\n\n", "На загрузку системы это не влияет.\n\n", "系统引导不受影响。\n\n")+
					tr("Common causes: BitLocker/third-party encryption not suspended, or an abnormal ESP.\n", "Частые причины: не приостановлено BitLocker/стороннее шифрование, либо аномальный ESP.\n", "常见原因: BitLocker/第三方加密未暂停、ESP 分区异常。\n")+
					tr("You can deploy it manually (see the log and 《EFI应急修复指南.md》, the EFI emergency-recovery guide).\n\n", "Можно развернуть вручную (см. журнал и 《EFI应急修复指南.md》 — руководство по аварийному восстановлению EFI).\n\n", "可手动部署(见日志与《EFI应急修复指南.md》)。\n\n")+
					tr("This install will continue with the Gen2 part; compute unlock takes effect once the EFI is deployed successfully.", "Эта установка продолжит часть Gen2; разблокировка вычислений вступит в силу после успешного развёртывания EFI.", "本次安装将继续完成 Gen2 部分, 算力解锁待 EFI 部署成功后生效。"),
				mbIconWarn)
		}
		return false
	}
	fmt.Printf(tr("    ESP mounted at %s: \\\n", "    ESP смонтирован в %s: \\\n", "    ESP 挂载于 %s: \\\n"), esp)
	fb, err := deployEspEfi(esp)
	hxcore.UnmountESP(esp)
	if err != nil {
		fmt.Println(tr("[!] Failed to copy the EFI:", "[!] Не удалось скопировать EFI:", "[!] 复制 EFI 失败:"), err)
		msgbox(tr("40HX Installer (EFI write failed)", "Установщик 40HX (сбой записи EFI)", "40HX 安装器 (EFI 写入失败)"),
			tr("Failed to copy the unlock EFI to the ESP (post-write verification was done, no bad file is left behind):\n", "Не удалось скопировать разблокировочный EFI на ESP (проверка после записи выполнена, повреждённый файл не остаётся):\n", "复制解锁 EFI 到 ESP 失败(已做写后校验, 坏文件不会残留):\n")+err.Error()+
				tr("\n\nSystem boot is unaffected; a reboot should enter Windows normally.\n\n", "\n\nНа загрузку системы это не влияет; после перезагрузки Windows должна загрузиться нормально.\n\n", "\n\n系统引导未受影响, 重启应能正常进 Windows。\n\n")+
				tr("To deploy manually, see 《EFI应急修复指南.md》 (EFI emergency-recovery guide) in this folder,\n", "Для ручного развёртывания см. 《EFI应急修复指南.md》 (руководство по аварийному восстановлению EFI) в этой папке,\n", "如需手动部署, 见同目录《EFI应急修复指南.md》中\n")+
				tr("section \"Manual deployment\".\n\n", "раздел \"Ручное развёртывание\".\n\n", "“手动部署”一节。\n\n")+
				tr("This install will continue with the Gen2 part.", "Эта установка продолжит часть Gen2.", "本次安装将继续完成 Gen2 部分。"), mbIconWarn)
		return false
	}
	if fb {
		fmt.Println(tr("    [!] Detected an existing bootx64.efi; backed it up as bootx64.efi.40hx.bak", "    [!] Обнаружен существующий bootx64.efi; сохранён как bootx64.efi.40hx.bak", "    [!] 检测到原 bootx64.efi, 已备份为 bootx64.efi.40hx.bak"))
	}
	efiOK = true

	// BootOrder (v2.4: 写回验证 + BIOS 指引弹框); 仅 EFI 部署成功才执行
	fmt.Println(tr("    · Setting the firmware boot entry (40HX Unlock on top)...", "    · Настройка записи загрузки прошивки (40HX Unlock первым)...", "    · 设置固件启动项(40HX Unlock 置顶)..."))
	bootOK := false
	if err := setupBootEntry(); err != nil {
		fmt.Println(tr("[!] Failed to set the boot entry automatically:", "[!] Не удалось автоматически задать запись загрузки:", "[!] 自动设置启动项失败:"), err)
	} else {
		if ex, first, ord := verifyBootEntry(); ex {
			bootOK = first
			if first {
				fmt.Println(tr("    Boot entry set on top and verified (first in the firmware displayorder)", "    Запись загрузки поставлена первой и проверена (первая в displayorder прошивки)", "    启动项已置顶并验证通过 (固件 displayorder 首位)"))
			} else {
				fmt.Println(tr("    [!] Boot entry created, but not first in displayorder:", "    [!] Запись загрузки создана, но не первая в displayorder:", "    [!] 启动项已创建, 但不在 displayorder 首位:"))
				fmt.Println(tr("        current firmware order: ", "        текущий порядок прошивки: ", "        当前固件顺序: ") + ord)
				fmt.Println(tr("        Enter BIOS and manually set '40HX Unlock' as the first boot entry (see dialog)", "        Войдите в BIOS и вручную установите '40HX Unlock' первой записью загрузки (см. диалог)", "        请进 BIOS 手动将 '40HX Unlock' 设为第一启动项(见弹窗)"))
			}
		} else {
			fmt.Println(tr("    [!] Could not find the '40HX Unlock' entry in the firmware boot list", "    [!] Не удалось найти запись '40HX Unlock' в списке загрузки прошивки", "    [!] 未能在固件启动列表中找到 '40HX Unlock' 项"))
			fmt.Println(tr("        (some boards ignore BCD writes; enter BIOS to add it / move it to the top manually)", "        (некоторые платы игнорируют запись BCD; войдите в BIOS, чтобы добавить/поднять запись вручную)", "        (部分主板忽略 BCD 写入, 请进 BIOS 手动添加/置顶)"))
		}
	}
	if !bootOK {
		// BIOS 指引弹窗 (社区用户不看日志/README 的关键一步)
		msgbox(tr("40HX Installer (important: please follow the steps)", "Установщик 40HX (важно: следуйте инструкциям)", "40HX 安装器 (重要: 请按提示操作)"),
			tr("The automatic boot entry was not accepted by the firmware.\n", "Автоматическая запись загрузки не принята прошивкой.\n", "自动启动项未被固件接受。\n")+
				tr("Reboot and press Del/F2 to enter BIOS, then do the following (otherwise it will not unlock):\n\n", "Перезагрузитесь и нажмите Del/F2 для входа в BIOS, затем выполните следующее (иначе разблокировки не будет):\n\n", "请重启并按 Del/F2 进 BIOS, 完成以下设置(否则不解锁):\n\n")+
				tr("1. Disable Secure Boot (if on, the unsigned EFI is rejected)\n", "1. Отключите Secure Boot (если включён, неподписанный EFI отклоняется)\n", "1. 关闭 Secure Boot(已开则未签名 EFI 会被拒)\n")+
				tr("2. Disable Fast Boot / Fast Startup (if present)\n", "2. Отключите Fast Boot / быстрый запуск (если есть)\n", "2. 关闭 Fast Boot / 快速启动(若有)\n")+
				tr("3. In [Boot Order / Boot Priority] set '40HX Unlock' as the first entry\n", "3. В [Порядок загрузки / Boot Priority] установите '40HX Unlock' первым пунктом\n", "3. 在 [启动顺序/Boot Priority] 中把 '40HX Unlock' 设为第一项\n")+
				tr("   or manually pick \\EFI\\40HX\\40HXUNLK.EFI from the boot device menu\n", "   или вручную выберите \\EFI\\40HX\\40HXUNLK.EFI из меню загрузочных устройств\n", "   或手动从启动设备选择 \\EFI\\40HX\\40HXUNLK.EFI\n")+
				tr("4. If the list only shows Windows Boot Manager:\n", "4. Если в списке только Windows Boot Manager:\n", "4. 若列表只有 Windows Boot Manager:\n")+
				tr("   - some boards only show the entry after CSM is disabled (pure UEFI)\n", "   - некоторые платы показывают запись только после отключения CSM (чистый UEFI)\n", "   - 部分主板需关闭 CSM(纯 UEFI)后才会出现该启动项\n")+
				tr("   - or boot directly from the UEFI drive letter (uses the bootx64 fallback)\n\n", "   - или загрузитесь прямо с буквы диска UEFI (используется запасной bootx64)\n\n", "   - 或直接选 UEFI 盘符启动(走 bootx64 回退)\n\n")+
				tr("The installer also deployed the unlock EFI to:\n", "Установщик также развернул разблокировочный EFI в:\n", "安装器已把解锁 EFI 同时部署到:\n")+
				tr("  \\EFI\\40HX\\40HXUNLK.EFI  (BCD path)\n", "  \\EFI\\40HX\\40HXUNLK.EFI  (путь BCD)\n", "  \\EFI\\40HX\\40HXUNLK.EFI  (BCD 路径)\n")+
				tr("  \\EFI\\Boot\\bootx64.efi    (standard fallback path)\n\n", "  \\EFI\\Boot\\bootx64.efi    (стандартный запасной путь)\n\n", "  \\EFI\\Boot\\bootx64.efi    (标准回退路径)\n\n")+
				tr("Detailed log: ", "Подробный журнал: ", "详细日志: ")+filepath.Join(os.TempDir(), "40HX_installer.log"),
			mbIconError)
	}
	return efiOK
}

func install() {
	fmt.Println("==============================================")
	fmt.Println("  CMP 40HX Windows Unlock Installer "+hxcore.Version)
	fmt.Println(tr("  Tensor unlock (EFI V70 + GSP enable) + PCIe Gen2 + autostart", "  Разблокировка Tensor (EFI V70 + включение GSP) + PCIe Gen2 + автозапуск", "  Tensor 解锁(EFI V70 + GSP 启用) + PCIe Gen2 + 自启动"))
	fmt.Println("==============================================")

	if !isAdmin() {
		fmt.Println(tr("[!] Administrator rights are required.", "[!] Требуются права администратора.", "[!] 需要管理员权限。"))
		msgbox(tr("40HX Installer", "Установщик 40HX", "40HX 安装器"), tr("Administrator rights are required.\nRight-click this program -> Run as administrator.", "Требуются права администратора.\nЩёлкните программу правой кнопкой -> Запуск от имени администратора.", "需要管理员权限。\n请右键本程序 -> 以管理员身份运行。"), mbIconError)
		return
	}
	if lockOnce(`Local\40HXInstaller_v1`) == nil {
		msgbox(tr("40HX Installer", "Установщик 40HX", "40HX 安装器"), tr("The installer is already running; please do not click again.", "Установщик уже запущен; не нажимайте повторно.", "安装器已在运行, 请勿重复点击。"), mbIconInfo)
		return
	}

	// 0. 重入检测: 已装过(固件启动项/GSP 键已存在) → 确认后再覆盖,
	//    避免用户误以为需要反复安装、或在不知情下覆盖现有部署。
	if alreadyInstalled() {
		fmt.Println(tr("[!] Detected an existing 40HX unlock install (boot entry / GSP key present).", "[!] Обнаружена ранее установленная разблокировка 40HX (есть запись загрузки / ключ GSP).", "[!] 检测到 40HX 解锁已安装过(启动项/GSP 键存在)。"))
		if !msgboxYesNo(tr("40HX Installer", "Установщик 40HX", "40HX 安装器"),
			tr("An existing 40HX unlock install was detected.\n\n", "Обнаружена ранее установленная разблокировка 40HX.\n\n", "检测到 40HX 解锁已安装过。\n\n")+
				tr("Installing again overwrites the existing deployment (drivers and boot entry are updated; system boot is not harmed).\n", "Повторная установка перезапишет существующее развёртывание (драйверы и запись загрузки обновятся; загрузка системы не пострадает).\n", "再次安装会覆盖现有部署(驱动与启动项会更新, 不会损坏系统引导)。\n")+
				tr("If you want to repair a problem / upgrade, choose \"Yes\" to continue;\n", "Если хотите исправить проблему / обновить, нажмите \"Да\" для продолжения;\n", "如果是想修复异常/升级, 选\"是\"继续;\n")+
				tr("If you opened it by mistake, choose \"No\" to keep things as they are.\n\n", "Если открыли по ошибке, нажмите \"Нет\", чтобы оставить всё как есть.\n\n", "如果只是误打开, 选\"否\"保持现状即可。\n\n")+
				tr("Continue reinstalling?", "Продолжить переустановку?", "继续重新安装?")) {
			fmt.Println(tr("Cancelled — keeping the existing installation unchanged.", "Отменено — существующая установка оставлена без изменений.", "已取消 — 保持现有安装不变。"))
			return
		}
		fmt.Println(tr("    User confirmed; continuing with the overwrite install.", "    Пользователь подтвердил; продолжаем установку с перезаписью.", "    用户确认, 继续覆盖安装。"))
	}

	// 1. GPU 检测
	fmt.Print(tr("[1/8] Detecting GPU ... ", "[1/8] Поиск GPU ... ", "[1/8] 检测 GPU ... "))
	if !hxcore.FindGPU() {
		fmt.Println(tr("not found ", "не найдено ", "未找到 ") + gpuVenDev)
		fmt.Println(tr("[!] CMP 40HX not detected. Aborting.", "[!] CMP 40HX не обнаружена. Прерывание.", "[!] 未检测到 CMP 40HX。中止。"))
		msgbox(tr("40HX Installer", "Установщик 40HX", "40HX 安装器"), tr("CMP 40HX graphics card not detected (VEN_10DE&DEV_1F0B).\nInstall aborted.", "Видеокарта CMP 40HX не обнаружена (VEN_10DE&DEV_1F0B).\nУстановка прервана.", "未检测到 CMP 40HX 显卡 (VEN_10DE&DEV_1F0B)。\n安装中止。"), mbIconError)
		return
	}
	fmt.Println(tr("CMP 40HX found", "CMP 40HX найдена", "CMP 40HX 已找到"))

	// 2. Secure Boot
	fmt.Print(tr("[2/8] Secure Boot check ... ", "[2/8] Проверка Secure Boot ... ", "[2/8] Secure Boot 检查 ... "))
	if hxcore.SecureBootOn() {
		fmt.Println(tr("on!", "включён!", "开启!"))
		fmt.Println(tr("[!] When Secure Boot is on, the unsigned EFI (40HXUNLK) is rejected by the firmware.", "[!] При включённом Secure Boot неподписанный EFI (40HXUNLK) отклоняется прошивкой.", "[!] Secure Boot 开启时, 未签名 EFI(40HXUNLK) 会被固件拒绝。"))
		msgbox(tr("40HX Installer (disable Secure Boot)", "Установщик 40HX (отключите Secure Boot)", "40HX 安装器 (需要关闭 Secure Boot)"),
			tr("Secure Boot is on; the unsigned unlock EFI will be rejected by the firmware.\n\n", "Secure Boot включён; неподписанный разблокировочный EFI будет отклонён прошивкой.\n\n", "检测到 Secure Boot 开启, 未签名的解锁 EFI 会被固件拒绝。\n\n")+
				tr("Reboot into BIOS, disable it, then run this installer again:\n", "Перезагрузитесь в BIOS, отключите его, затем снова запустите установщик:\n", "请重启进 BIOS 关闭后再运行本安装器:\n")+
				tr("  1. Reboot and press Del / F2 (some boards F1/F10/F12) to enter BIOS\n", "  1. Перезагрузитесь и нажмите Del / F2 (некоторые платы F1/F10/F12) для входа в BIOS\n", "  1. 重启, 开机按 Del / F2(部分主板 F1/F10/F12)进 BIOS\n")+
				tr("  2. Find the Security / Boot tab\n", "  2. Найдите вкладку Security / Boot\n", "  2. 找 Security / Boot / 启动 选项卡\n")+
				tr("  3. Set Secure Boot to Disabled\n", "  3. Установите Secure Boot в Disabled\n", "  3. 将 Secure Boot 设为 Disabled\n")+
				tr("     (if greyed out, first set CSM/Compatibility mode or restore default security settings)\n", "     (если недоступно, сначала включите CSM/режим совместимости или восстановите настройки безопасности по умолчанию)\n", "     (若灰显, 先设 CSM/兼容模式 或恢复默认安全设置)\n")+
				tr("  4. Save and exit (F10), then run this program again\n\n", "  4. Сохраните и выйдите (F10), затем снова запустите программу\n\n", "  4. 保存退出(F10)后重新运行本程序\n\n")+
				tr("This is required for unlocking: the 40HX unlock EFI has no Microsoft signature.", "Это необходимо для разблокировки: разблокировочный EFI 40HX не имеет подписи Microsoft.", "这是解锁必需的: 40HX 解锁 EFI 无微软签名。"),
			mbIconError)
		return
	}
	fmt.Println(tr("off/unavailable (OK)", "выключен/недоступен (OK)", "关闭/不可用(OK)"))

	// 3. 测试签名 (v2.5 不需要 — BYOVD 预签名驱动普通模式即可加载)
	fmt.Print(tr("[3/8] Test signing ... ", "[3/8] Тестовая подпись ... ", "[3/8] 测试签名 ... "))
	if hxcore.TestSigningOn() {
		fmt.Println(tr("on — not needed since v2.5; after install you can disable it with bcdedit /set testsigning off", "включена — не нужна с v2.5; после установки можно отключить через bcdedit /set testsigning off", "已开启 — v2.5 不需要, 装完可 bcdedit /set testsigning off 关闭"))
	} else {
		fmt.Println(tr("off (OK) — v2.5 needs no test signing at all", "выключена (OK) — v2.5 полностью обходится без тестовой подписи", "关闭(OK) — v2.5 全程免测试签名"))
	}

	// 3.5 GSP 启用 (v2.3: 解锁不黑屏的关键!)
	// 40HX 默认 GSP 关(CPU-RM 模式) -> EFI 解锁后 nvlddmkm 拒绝 -> Code43 黑屏
	// EnableGpuFirmware=1 -> GSP-RM 管理 SEC2/booter -> 接受解锁状态
	fmt.Print(tr("[3.5/8] Enabling GSP (EnableGpuFirmware) ... ", "[3.5/8] Включение GSP (EnableGpuFirmware) ... ", "[3.5/8] 启用 GSP (EnableGpuFirmware) ... "))
	if hxcore.GspEnabled() {
		if sub, _, fw := hxcore.GspDiag(); sub != "" {
			fmt.Printf(tr("already enabled (OK) — Class\\%s EnableGpuFirmware=%d\n", "уже включено (OK) — Class\\%s EnableGpuFirmware=%d\n", "已启用(OK) — Class\\%s EnableGpuFirmware=%d\n"), sub, fw)
		} else {
			fmt.Println(tr("already enabled (OK)", "уже включено (OK)", "已启用(OK)"))
		}
	} else {
		if err := enableGsp(); err != nil {
			// v2.4.1: 附带 AdapterString 诊断 — 伪装驱动(雨糖识别成2070等)会命中此分支
			sub, adapterDiag, _ := hxcore.GspDiag()
			fmt.Println(tr("Set failed:", "Ошибка установки:", "设置失败:"), err)
			// sub == "" is GspDiag's no-match case, where adapterDiag is the
			// subkey-dump diagnostic rather than a real AdapterString — print it
			// plainly. (Previously this matched a "无 CMP 40HX" marker that never
			// appeared in GspDiag's output, so the no-match dump was mislabeled.)
			if sub != "" && adapterDiag != "" {
				fmt.Println(tr("    [!] Actual AdapterString:", "    [!] Фактический AdapterString:", "    [!] 实际 AdapterString:"), adapterDiag)
			} else if adapterDiag != "" {
				fmt.Println("    [!]", adapterDiag)
			}
			fmt.Println(tr("    [!] If the driver is a spoofed build (detected as 2070, etc.): use a non-spoofed driver or set GSP manually", "    [!] Если драйвер поддельной сборки (определяется как 2070 и т.п.): используйте не поддельный драйвер или задайте GSP вручную", "    [!] 若驱动是伪装版(识别成 2070 等): 换未伪装版驱动或手动设 GSP"))
			msgbox(tr("40HX Installer", "Установщик 40HX", "40HX 安装器"), tr("Failed to set EnableGpuFirmware=1 (needs administrator).\nAfter unlock you may get a black screen / dropped driver.\nError: ", "Не удалось установить EnableGpuFirmware=1 (нужен администратор).\nПосле разблокировки возможен чёрный экран / отвал драйвера.\nОшибка: ", "设置 EnableGpuFirmware=1 失败(需管理员)。\n解锁后可能黑屏/掉驱动。\n错误: ")+err.Error()+tr("\nIf the driver is a spoofed build (detected as 2070, etc.), use a non-spoofed driver or check AdapterString with -status.", "\nЕсли драйвер поддельной сборки (определяется как 2070 и т.п.), используйте не поддельный драйвер или проверьте AdapterString через -status.", "\n若驱动是伪装版(识别成2070等),请换未伪装驱动或用 -status 查 AdapterString。"), mbIconError)
			return
		}
		fmt.Println(tr("EnableGpuFirmware=1 set (takes effect after reboot)", "EnableGpuFirmware=1 установлено (вступит в силу после перезагрузки)", "已设 EnableGpuFirmware=1 (重启生效)"))
		fmt.Println(tr("    [!] GSP is required: otherwise, after EFI unlock the driver rejects the GPU -> Code43 black screen", "    [!] GSP обязателен: иначе после разблокировки EFI драйвер не примет GPU -> чёрный экран Code43", "    [!] GSP 必需: 否则 EFI 解锁后驱动不认 -> Code43 黑屏"))
	}

	// 3.6 系统电源设置 (v2.6.0: 社区 v2.4.5 排障结论)
	//     快速启动: "关机→再开"走休眠恢复, 不做完整 UEFI 引导, EFI 可能不跑
	//     PCIe ASPM: 开启时空闲会降到 Gen1, 登录后实测容易被误读成"Gen2 失败"
	//     两项幂等设置, 只在当前为开时改; 均可在电源选项恢复, 不碰其他电源策略
	fmt.Print(tr("[3.6/8] Power settings (Fast Startup + PCIe link power saving) ... ", "[3.6/8] Настройки электропитания (быстрый запуск + энергосбережение канала PCIe) ... ", "[3.6/8] 电源设置(快速启动 + PCIe 链路省电) ... "))
	pwrNotes := applyPowerSettings()
	fmt.Println(tr("Done", "Готово", "完成"))
	for _, n := range pwrNotes {
		fmt.Println("    - " + n)
	}

	// 4. 驱动安装
	fmt.Println(tr("[4/8] Preparing Gen2 BYOVD drivers (ThrottleStop + WinRing0)...", "[4/8] Подготовка драйверов Gen2 BYOVD (ThrottleStop + WinRing0)...", "[4/8] 准备 Gen2 BYOVD 驱动(ThrottleStop + WinRing0)..."))
	installDrivers()

	// 4.5 Defender 精确排除(防杀软误删驱动文件导致 Gen2 自启失败)
	//     只加我们自己的驱动/备份/发布目录, 不关任何系统防护。
	fmt.Print(tr("[4.5/8] Defender exclusions (prevent accidental deletion) ... ", "[4.5/8] Исключения Defender (защита от случайного удаления) ... ", "[4.5/8] Defender 排除(防误删) ... "))
	if err := hxcore.AddDefenderExclusions(); err != nil {
		fmt.Println(tr("not performed (ignorable):", "не выполнено (можно игнорировать):", "未执行(可忽略):"), err)
	} else {
		fmt.Println(tr("Added ThrottleStop/WinRing0 driver files and the backup directory to the exclusion list", "Добавлены файлы драйверов ThrottleStop/WinRing0 и резервный каталог в список исключений", "已加白 ThrottleStop/WinRing0 驱动文件与备份目录"))
	}

	// 5+6. EFI 部署与启动项 (v2.6.0: 抽取为 installEFI, GUI 按组件复用)
	fmt.Println(tr("[5/8]+[6/8] Deploying the unlock EFI and firmware boot entry (dual-path write + displayorder on top)...", "[5/8]+[6/8] Развёртывание разблокировочного EFI и записи загрузки прошивки (запись по двум путям + displayorder первым)...", "[5/8]+[6/8] 部署解锁 EFI 与固件启动项(双路写入 + displayorder 置顶)..."))
	efiOK := installEFI()
	if efiOK {
		// ReBAR: the unlock EFI resizes BAR1 to 8 GiB at boot by default. Clear
		// any stale "norebar" token so a full install always enables it; check
		// afterwards with 40HXCheck.exe (NVIDIA APP/Control Panel never show it).
		applyRebarLoadOption(true)
	}

	// 7. Gen2 自启动(安装时不 retrain!)
	// 重要: 安装过程中绝不执行 Gen2 PCIe 重训。此时 nvlddmkm 正占用 GPU,
	// 强行 retrain 会让 GPU/链路进入异常状态, 导致下次开机 EFI 接力或
	// nvlddmkm 初始化失败(实测: 设备报 code19 / Windows 启动异常进安全模式)。
	// 正确时机 = 重启后登录时执行(与手动方案一致, 已验证稳定)。
	// v2.6.0: 两路互斥串行设计 — Run 键登录瞬间先试 + SYSTEM 任务延迟30s确认;
	// 单实例互斥体(gen2AcquireSingleInstance)保证二者不会同时进入驱动加载临界区。
	// Run 键在普通权限下无法 sc start 驱动 → 自动交权给 SYSTEM 任务(静默)。
	fmt.Println(tr("[7/8] Registering Gen2 logon autostart (SYSTEM task + Run key, mutually-exclusive serial)...", "[7/8] Регистрация автозапуска Gen2 при входе (задача SYSTEM + ключ Run, взаимоисключающе последовательно)...", "[7/8] 注册 Gen2 登录自启动(SYSTEM 任务 + Run 键, 互斥串行)..."))
	setRunKey()
	if err := setupGen2Task(); err != nil {
		// v2.6.0: 任务是 Gen2 链的命脉, 注册失败必须让用户看见并可一键修复
		fmt.Println("[!]", err)
		msgbox(tr("40HX Installer (Gen2 autostart registration failed)", "Установщик 40HX (сбой регистрации автозапуска Gen2)", "40HX 安装器 (Gen2 自启注册失败)"),
			tr("Gen2 logon-autostart task registration failed — Gen2 will not unlock automatically after logon.\n\n", "Не удалось зарегистрировать задачу автозапуска Gen2 при входе — Gen2 не разблокируется автоматически после входа.\n\n", "Gen2 登录自启任务注册失败 — 登录后不会自动解锁 Gen2。\n\n")+
				tr("Please right-click and run once as administrator later:\n", "Позже щёлкните правой кнопкой и один раз запустите от имени администратора:\n", "请稍后右键以管理员身份运行一次:\n")+
				"  40HXInstaller.exe -task\n\n"+
				tr("The remaining installation steps are complete.", "Остальные шаги установки выполнены.", "其余安装步骤已完成。"), mbIconWarn)
	}

	fmt.Println()
	fmt.Println(tr("Installation complete!", "Установка завершена!", "安装完成!"))
	if efiOK {
		fmt.Println(tr("  Next reboot: the firmware will automatically run 40HX Unlock (Tensor unlock) -> boot into Windows", "  При следующей перезагрузке: прошивка автоматически запустит 40HX Unlock (разблокировка Tensor) -> загрузка в Windows", "  下次重启: 固件将自动运行 40HX Unlock (Tensor 解锁) -> 自动进 Windows"))
	} else {
		fmt.Println(tr("  [!] The EFI compute unlock was not deployed this time (see the [5/8] notes) — compute will not be unlocked yet,", "  [!] Разблокировка вычислений через EFI в этот раз не развёрнута (см. пояснения [5/8]) — вычисления пока не разблокированы,", "  [!] EFI 算力解锁本次未部署(见 [5/8] 说明) — 算力暂不会解锁,"))
		fmt.Println(tr("      follow the [5/8] dialog guidance (mbr2gpt / manual deployment), then run this installer again.", "      выполните указания диалога [5/8] (mbr2gpt / ручное развёртывание), затем снова запустите установщик.", "      按 [5/8] 弹窗指引(mbr2gpt/手动部署)处理后重跑本安装器即可。"))
	}
	fmt.Println(tr("  GSP enabled: the driver takes over the GPU in GSP-RM mode, no more black screen / dropped driver after unlock", "  GSP включён: драйвер управляет GPU в режиме GSP-RM, больше нет чёрного экрана / отвала драйвера после разблокировки", "  GSP 已启用: 驱动以 GSP-RM 模式接管 GPU, 解锁后不再黑屏/掉驱动"))
	fmt.Println(tr("  After logon: Gen2 unlocks automatically (autostart registered, silent and windowless)", "  После входа: Gen2 разблокируется автоматически (автозапуск зарегистрирован, тихо и без окна)", "  登录后: Gen2 自动解锁 (已注册自启动, 无窗口静默)"))
	fmt.Println(tr("  [!] PCIe is not retrained during install; it runs at logon after reboot (avoids conflicts with the GPU driver)", "  [!] PCIe не переобучается во время установки; это выполняется при входе после перезагрузки (во избежание конфликтов с драйвером GPU)", "  [!] 安装时不重训 PCIe, 重启后登录时才执行(避免与显卡驱动冲突)"))
	fmt.Println(tr("  Verify after reboot: double-click 40HXCheck.exe to view the unlock status (SS0=0x88888888 means success)", "  Проверка после перезагрузки: дважды щёлкните 40HXCheck.exe, чтобы увидеть статус разблокировки (SS0=0x88888888 — успех)", "  重启后验证: 双击 40HXCheck.exe 查看解锁状态(SS0=0x88888888 即成功)"))
	fmt.Println(tr("  If test signing was just enabled: reboot once first so the driver can load", "  Если тестовая подпись только что включена: сначала перезагрузитесь один раз, чтобы драйвер мог загрузиться", "  若 testsigning 刚开启: 请先重启一次使驱动可加载"))
	// v2.4: 完成弹框含关键 BIOS/重启指引(社区用户不依赖 README 也能操作)
	// v2.6.0: EFI 成败给出不同指引; 告知电源设置已自动调整及恢复方式
	efiNote := ""
	if efiOK {
		efiNote = tr("At reboot, please note:\n", "При перезагрузке обратите внимание:\n", "重启时请注意:\n") +
			tr("  · A black screen / 40HX text log for about 10-30 seconds is normal (unlocking in progress)\n", "  · Чёрный экран / текстовый лог 40HX в течение примерно 10-30 секунд — это нормально (идёт разблокировка)\n", "  · 若黑屏/显示 40HX 文字日志约 10~30 秒, 属正常(正在解锁)\n") +
			tr("  · After unlocking, it boots into Windows automatically\n\n", "  · После разблокировки автоматически загрузится Windows\n\n", "  · 解锁完成后会自动进入 Windows\n\n") +
			tr("If it boots straight into Windows after reboot (no unlock ran), enter BIOS (Del/F2):\n", "Если после перезагрузки сразу загрузилась Windows (разблокировка не выполнялась), войдите в BIOS (Del/F2):\n", "若重启后直接进了 Windows(没跑解锁), 请进 BIOS(Del/F2):\n") +
			tr("  1. Disable Secure Boot (required for the unsigned EFI)\n", "  1. Отключите Secure Boot (нужно для неподписанного EFI)\n", "  1. 关闭 Secure Boot(未签名 EFI 需要)\n") +
			tr("  2. Disable Fast Boot\n", "  2. Отключите Fast Boot\n", "  2. 关闭 Fast Boot\n") +
			tr("  3. Set '40HX Unlock' as the first boot entry\n", "  3. Установите '40HX Unlock' первым в порядке загрузки\n", "  3. 把 '40HX Unlock' 设为第一启动项\n") +
			tr("     (if the list shows only Windows Boot Manager, disable CSM and look again)\n", "     (если в списке только Windows Boot Manager, отключите CSM и посмотрите снова)\n", "     (若列表只有 Windows Boot Manager, 关 CSM 后再看)\n")
	} else {
		efiNote = tr("[!] The EFI compute unlock was not deployed this time (see the dialog/log above for the reason):\n", "[!] Разблокировка вычислений через EFI в этот раз не развёрнута (причину см. в диалоге/журнале выше):\n", "[!] 本次 EFI 算力解锁未部署(原因见上方弹窗/日志):\n") +
			tr("  · Compute will not be unlocked yet; follow the guidance, then run the installer again\n", "  · Вычисления пока не разблокированы; выполните указания, затем снова запустите установщик\n", "  · 算力暂不会解锁, 按指引处理后重跑安装器即可\n") +
			tr("  · Gen2 autostart is registered and unaffected\n", "  · Автозапуск Gen2 зарегистрирован и не затронут\n", "  · Gen2 自启已注册, 不受影响\n")
	}
	msgbox(tr("40HX Installer (installation complete)", "Установщик 40HX (установка завершена)", "40HX 安装器 (安装完成)"),
		tr("✅ Installation complete! ", "✅ Установка завершена! ", "✅ 安装完成! ")+map[bool]string{true: tr("Unlock will run automatically after reboot.", "Разблокировка выполнится автоматически после перезагрузки.", "重启后将自动执行解锁。"), false: tr("The Gen2 part is ready.", "Часть Gen2 готова.", "Gen2 部分已就绪。")}[efiOK]+"\n\n"+
			efiNote+
			tr("\nAfter rebooting into the system:\n", "\nПосле перезагрузки в систему:\n", "\n重启进系统后:\n")+
			tr("  · Double-click 40HXCheck.exe in the same folder to verify — it shows\n", "  · Дважды щёлкните 40HXCheck.exe в той же папке для проверки — отобразится\n", "  · 双击同目录的 40HXCheck.exe 验证 — 显示\n")+
			tr("    'Unlock succeeded: Tensor at full power (SS0=0x88888888)' means done\n", "    'Разблокировка успешна: Tensor на полной мощности (SS0=0x88888888)' — готово\n", "    '解锁成功: Tensor 满血(SS0=0x88888888)' 即完成\n")+
			tr("  · If it reports not unlocked, it gives the next step (such as enabling Above 4G)\n\n", "  · Если сообщит, что не разблокировано, подскажет следующий шаг (например, включить Above 4G)\n\n", "  · 若提示未解锁, 它会给下一步(如开 Above 4G)\n\n")+
			tr("· If test signing was just enabled: reboot once so the driver can load\n", "· Если тестовая подпись только что включена: перезагрузитесь один раз, чтобы драйвер загрузился\n", "· 测试签名若刚开启: 先重启一次驱动才可加载\n")+
			tr("· GSP enabled (EnableGpuFirmware=1): the key to unlocking without a black screen\n", "· GSP включён (EnableGpuFirmware=1): ключ к разблокировке без чёрного экрана\n", "· GSP 已启用(EnableGpuFirmware=1): 解锁不黑屏的关键\n")+
			tr("· Fast Startup and PCIe link power saving (ASPM) were disabled automatically:\n", "· Быстрый запуск и энергосбережение канала PCIe (ASPM) отключены автоматически:\n", "· 已自动关闭快速启动与 PCIe 链路省电(ASPM):\n")+
			tr("  the former ensures a full UEFI boot even after shutdown+power-on, the latter reduces idle drops to Gen1;\n", "  первое обеспечивает полную загрузку UEFI даже после выключения и включения, второе уменьшает падения до Gen1 в простое;\n", "  前者保证关机再开也走完整 UEFI 引导, 后者减少空闲降到 Gen1;\n")+
			tr("  see README §2.4 to restore them\n", "  способ восстановления см. в README §2.4\n", "  恢复方式见 README §2.4\n")+
			tr("· After logon, Gen2 unlocks automatically (silent)\n\n", "· После входа Gen2 разблокируется автоматически (тихо)\n\n", "· 登录后 Gen2 自动解锁(静默)\n\n")+
			tr("Detailed log: ", "Подробный журнал: ", "详细日志: ")+filepath.Join(os.TempDir(), "40HX_installer.log"),
		mbIconInfo)
}

func installDrivers() {
	sysDir := os.Getenv("SystemRoot") + "\\System32\\drivers"
	svcRunning := func(name string) bool {
		out, _ := hxcore.RunOut("sc.exe", "query", name)
		return strings.Contains(out, "RUNNING")
	}
	// v2.5: 不再常驻 40hx_bridge(需测试签名)。Gen2 改 BYOVD:
	//   ThrottleStop(任意物理内存写, EV 预签名) + WinRing0(PCI config) —
	//   两者普通模式(testsigning off)即可加载。安装阶段仅放好驱动文件 +
	//   注册 demand 服务; 真正的加载与自清理由登录后的 -gen2(SYSTEM 任务)
	//   完成 → 用完即卸, 游戏时系统无第三方驱动。
	tsApp := throttleStopAppRunning()
	for _, d := range []struct{ name, file string }{
		{"ThrottleStop", "ThrottleStop.sys"},
		{"WinRing0_1_2_0", "WinRing0x64.sys"},
	} {
		dst := filepath.Join(sysDir, d.file)
		// 本机装了 ThrottleStop 软件 → 复用其同名驱动, 绝不覆盖/删除(避免冲突+写保护)
		if tsApp {
			fmt.Printf(tr("  Detected the ThrottleStop app; reusing its %s driver (not overwriting/deleting)\n", "  Обнаружено приложение ThrottleStop; используем его драйвер %s (не перезаписываем/не удаляем)\n", "  检测到 ThrottleStop 软件, 复用其 %s 驱动(不覆盖/不删)\n"), d.name)
			continue
		}
		if svcRunning(d.name) {
			fmt.Printf(tr("  %s is already running; skipping overwrite (keeping current state)\n", "  %s уже запущен; пропускаем перезапись (сохраняем текущее состояние)\n", "  %s 已在运行, 跳过覆盖(保持当前状态)\n"), d.name)
			continue
		}
		hxcore.RunOut("sc.exe", "stop", d.name)
		// 留一份到 %ProgramData%\40HXUnlock\drivers 作为持久备份源
		// (40HXCheck 实测/Gen2 临时部署都从这里取; System32 的会被用完即卸删除)
		pdDir := filepath.Join(os.Getenv("ProgramData"), "40HXUnlock", "drivers")
		os.MkdirAll(pdDir, 0o755)
		copyEmbedTo(filepath.Join(pdDir, d.file), d.file)
		if err := copyEmbedTo(dst, d.file); err != nil {
			if _, statErr := os.Stat(dst); statErr != nil {
				fmt.Printf(tr("  [!] Failed to copy %s: %v\n", "  [!] Не удалось скопировать %s: %v\n", "  [!] 复制 %s 失败: %v\n"), d.file, err)
				continue
			}
		} else {
			fmt.Printf(tr("  Copied %s\n", "  Скопировано %s\n", "  已复制 %s\n"), d.file)
		}
		ensureService(d.name, d.file)
	}
	fmt.Println(tr("  Gen2 driver files are ready (demand); the SYSTEM task loads them temporarily after logon and self-cleans", "  Файлы драйверов Gen2 готовы (demand); задача SYSTEM временно загружает их после входа и очищает сама", "  Gen2 驱动文件已就绪(demand), 登录后由 SYSTEM 任务临时加载并自清理"))
}

// ensureService: 仅注册(或更新)驱动服务, 不在此处加载。
// 安装阶段加载 40hx_bridge(映射 GPU BAR0)会与正在运行的 nvlddmkm 争用硬件,
// 实测导致 40HX 设备报 code19 / 后续启动异常。加载推迟到重启后登录时的 -gen2。
// v2.4.6 关键修复(社区 #1/#2 根因):
//
//	驱动服务注册为 start=demand(手动), 需在登录后由 -gen2 拉起。
//	而 -gen2 走 Run 键以普通用户权限运行 → sc start 需要管理员 →
//	"[SC] StartService: OpenService 失败 5: 拒绝访问" → 驱动永远起不来
//	→ Gen2 永远失败(用户现象: 算力解锁 OK 但 Gen2 ✗)。
//	正解 = 保持 demand(不改成 auto! 详见下), 并把 -gen2 的执行权限升到
//	SYSTEM: 注册 SYSTEM 计划任务(登录时触发 + 延迟 30s)跑 -gen2 -silent,
//	既不需要 UAC 弹窗, 又保留"登录后才加载驱动"的安全时序。
//
// 为什么不改成 start=auto: type=kernel auto 驱动在开机早期由 SCM 加载,
// 会与随后初始化的 nvlddmkm 争用 GPU BAR0 — 历史上实测导致 40HX 报
// code19 / Windows 启动异常进安全模式。demand + 登录后加载是经过验证的时序。
func ensureService(name string, sysFile string) {
	bin := fmt.Sprintf("\\SystemRoot\\System32\\drivers\\%s", sysFile)
	// 创建(已存在会失败, 忽略); 启动类型 demand — 由 SYSTEM 任务登录后拉起
	hxcore.RunOut("sc.exe", "create", name, "type=", "kernel", "start=", "demand", "binPath=", bin)
	out, err := hxcore.RunOut("sc.exe", "query", name)
	if err != nil || !strings.Contains(out, "STATE") {
		fmt.Printf(tr("  [!] Failed to register service %s: %s\n", "  [!] Не удалось зарегистрировать службу %s: %s\n", "  [!] 注册服务 %s 失败: %s\n"), name, strings.TrimSpace(out))
		return
	}
	// 纠正被安全软件/策略改错的启动类型(Disabled 会导致 Gen2 永远拉不起)。
	// 启动类型在 sc qc, 不在 query; 状态(STOPPED/RUNNING)在 query。
	start := "demand"
	if qc, qerr := hxcore.RunOut("sc.exe", "qc", name); qerr == nil {
		qcu := strings.ToUpper(qc)
		switch {
		case strings.Contains(qcu, "DISABLED"):
			hxcore.RunOut("sc.exe", "config", name, "start=", "demand")
			start = tr("demand (was changed to DISABLED, now fixed)", "demand (было изменено на DISABLED, исправлено)", "demand(原被改 DISABLED, 已修正)")
		case strings.Contains(qcu, "AUTO_START"):
			start = tr("auto (note: should be demand)", "auto (примечание: должно быть demand)", "auto(注意: 应为 demand)")
		}
	}
	stateS := "?"
	switch {
	case strings.Contains(out, "RUNNING"):
		stateS = "RUNNING"
	case strings.Contains(out, "STOPPED"):
		stateS = "STOPPED"
	}
	fmt.Printf(tr("  Service %s registered (%s, %s); loaded by the SYSTEM task after logon\n", "  Служба %s зарегистрирована (%s, %s); загружается задачей SYSTEM после входа\n", "  服务 %s 已注册 (%s, %s), 登录后由 SYSTEM 任务加载\n"), name, start, stateS)
}

// ensureSvcLoaded: 确保驱动服务已注册并加载。
// v2.4.6: 由 SYSTEM 任务(或管理员手动)调用时 sc start 才有权限;
// 普通权限(Run 键兜底)下失败属预期 — 静默交给 SYSTEM 任务处理。

// throttleStopAppRunning: 本机 ThrottleStop 软件进程检测(第三方占用驱动时跳过自清理)。

func throttleStopAppRunning() bool {
	out, _ := hxcore.RunOut("tasklist.exe", "/FI", "IMAGENAME eq ThrottleStop.exe")
	return strings.Contains(out, "ThrottleStop.exe")
}

// redeployDriverFile: v2.6.0 - 杀软可能删驱动文件, 每次 -gen2 前从 embed 重新释放到
// System32\drivers(内容一致则跳过写入, 避免占用冲突)。返回 true = 驱动文件已就绪。

func redeployDriverFile(sysFile string) bool {
	data, err := embedded.ReadFile("embed/" + sysFile)
	if err != nil {
		return false
	}

	target := os.Getenv("SystemRoot") + "\\System32\\drivers\\" + sysFile
	if cur, cerr := os.ReadFile(target); cerr == nil && len(cur) == len(data) {
		return true
	}
	if werr := os.WriteFile(target, data, 0o644); werr != nil {
		return false
	}
	return true
}

func ensureSvcLoaded(name string, sysFile string) {
	if out, _ := hxcore.RunOut("sc.exe", "query", name); strings.Contains(out, "RUNNING") {
		return // 已运行
	}
	if redeployDriverFile(sysFile) {
		hxcore.AddDefenderExclusions()
	}
	bin := fmt.Sprintf("\\SystemRoot\\System32\\drivers\\%s", sysFile)
	hxcore.RunOut("sc.exe", "create", name, "type=", "kernel", "start=", "demand", "binPath=", bin)
	_, err := hxcore.RunOut("sc.exe", "start", name)
	if err != nil {
		// 首次启动失败 - 常见于杀软删除驱动文件或服务配置被改为 disabled。
		// 删除服务 -> 重新部署 -> 用新建服务重试一次。

		hxcore.RunOut("sc.exe", "delete", name)
		redeployDriverFile(sysFile)
		hxcore.RunOut("sc.exe", "create", name, "type=", "kernel", "start=", "demand", "binPath=", bin)
		if out, err := hxcore.RunOut("sc.exe", "start", name); err != nil {
			fmt.Printf(tr("[Gen2] Failed to start service %s: %s\n", "[Gen2] Не удалось запустить службу %s: %s\n", "[Gen2] 启动服务 %s 失败: %s\n"), name, strings.TrimSpace(out))
			if !isAdmin() {
				fmt.Println(tr("[Gen2] Not administrator right now — handing off to the SYSTEM scheduled task (no action needed)", "[Gen2] Сейчас без прав администратора — передаём задаче планировщика SYSTEM (действий не требуется)", "[Gen2] 当前非管理员 — 交给 SYSTEM 计划任务处理(无需操作)"))
			}
		}
	}
}

func setupBootEntry() error {
	// 幂等: 已存在 "40HX Unlock" 项则跳过 (用全量 firmware 枚举, 描述在项详情)
	if out, _ := hxcore.RunOut("bcdedit.exe", "/enum", "firmware"); strings.Contains(out, bootDesc) {
		fmt.Println(tr("    Boot entry already exists; skipping", "    Запись загрузки уже существует; пропускаем", "    启动项已存在, 跳过"))
		return nil
	}
	// 1. copy {bootmgr} 作模板
	out, err := hxcore.RunOut("bcdedit.exe", "/copy", "{bootmgr}", "/d", bootDesc)
	if err != nil {
		return fmt.Errorf("bcdedit copy: %v", err)
	}
	re := regexp.MustCompile(`\{([0-9a-fA-F-]{36})\}`)
	m := re.FindStringSubmatch(out)
	if len(m) < 2 {
		return errors.New(tr("Failed to parse bcdedit output: ", "Не удалось разобрать вывод bcdedit: ", "无法解析 bcdedit 输出: ") + out)
	}
	guid := m[1]
	cleanup := func() { hxcore.RunOut("bcdedit.exe", "/delete", "{"+guid+"}", "/f") }

	// 2. 找 ESP 盘符 (mountvol 重挂)
	esp := hxcore.MountESP()
	if esp == "" {
		cleanup()
		return errors.New(tr("Failed to mount the ESP", "Не удалось смонтировать ESP", "无法挂载 ESP"))
	}
	defer hxcore.UnmountESP(esp)

	// 3. set device + path
	if _, err := hxcore.RunOut("bcdedit.exe", "/set", "{"+guid+"}", "device", "partition="+esp+":"); err != nil {
		cleanup()
		return err
	}
	path := efiDir + "\\" + efiFile // \EFI\40HX\40HXUNLK.EFI
	if _, err := hxcore.RunOut("bcdedit.exe", "/set", "{"+guid+"}", "path", path); err != nil {
		cleanup()
		return err
	}
	// 4. displayorder addfirst
	if _, err := hxcore.RunOut("bcdedit.exe", "/set", "{fwbootmgr}", "displayorder", "{"+guid+"}", "/addfirst"); err != nil {
		cleanup()
		return err
	}
	fmt.Printf(tr("    Boot entry %s moved to the top\n", "    Запись загрузки %s перемещена наверх\n", "    启动项 %s 已置顶\n"), guid)
	return nil
}

// find40HXBootGUID: 从 bcdedit /enum firmware 输出里定位 "40HX Unlock" 启动项的
// GUID。按空行分块, 命中 bootDesc 的块里抽 {guid}(中文系统标签乱码但 GUID 为 ASCII)。
func find40HXBootGUID() string {
	out, err := hxcore.RunOut("bcdedit.exe", "/enum", "firmware")
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`\{([0-9a-fA-F-]{36})\}`)
	for _, blk := range regexp.MustCompile(`\r?\n\r?\n`).Split(out, -1) {
		if strings.Contains(blk, bootDesc) {
			if m := re.FindStringSubmatch(blk); len(m) >= 2 {
				return m[1]
			}
		}
	}
	return ""
}

// applyRebarLoadOption: ReBAR 令牌开关 — 通过 40HX 启动项的 loadoptions 传给解锁 EFI。
//
//	enable=true  → 删除任何 "norebar"(ReBAR 开, 即 EFI 默认); 幂等。
//	enable=false → 写 loadoptions "norebar"(EFI 跳过 8 GiB BAR1 放大)。
//
// EFI 默认(无令牌)= 开, 所以即使固件忽略 loadoptions, "开"也不会退化到关 —
// 启用永不产生回退。生效后用 40HXCheck.exe 验证(NVIDIA APP/控制面板不显示 ReBAR)。
func applyRebarLoadOption(enable bool) {
	guid := find40HXBootGUID()
	if guid == "" {
		fmt.Println(tr("  [ReBAR] no '40HX Unlock' boot entry found — deploy the compute EFI first (ReBAR is applied at boot by that EFI)",
			"  [ReBAR] запись загрузки '40HX Unlock' не найдена — сначала разверните compute EFI (ReBAR применяется этим EFI при загрузке)",
			"  [ReBAR] 未找到 '40HX Unlock' 启动项 — 请先部署算力 EFI(ReBAR 由该 EFI 开机时施加)"))
		return
	}
	if enable {
		// 删除可能存在的 norebar; 无令牌时 bcdedit 返回非零属正常, 不报错。
		hxcore.RunOut("bcdedit.exe", "/deletevalue", "{"+guid+"}", "loadoptions")
		fmt.Println(tr("  [ReBAR] enabled — 'norebar' cleared from the boot entry; the unlock EFI resizes BAR1 to 8 GB at boot. Verify with 40HXCheck.exe.",
			"  [ReBAR] включён — 'norebar' удалён из записи загрузки; разблокировочный EFI увеличит BAR1 до 8 ГБ при загрузке. Проверьте через 40HXCheck.exe.",
			"  [ReBAR] 已启用 — 已从启动项清除 'norebar'; 解锁 EFI 会在开机时把 BAR1 放大到 8 GB。用 40HXCheck.exe 验证。"))
		return
	}
	if _, err := hxcore.RunOut("bcdedit.exe", "/set", "{"+guid+"}", "loadoptions", "norebar"); err != nil {
		fmt.Println(tr("  [ReBAR] failed to set 'norebar': ", "  [ReBAR] не удалось установить 'norebar': ", "  [ReBAR] 设置 'norebar' 失败: "), err)
		return
	}
	fmt.Println(tr("  [ReBAR] disabled — 'norebar' set on the boot entry; the EFI will skip the BAR1 resize (compute unlock is unaffected).",
		"  [ReBAR] отключён — 'norebar' установлен в записи загрузки; EFI пропустит увеличение BAR1 (разблокировка вычислений не затрагивается).",
		"  [ReBAR] 已禁用 — 启动项已写入 'norebar'; EFI 将跳过 BAR1 放大(不影响算力解锁)。"))
}

func setRunKey() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Println(tr("  [!] Failed to get the exe path:", "  [!] Не удалось получить путь к exe:", "  [!] 无法获取 exe 路径:"), err)
		return
	}
	abs, _ := filepath.Abs(exe)
	val := fmt.Sprintf("\"%s\" -gen2 -silent", abs)
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		k, _, err = registry.CreateKey(registry.CURRENT_USER,
			`Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	}
	if err != nil {
		fmt.Println(tr("  [!] Failed to write the Run key:", "  [!] Не удалось записать ключ Run:", "  [!] Run 键写入失败:"), err)
		return
	}
	defer k.Close()
	if err := k.SetStringValue("40HXGen2", val); err != nil {
		fmt.Println(tr("  [!] Failed to set the Run key:", "  [!] Не удалось установить ключ Run:", "  [!] Run 键设置失败:"), err)
		return
	}
	// 注意: 这只是 HKCU Run 键(辅助通道, 登录瞬间先试); SYSTEM 计划任务才是权威通道。
	// 不要打印成"Gen2 已注册", 以免与下方 setupGen2Task 的成功提示混淆。
	fmt.Println(tr("  Gen2 Run key written (HKCU, tried first at logon; the SYSTEM task is the authoritative channel): ", "  Ключ Run для Gen2 записан (HKCU, пробуется первым при входе; задача SYSTEM — основной канал): ", "  Gen2 Run 键已写入(HKCU, 登录瞬间先试; SYSTEM 任务为权威通道): ") + abs)
}

// setupGen2Task: v2.4.6 核心 — 注册 SYSTEM 计划任务, 登录时(延迟 30s)以
// 最高权限静默执行 -gen2。
//
// 为什么需要它: 驱动服务是 demand 启动, 登录后需 sc start 拉起, 而 sc start
// 需要管理员。Run 键以普通用户权限跑 → "OpenService 失败 5: 拒绝访问" →
// 驱动永远起不来 → Gen2 永远失败(社区 #1/#2 的真实根因)。
// 为什么不用 UAC 提权: 每次登录弹 UAC 体验差, 且 UAC 关闭时静默降权仍失败。
// SYSTEM 任务 = 无声的管理员: 权限最高、无弹窗、时机仍在登录后(安全)。
// 注意保持 demand: 若改 auto 会在开机早期加载驱动, 与 nvlddmkm 争用 BAR0
// (历史实测 code19 / 启动异常), demand + 登录后加载才是验证过的时序。
//
// v2.6.0: 改为返回 error; 创建后用 hxcore.TaskInfo 二次校验任务真的存在
// (此前 schtasks 返回成功即认为完成, 用户端"任务未注册"直到 Gen2 没跑才暴露),
// 失败自动重试; 仍失败返回错误, 由调用方弹窗给修复命令(-task)。
//
// v2.6.0 修复(社区"非管理员安装却提示未注册、重启又自动解锁"误报根因):
// schtasks /create 退出码 0 = 任务已提交给计划任务服务(真实成功)。
//
// 权威判据必须且只能是"退出码 0", 不能依赖其 stdout 中的 "SUCCESS/成功" 串:
//
//	· 中文 Windows 上 "成功" 由 schtasks 以系统 ANSI/GBK 代码页写出, 而 Go 把
//	  管道字节当 UTF-8, 字面量 "成功"(UTF-8) 与 GBK 字节不匹配 -> Contains 失败;
//	· 部分环境 schtasks /create 的 stdout 甚至为空(成功信息走别处), 同样无串可匹配;
//	· 此前依赖 "SUCCESS/成功" 串 -> 串缺失即误判, 实测在中文机上稳定复现"假失败"。
//
// 退出码 0 = 任务已写入计划服务, 与语言/代码页无关, 是可靠判据。
// (紧随其后的 /query 仍存在提交延迟竞态, 仅作可选信息, 不再作为成败判据。)
func setupGen2Task() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf(tr("failed to get the exe path: %v", "не удалось получить путь к exe: %v", "无法获取 exe 路径: %v"), err)
	}
	abs, _ := filepath.Abs(exe)
	tn := gen2TaskName
	var lastErr string
	for attempt := 1; attempt <= 3; attempt++ {
		out, cerr := hxcore.RunOut("schtasks.exe", "/create", "/tn", tn,
			"/tr", fmt.Sprintf("\"%s\" -gen2 -silent -guard", abs),
			"/sc", "onlogon", "/ru", "SYSTEM", "/delay", "0000:30", "/f")
		// 权威判据 = 退出码 0。任务已写入计划服务(中文机上 SUCCESS/成功 串不可靠, 不依赖)。
		// 仅在退出码非 0 时才视为真实失败; 退出码 0 一律视为成功, 不再二次查询(避免提交延迟竞态误报)。
		if cerr == nil {
			fmt.Println(tr("  Gen2 task registered (SYSTEM, 30s logon delay, silent): ", "  Задача Gen2 зарегистрирована (SYSTEM, задержка входа 30с, тихо): ", "  Gen2 任务已注册(SYSTEM, 登录延迟30s, 静默): ") + abs)
			return nil
		}
		lastErr = strings.TrimSpace(out)
		if attempt < 3 {
			fmt.Printf(tr("  [!] Task registration failed (attempt %d), retrying... (%s)\n", "  [!] Не удалось зарегистрировать задачу (попытка %d), повтор... (%s)\n", "  [!] 任务注册失败(第%d次), 重试... (%s)\n"), attempt, lastErr)
			time.Sleep(800 * time.Millisecond)
		}
	}
	return fmt.Errorf(tr("Gen2 scheduled task creation failed (after retries): %s\n      Manual fix: run 40HXInstaller.exe -task as administrator", "Не удалось создать задачу планировщика Gen2 (после повторов): %s\n      Вручную: запустите 40HXInstaller.exe -task от имени администратора", "Gen2 计划任务创建失败(已重试): %s\n      可手动: 以管理员运行 40HXInstaller.exe -task"), lastErr)
}

// ===================== Gen2 解锁 (原生, 无 python) =====================

func gen2Main() {
	// 幂等; -silent(登录自启动调用)时全程无窗口静默
	// v2.5: BYOVD (ThrottleStop + WinRing0) — 免测试签名; 用完即卸(自清理)

	// v2.6.0: 单实例互斥 — 防止 SYSTEM 任务 / Run 键 / 手动 -gen2 并发触发时,
	// 两进程同时 sc start 同一驱动、争抢 BAR0 导致链路/驱动状态错乱。
	// 放在最前: 拿不到锁直接退出, 绝不进入驱动加载临界区。
	owned, release := gen2AcquireSingleInstance()
	if !owned {
		fmt.Println(tr("[Gen2] Another Gen2 instance is running; skipping (single-instance guard)", "[Gen2] Другой экземпляр Gen2 уже работает; пропускаем (защита от повторного запуска)", "[Gen2] 另一 Gen2 实例正在运行, 跳过(单实例保护)"))
		hxcore.WriteGen2Status(tr("⏭️ Skipped: another Gen2 instance is running (single-instance guard to avoid concurrent driver contention)", "⏭️ Пропущено: другой экземпляр Gen2 уже работает (защита от повторного запуска во избежание конкуренции за драйвер)", "⏭️ 跳过: 另一 Gen2 实例正在运行(单实例保护, 避免并发抢驱动)"))
		return
	}
	defer release()

	// v2.6.0: 时序保护 — 等 nvlddmkm 进入 RUNNING 后再动 GPU。抢在 nv 驱动初始化前
	// retrain 会被 nv 起来后重置 PCIe 链路 / 覆盖 GPU 寄存器, 既冲掉 Gen2, 又可能触发
	// code19(安装器注释 §785 已实证 "nvlddmkm 正占用 GPU 时 retrain 导致异常")。
	// 普通机器 nv 登录后几秒即 RUNNING → 此处几乎不等待; 慢速/多卡机器则等到就绪,
	// 避免与 nv 初始化重叠(固定 30s 延迟的脆弱性由此消除)。
	waitForNvDriver(60 * time.Second)

	ensureGspSilent()
	defer cleanupByovd() // 注册最早→最后执行(在句柄 Close 后), 失败也清理

	// 驱动文件可能被上次"用完即卸"删除, 每次从 embed 重新放好
	sysDir := os.Getenv("SystemRoot") + "\\System32\\drivers"
	for _, df := range []string{"ThrottleStop.sys", "WinRing0x64.sys"} {
		if _, err := os.Stat(filepath.Join(sysDir, df)); err != nil {
			copyEmbedTo(filepath.Join(sysDir, df), df) // 占用中忽略错误
		}
	}
	ensureSvcLoaded("ThrottleStop", "ThrottleStop.sys")
	ensureSvcLoaded("WinRing0_1_2_0", "WinRing0x64.sys")

	th, err := hxcore.OpenThrottleStop()
	if err != nil {
		if !isAdmin() {
			fmt.Println(tr("[Gen2] ThrottleStop is not loaded and this is not an administrator — handing off to the SYSTEM task, exiting silently", "[Gen2] ThrottleStop не загружен, и это не администратор — передаём задаче SYSTEM, тихий выход", "[Gen2] ThrottleStop 未加载且当前非管理员 — 交给 SYSTEM 任务处理, 静默退出"))
			gen2StatusFail(tr("ThrottleStop driver not loaded (the SYSTEM task will start it)", "Драйвер ThrottleStop не загружен (запустит задача SYSTEM)", "ThrottleStop 驱动未加载(由 SYSTEM 任务负责拉起)"))
			return
		}
		fmt.Println(tr("[Gen2] The ThrottleStop driver is not running. Re-run the installer (as administrator), then reboot.", "[Gen2] Драйвер ThrottleStop не запущен. Перезапустите установщик (от администратора), затем перезагрузитесь.", "[Gen2] ThrottleStop 驱动未运行。请重跑安装器(管理员)后重启。"))
		gen2StatusFail(tr("ThrottleStop driver not running (re-run the installer as administrator)", "Драйвер ThrottleStop не запущен (перезапустите установщик от администратора)", "ThrottleStop 驱动未运行 (需管理员重跑安装器)"))
		gen2Notify(tr("The ThrottleStop driver is not running.\nPossible causes: ① an AV quarantined ThrottleStop.sys (this tool already added a Defender exclusion; for third-party AVs, allow it in the security center); ② the local ThrottleStop app is holding/conflicting with it (close ThrottleStop and retry — this tool reuses it automatically).\nRight-click the installer -> Run as administrator, then reboot.", "Драйвер ThrottleStop не запущен.\nВозможные причины: ① антивирус поместил ThrottleStop.sys в карантин (этот инструмент уже добавил исключение Defender; для сторонних антивирусов разрешите его в центре безопасности); ② локальное приложение ThrottleStop занимает/конфликтует (закройте ThrottleStop и повторите — инструмент использует его автоматически).\nЩёлкните установщик правой кнопкой -> Запуск от имени администратора, затем перезагрузитесь.", "ThrottleStop 驱动未运行。\n可能原因: ①杀软隔离了 ThrottleStop.sys(本工具已加 Defender 排除, 第三方杀软请在安全中心放行); ②本机 ThrottleStop 软件占用/冲突(关闭 ThrottleStop 后重试, 本工具会自动复用)。\n请右键安装程序 -> 以管理员身份运行, 再重启。"))
		return
	}
	// th/wh 句柄可能在 -hard 回退(Stage2)中被重开, 统一在下方 wh 处闭包按最终值关闭

	wh, err := hxcore.OpenDevice(`\\.\WinRing0_1_2_0`)
	if err != nil {
		if !isAdmin() {
			fmt.Println(tr("[Gen2] WinRing0 is not loaded and this is not an administrator — handing off to the SYSTEM task, exiting silently", "[Gen2] WinRing0 не загружен, и это не администратор — передаём задаче SYSTEM, тихий выход", "[Gen2] WinRing0 未加载且当前非管理员 — 交给 SYSTEM 任务处理, 静默退出"))
			gen2StatusFail(tr("WinRing0 driver not loaded, and this is a normal-privilege context (the SYSTEM task will start it)", "Драйвер WinRing0 не загружен, и это контекст обычных прав (запустит задача SYSTEM)", "WinRing0 驱动未加载, 且当前为普通权限(由 SYSTEM 任务负责拉起)"))
			return
		}
		fmt.Println(tr("[Gen2] The WinRing0 driver is not running.", "[Gen2] Драйвер WinRing0 не запущен.", "[Gen2] WinRing0 驱动未运行。"))
		gen2StatusFail(tr("WinRing0 driver not running (re-run the installer as administrator)", "Драйвер WinRing0 не запущен (перезапустите установщик от администратора)", "WinRing0 驱动未运行 (需管理员重跑安装器)"))
		gen2Notify(tr("The WinRing0 driver is not running.\nPossible causes: ① an AV quarantined WinRing0x64.sys (this tool already added a Defender exclusion; for third-party AVs, allow it in the security center); ② the local ThrottleStop app is holding/conflicting with it (close ThrottleStop and retry — this tool reuses it automatically).\nRight-click the installer -> Run as administrator, then reboot.", "Драйвер WinRing0 не запущен.\nВозможные причины: ① антивирус поместил WinRing0x64.sys в карантин (этот инструмент уже добавил исключение Defender; для сторонних антивирусов разрешите его в центре безопасности); ② локальное приложение ThrottleStop занимает/конфликтует (закройте ThrottleStop и повторите — инструмент использует его автоматически).\nЩёлкните установщик правой кнопкой -> Запуск от имени администратора, затем перезагрузитесь.", "WinRing0 驱动未运行。\n可能原因: ①杀软隔离了 WinRing0x64.sys(本工具已加 Defender 排除, 第三方杀软请在安全中心放行); ②本机 ThrottleStop 软件占用/冲突(关闭 ThrottleStop 后重试, 本工具会自动复用)。\n请右键安装程序 -> 以管理员身份运行, 再重启。"))
		return
	}
	// 闭包按最终值关闭 th/wh(支持 -hard 回退中重开驱动句柄)
	defer func() { hxcore.CloseHandle(th); hxcore.CloseHandle(wh) }()

	// 定位 40HX (VEN_10DE&DEV_1F0B), 不硬编码 BDF
	// v2.6.0: 慢速 GPU 初始化(开机 40HX 未就绪)会偶发定位失败 → 重试最多 3 次,
	// 避免"假失败"导致本次开机不解锁(30s 后的 SYSTEM 任务会再确认一次)。
	var gpuBDF uint32
	gpuFound := false
	for attempt := 1; attempt <= 3; attempt++ {
		gpuBDF, gpuFound = hxcore.FindGPUPCI(wh)
		if gpuFound {
			break
		}
		if attempt < 3 {
			fmt.Printf(tr("[Gen2] 40HX not located yet; retrying in 2s (%d/3)...\n", "[Gen2] 40HX пока не найдена; повтор через 2с (%d/3)...\n", "[Gen2] 暂未定位到 40HX, 2s 后重试 (%d/3)...\n"), attempt)
			time.Sleep(2 * time.Second)
		}
	}
	if !gpuFound {
		fmt.Println(tr("[Gen2] Could not locate 40HX (VEN_10DE&DEV_1F0B). Please send the logs.", "[Gen2] Не удалось найти 40HX (VEN_10DE&DEV_1F0B). Пришлите журналы.", "[Gen2] 未能定位 40HX (VEN_10DE&DEV_1F0B)。请发日志。"))
		gen2StatusFail(tr("Could not locate 40HX on the PCI bus (VEN_10DE&DEV_1F0B)", "Не удалось найти 40HX на шине PCI (VEN_10DE&DEV_1F0B)", "未能在 PCI 总线上定位 40HX (VEN_10DE&DEV_1F0B)"))
		gen2Notify(tr("Could not find 40HX on the PCI bus.\nMake sure the card is seated properly and the driver is installed.", "Не удалось найти 40HX на шине PCI.\nУбедитесь, что карта установлена правильно и драйвер установлен.", "未能在 PCI 总线上找到 40HX。\n请确认显卡已插好且驱动已装。"))
		return
	}
	gpuBus := (gpuBDF >> 8) & 0xFF
	fmt.Printf(tr("[Gen2] 40HX is at %02x:%02x.%x\n", "[Gen2] 40HX находится на %02x:%02x.%x\n", "[Gen2] 40HX 位于 %02x:%02x.%x\n"), gpuBus, (gpuBDF>>3)&0x1F, gpuBDF&7)
	cur := hxcore.LinkSpeed(wh, gpuBDF)
	fmt.Printf(tr("[Gen2] Current link: Gen%d\n", "[Gen2] Текущий канал: Gen%d\n", "[Gen2] 当前链路: Gen%d\n"), cur)
	// v2.6.0: 记录原始 PCIe 寄存器(LNKCAP/LNKCTL/LNKCTL2) — 社区反馈 -hard 调试用
	if cap := hxcore.PcieCap(wh, gpuBDF); cap != 0 {
		rd := func(off uint32) uint32 {
			v, _ := hxcore.PciRd(wh, gpuBDF, off)
			return v
		}
		fmt.Printf(tr("[Gen2] LNKCAP=0x%08X LNKCTL=0x%08X LNKCTL2=0x%08X (target Gen%d)\n", "[Gen2] LNKCAP=0x%08X LNKCTL=0x%08X LNKCTL2=0x%08X (цель Gen%d)\n", "[Gen2] LNKCAP=0x%08X LNKCTL=0x%08X LNKCTL2=0x%08X (目标Gen%d)\n"),
			rd(cap+0x0C), rd(cap+0x10), rd(cap+0x30), rd(cap+0x30)&0xF)
	}
	if cur >= 2 {
		fmt.Println(tr("[Gen2] Already Gen2; no action needed.", "[Gen2] Уже Gen2; действий не требуется.", "[Gen2] 已是 Gen2, 无需操作。"))
		hxcore.WriteGen2Status(fmt.Sprintf(tr("✅ Gen2 no action needed: the link is already Gen%d\nRunning as: %s\n40HX location: %02x:%02x.%x\n", "✅ Gen2 действий не требуется: канал уже Gen%d\nЗапуск от: %s\n40HX расположение: %02x:%02x.%x\n", "✅ Gen2 无需操作: 当前链路已是 Gen%d\n运行身份: %s\n40HX 位置: %02x:%02x.%x\n"),
			cur, map[bool]string{true: tr("administrator/SYSTEM", "администратор/SYSTEM", "管理员/SYSTEM"), false: tr("normal user (restricted)", "обычный пользователь (ограничено)", "普通用户(受限)")}[isAdmin()],
			gpuBus, (gpuBDF>>3)&0x1F, gpuBDF&7))
		gen2Notify(tr("PCIe is already Gen", "PCIe уже Gen", "PCIe 已是 Gen") + fmt.Sprint(cur) + tr(", no action needed.", ", действий не требуется.", ", 无需操作。"))
		return
	}

	// 1. PL0 writes (BAR0) — 经 ThrottleStop 物理内存写
	fmt.Println(tr("[Gen2] Writing XVE/link registers (ThrottleStop)...", "[Gen2] Запись регистров XVE/канала (ThrottleStop)...", "[Gen2] 写 XVE/链路寄存器 (ThrottleStop)..."))
	pl0 := []struct {
		off  uint64
		val  uint32
		name string
	}{
		{0x8872C, 0x6, "XVE_OVR=6"},
		{0x8C040, 0x80085800, "LINK_CONFIG_0"},
		{0x8841C, 0xE0B42D00, "PRIV_MISC_1"},
		{0x8C2C0, 0x068731B3, "CYA_0"},
	}
	bar0raw, _ := hxcore.PciRd(wh, gpuBDF, 0x10)
	if bar0raw == 0 || bar0raw == 0xFFFFFFFF {
		bar0raw = 0xF6000000
	}
	bar0Phys := uint64(bar0raw & 0xFFFFFFF0)
	fmt.Printf("[Gen2] BAR0 = 0x%08X\n", bar0Phys)
	// v2.6.0: BAR0 合法性校验 — 写 PL0 前确认 BAR0 真指向 40HX MMIO, 避免把 4 个
	// 链路寄存器写到错误物理地址(多卡/寨板 BAR 重映射、BAR0 读回异常场景)。
	// NV_PMC BOOT_0 @ BAR0+0x0: TU106 家族字节 = 0x16 (unlock40x_v70.c:2555 记 40HX=0x166000A1)。
	// 家族不匹配或读回 0xFFFFFFFF → 中止 PL0 写入(宁可本次不开锁, 不污染他设备 MMIO)。
	boot0, berr := hxcore.TSRead(th, bar0Phys+0x0)
	if berr != nil || (boot0&0xFF000000) != 0x16000000 {
		fmt.Printf(tr("[Gen2][!] BAR0 validity check failed: BOOT_0=0x%08X (expected TU10x family 0x16xxxxxx); aborting PL0 writes\n", "[Gen2][!] Проверка корректности BAR0 не пройдена: BOOT_0=0x%08X (ожидалось семейство TU10x 0x16xxxxxx); прерывание записи PL0\n", "[Gen2][!] BAR0 合法性校验失败: BOOT_0=0x%08X (期望 TU10x 家族 0x16xxxxxx), 中止 PL0 写入\n"), boot0)
		gen2StatusFail(fmt.Sprintf(tr("BAR0 validation failed (BOOT_0=0x%08X); safely aborting PL0 writes; please send the logs", "Проверка BAR0 не пройдена (BOOT_0=0x%08X); безопасное прерывание записи PL0; пришлите журналы", "BAR0 校验失败(BOOT_0=0x%08X), 安全中止 PL0 写入; 请发日志"), boot0))
		if !hasArg("-silent") {
			gen2Notify(tr("BAR0 validation failed; Gen2 safely aborted.\nPlease send the logs.", "Проверка BAR0 не пройдена; Gen2 безопасно прервано.\nПришлите журналы.", "BAR0 校验失败, Gen2 安全中止。\n请发日志。"))
		}
		return
	}
	fmt.Printf(tr("[Gen2] BAR0 verified (BOOT_0=0x%08X, TU106)\n", "[Gen2] BAR0 проверен (BOOT_0=0x%08X, TU106)\n", "[Gen2] BAR0 校验通过 (BOOT_0=0x%08X, TU106)\n"), boot0)
	for _, p := range pl0 {
		if werr := hxcore.TSWrite(th, bar0Phys+p.off, p.val); werr != nil {
			fmt.Printf(tr("  [!] %s write failed: %v\n", "  [!] %s: ошибка записи: %v\n", "  [!] %s 写失败: %v\n"), p.name, werr)
			continue
		}
		if rb, rerr := hxcore.TSRead(th, bar0Phys+p.off); rerr != nil || rb != p.val {
			fmt.Printf(tr("  [warn] %s read back 0x%08x (expected 0x%08x)\n", "  [warn] %s: обратное чтение 0x%08x (ожидалось 0x%08x)\n", "  [warn] %s 读回 0x%08x (期望 0x%08x)\n"), p.name, rb, p.val)
		} else {
			fmt.Printf("  %s OK (0x%08X)\n", p.name, rb)
		}
	}

	// 2. LNKCTL2 TLS=2 (GPU + root)
	root := hxcore.FindRootPort(wh, gpuBus)
	if root == 0xFFFFFFFF {
		fmt.Println(tr("[Gen2] Root port not found; using GPU retrain fallback", "[Gen2] Корневой порт не найден; используется откат через переобучение GPU", "[Gen2] 未找到 root port, 用 GPU retrain fallback"))
	}
	fmt.Printf("[Gen2] root port = 00:%02x.%x\n", (root>>3)&0x1F, root&7)
	for _, b := range []struct {
		bdf uint32
		tag string
	}{{gpuBDF, "GPU"}, {root, "ROOT"}} {
		if b.bdf == 0xFFFFFFFF {
			continue
		}
		cap := hxcore.PcieCap(wh, b.bdf)
		if cap == 0 {
			continue
		}
		// v2.6.0: 读改写 — 只改 TLS(bit3:0), 保留其余位(对齐 python 版)。
		// 此前直接写 {2,0} 清掉高 12 位, 个别 VBIOS 依赖这些位时链路异常。
		curRaw, _ := hxcore.PciRd(wh, b.bdf, cap+0x30)
		nv := uint16(curRaw&0xFFF0) | 2
		hxcore.PciWr(wh, b.bdf, cap+0x30, []byte{byte(nv), byte(nv >> 8)})
		rb, _ := hxcore.PciRd(wh, b.bdf, cap+0x30)
		fmt.Printf(tr("  %s LNKCTL2 TLS=2 (0x%04X -> 0x%04X, read-back TLS=%d)\n", "  %s LNKCTL2 TLS=2 (0x%04X -> 0x%04X, обратное чтение TLS=%d)\n", "  %s LNKCTL2 TLS=2 (0x%04X -> 0x%04X, 回读 TLS=%d)\n"), b.tag, curRaw&0xFFFF, rb&0xFFFF, rb&0xF)
	}

	// 3. UPGRADE retrain: 清位→置位脉冲 (只置位在 40HX 上不生效)
	retrain := func(bdf uint32) {
		cap := hxcore.PcieCap(wh, bdf)
		if cap == 0 {
			return
		}
		ctl, _ := hxcore.PciRd(wh, bdf, cap+0x10)
		lo := uint16(ctl & 0xFFFF)
		buf := []byte{byte(lo & 0xFF), byte((lo >> 8) & 0xFF)}
		buf[0] &^= 0x20 // clear bit5
		hxcore.PciWr(wh, bdf, cap+0x10, buf)
		time.Sleep(300 * time.Millisecond)
		ctl2, _ := hxcore.PciRd(wh, bdf, cap+0x10)
		lo2 := uint16(ctl2 & 0xFFFF)
		buf2 := []byte{byte(lo2 & 0xFF), byte((lo2 >> 8) & 0xFF)}
		buf2[0] |= 0x20 // set bit5
		hxcore.PciWr(wh, bdf, cap+0x10, buf2)
	}
	// v2.6.0: 单次 root 重训 → 最多 6 轮 root/GPU 交替(对齐 python 版, 比初版 4 轮更稳)。
	// 寨板/双卡下根端口一次脉冲常训不上(issue #8 "需反复禁用/启用"),
	// 交替多轮显著提高成功率; 达成 Gen2 即提前退出(上限约 13s, 登录后 30s 才跑)。
	for attempt := 0; attempt < 6; attempt++ {
		bdf, tag := gpuBDF, "GPU"
		if attempt%2 == 0 && root != 0xFFFFFFFF {
			bdf, tag = root, "ROOT"
		}
		fmt.Printf(tr("[Gen2] Link retrain #%d (%s side)...\n", "[Gen2] Переобучение канала #%d (сторона %s)...\n", "[Gen2] 链路重训 #%d (%s端)...\n"), attempt+1, tag)
		retrain(bdf)
		time.Sleep(2200 * time.Millisecond)
		cur = hxcore.LinkSpeed(wh, gpuBDF)
		if cur >= 2 {
			break
		}
	}

	// v2.6.0: 判据修正 — 驱动/ASPM 会在空闲时把链路降到 Gen1 省电, 只看当前
	// 速率会把成功误报成失败(社区"Gen1"误报来源之一, v2.4.5 时代已实证:
	// "待机省电时为 Gen1, 负载下自动跑满 Gen2")。以 GPU LNKCTL2 的
	// TLS(目标速率)区分: TLS>=2 且当前 Gen1 = 配置成功, 空闲降速属正常。
	tls := uint32(0)
	if gcap := hxcore.PcieCap(wh, gpuBDF); gcap != 0 {
		if v, rerr := hxcore.PciRd(wh, gpuBDF, gcap+0x30); rerr == nil {
			tls = v & 0xF
		}
	}
	// v2.5.2: Stage2 自动化(社区 #11/#20/#8 + 贴吧多平台复现的实证解法) —
	// 寨板/多卡/X99 平台 retrain-only 开机训不上, 需要 Root Link Disable(+
	// PnP 恢复)才能上 Gen2, 且每次开机都得重来一次(#20 实证); 手动 -hard
	// 用户根本不会做, 贴吧/B站大量"每开机手动禁用启用显卡"的变通皆源于此。
	// 现在登录任务在 Stage1 未达成(cur<2)时自动执行一次 Stage2(静默, 上限约1分钟):
	//   · 无论 TLS: TLS 已配而链路仍 Gen1 → LD 会立即训上并消除"空闲降速"歧义;
	//     TLS 没配上 → LD 后重写常能粘住(贴吧 .06 批次用户 LD 后同样成功,
	//     说明"写保护批次"与"retrain-only 不够"此前被混为一谈)。
	//   · 退出开关: reg add HKLM\SOFTWARE\40HXUnlock /v Gen2AutoHard /t REG_DWORD /d 0 /f
	//     (40HX 是唯一显示卡的机器若不想要登录后数秒黑屏, 可关)
	//   · 手动 -hard 保留: cur<2 即强制走该路径(不再要求 tls>=2)。
	// Link Disable 期间 nvidia-smi 短暂报 "GPU is lost", 结束后自动 PnP 恢复。
	if cur < 2 && (hasArg("-hard") || gen2AutoHardEnabled()) {
		if hasArg("-hard") {
			fmt.Println(tr("[Gen2] retrain did not succeed → -hard explicitly triggers the Link Disable fallback", "[Gen2] переобучение не удалось → -hard явно запускает откат Link Disable", "[Gen2] retrain 未成 → -hard 显式触发 Link Disable 回退"))
		} else {
			fmt.Println(tr("[Gen2] retrain did not succeed → automatically running the Link Disable fallback (Gen2AutoHard is on by default; see README §2.5 to disable)", "[Gen2] переобучение не удалось → автоматический откат Link Disable (Gen2AutoHard включён по умолчанию; отключение см. в README §2.5)", "[Gen2] retrain 未成 → 自动执行 Link Disable 回退 (Gen2AutoHard 默认开; 关闭方法见 README §2.5)"))
		}
		gen2HardFallback(&th, &wh, gpuBDF, bar0Phys, root)
		return
	}
	gen2Verdict := ""
	unlocked := false
	switch {
	case cur >= 2:
		unlocked = true
		fmt.Printf("[Gen2] *** GEN2 ACHIEVED (Gen%d) ***\n", cur)
		gen2Verdict = fmt.Sprintf(tr("✅ Gen2 succeeded: current link Gen%d", "✅ Gen2 успешно: текущий канал Gen%d", "✅ Gen2 成功: 当前链路 Gen%d"), cur)
	case tls >= 2:
		unlocked = true
		fmt.Printf(tr("[Gen2] TLS=Gen%d but currently Gen%d — idle power-saving downshift (returns to Gen2 under load)\n", "[Gen2] TLS=Gen%d, но сейчас Gen%d — понижение для экономии в простое (возвращается к Gen2 под нагрузкой)\n", "[Gen2] TLS=Gen%d 但当前 Gen%d — 空闲省电降速(负载下自动回 Gen2)\n"), tls, cur)
		gen2Verdict = fmt.Sprintf(tr("🟢 Gen2 configured (TLS=Gen%d): current Gen%d is an idle power-saving downshift; returns to Gen2 under load", "🟢 Gen2 настроен (TLS=Gen%d): текущий Gen%d — понижение для экономии в простое; возвращается к Gen2 под нагрузкой", "🟢 Gen2 已配置(TLS=Gen%d): 当前 Gen%d 为空闲省电降速, 负载下自动回 Gen2"), tls, cur)
	default:
		fmt.Printf(tr("[Gen2] Still at Gen%d (TLS=Gen%d); unlock failed. Please send the logs.\n", "[Gen2] По-прежнему Gen%d (TLS=Gen%d); разблокировка не удалась. Пришлите журналы.\n", "[Gen2] 仍在 Gen%d (TLS=Gen%d), 解锁失败。请发日志。\n"), cur, tls)
		gen2Verdict = fmt.Sprintf(tr("❌ Gen2 failed: still at Gen%d (TLS=Gen%d; all PL0 registers OK but TLS did not stick, usually the driver/GSP holding the link policy — the logon task (if registered) automatically runs the Stage2 fallback; if the task is not registered it will not run automatically, so register it first and retry; see README §5.2)", "❌ Gen2 не удалось: по-прежнему Gen%d (TLS=Gen%d; все регистры PL0 OK, но TLS не закрепился, обычно драйвер/GSP удерживает политику канала — задача входа (если зарегистрирована) автоматически запускает откат Stage2; если задача не зарегистрирована, автоматически не запустится, сначала зарегистрируйте и повторите; см. README §5.2)", "❌ Gen2 失败: 仍在 Gen%d (TLS=Gen%d; PL0 全 OK 而 TLS 未粘住, 多为驱动/GSP 持有链路策略 — 登录任务(已注册)会自动执行 Stage2 回退; 任务未注册则不会自动跑, 先注册再重试; 详见 README §5.2)"), cur, tls)
	}
	// v2.6.0: 成功清掉遗留重试任务; 失败按策略安排自动重试(次数/间隔见 hxcore config)。
	// v3.0.1: 常驻守护模式不排一次性重试任务 — 守护进程每分钟自行重试。
	if unlocked {
		deleteGen2Retry()
	} else if hxcore.DriverStrategy() != hxcore.DriverStrategyResident {
		scheduleGen2Retry(retryDepth())
	}
	st := fmt.Sprintf(tr("Verdict: %s\nRunning as: %s\n40HX location: %02x:%02x.%x\nRoot Port: %02x:%02x.%x\n", "Заключение: %s\nЗапуск от: %s\n40HX расположение: %02x:%02x.%x\nRoot Port: %02x:%02x.%x\n", "结论: %s\n运行身份: %s\n40HX 位置: %02x:%02x.%x\nRoot Port: %02x:%02x.%x\n")+
		tr("Link: current Gen%d / target TLS=Gen%d\nDrivers: ThrottleStop=✓ WinRing0=✓ (BYOVD, unloaded when done)\n", "Канал: текущий Gen%d / цель TLS=Gen%d\nДрайверы: ThrottleStop=✓ WinRing0=✓ (BYOVD, выгружаются по завершении)\n", "链路: 当前 Gen%d / 目标 TLS=Gen%d\n驱动: ThrottleStop=✓ WinRing0=✓ (BYOVD, 用完即卸)\n"),
		gen2Verdict,
		map[bool]string{true: tr("administrator/SYSTEM", "администратор/SYSTEM", "管理员/SYSTEM"), false: tr("normal user (restricted)", "обычный пользователь (ограничено)", "普通用户(受限)")}[isAdmin()],
		gpuBus, (gpuBDF>>3)&0x1F, gpuBDF&7,
		(root>>8)&0xFF, (root>>3)&0x1F, root&7,
		cur, tls)
	if wErr := hxcore.WriteGen2Status(st); wErr != nil {
		fmt.Printf(tr("[Gen2] Failed to write the status file (does not affect the unlock): %v\n", "[Gen2] Не удалось записать файл состояния (не влияет на разблокировку): %v\n", "[Gen2] 状态文件写入失败(不影响解锁): %v\n"), wErr)
	}
	if !hasArg("-silent") && !hasArg("-y") {
		icon := uint(mbIconInfo)
		txt := fmt.Sprintf(tr("PCIe link: current Gen%d (target TLS=Gen%d)\n", "Канал PCIe: текущий Gen%d (цель TLS=Gen%d)\n", "PCIe 链路: 当前 Gen%d (目标 TLS=Gen%d)\n"), cur, tls)
		if unlocked {
			txt += tr("=== GEN2 UNLOCK SUCCEEDED ===", "=== РАЗБЛОКИРОВКА GEN2 УСПЕШНА ===", "=== GEN2 解锁成功 ===")
			if cur < 2 {
				txt += tr("\n(currently an idle power-saving downshift; returns to Gen2 under load)", "\n(сейчас понижение для экономии в простое; возвращается к Gen2 под нагрузкой)", "\n(当前为空闲省电降速, 负载下自动回 Gen2)")
			}
		} else {
			txt += tr("Still at Gen1; unlock failed (see the log for details).", "По-прежнему Gen1; разблокировка не удалась (подробности в журнале).", "仍在 Gen1, 解锁失败(详见日志)。")
			icon = mbIconError
		}
		msgbox("40HX Gen2", txt, icon)
	}
}

// ---------- v3.0.1: 常驻守护 (驱动策略=常驻时, 由登录任务 -guard 启动) ----------
// 每 1 分钟读 GPU 目标速率 TLS: TLS>=2 就不动(空闲降 Gen1 属正常省电);
// TLS 掉回 <2 = 解锁配置丢失(如显卡复位/驱动重载) → 自动重跑一次完整解锁。
// 进程随登录任务常驻; 注销/任务结束/卸载即停止。守护重试不排一次性重试任务。
const gen2GuardInterval = 1 * time.Minute

func residentGuard() {
	fmt.Println(tr("[Guard] Resident guard started: checks the Gen2 target (TLS) every minute and retrains automatically if the configuration is lost (TLS<2); stops on logoff or when the task ends.", "[Страж] Резидентный страж запущен: каждую минуту проверяет цель Gen2 (TLS) и автоматически переобучает при потере конфигурации (TLS<2); останавливается при выходе или завершении задачи.", "[守护] 常驻守护启动: 每 1 分钟检查 Gen2 目标(TLS), 配置丢失(TLS<2)自动重训; 注销或任务结束即停止。"))
	for {
		time.Sleep(gen2GuardInterval)
		st := hxcore.ReadUnlockStateV2(0, 0)
		if st.TLS >= 2 {
			continue // 目标仍在: 空闲降速属正常, 不动
		}
		fmt.Println(tr("[Guard] Detected TLS<2 — the Gen2 unlock configuration was lost; unlocking again automatically...", "[Страж] Обнаружено TLS<2 — конфигурация разблокировки Gen2 потеряна; повторная автоматическая разблокировка...", "[守护] 检测到 TLS<2 — Gen2 解锁配置丢失, 自动重新解锁..."))
		gen2Main()
	}
}

// ---------- Gen2 -hard 回退: Root Link Disable + PnP 恢复 (Stage 2) ----------
// 仅当显式 40HXInstaller.exe -gen2 -hard 时进入。普通/计划任务路径绝不触发,
// 因为 Root Link Disable 会让 nvidia-smi 短暂报 "GPU is lost"(链路瞬断+驱动重置)。
// 对齐社区 byovd.py(member573, issue #11, 2026-09-06 多卡实测):
//   retrain-only 在寨板/多卡不足 → Root Link Disable 循环(PL0+TLS 保持)使
//   LNKCAP.max=2 训上 Gen2 → PnP 禁用/启用 40HX 恢复 "GPU is lost" →
//   Retrain-ONLY(不再二次 LD, 保驱动健康) → 重启 NVDisplay.ContainerLocalSystem。
// 代码层无法判断"当前 Gen1 是空闲降速还是真训不上", 故 -hard 交给用户手动裁决。

func gen2WritePL0(th syscall.Handle, bar0Phys uint64) {
	pl0 := []struct {
		off  uint64
		val  uint32
		name string
	}{
		{0x8872C, 0x6, "XVE_OVR=6"},
		{0x8C040, 0x80085800, "LINK_CONFIG_0"},
		{0x8841C, 0xE0B42D00, "PRIV_MISC_1"},
		{0x8C2C0, 0x068731B3, "CYA_0"},
	}
	for _, p := range pl0 {
		if werr := hxcore.TSWrite(th, bar0Phys+p.off, p.val); werr != nil {
			fmt.Printf(tr("  [!] %s write failed: %v\n", "  [!] %s: ошибка записи: %v\n", "  [!] %s 写失败: %v\n"), p.name, werr)
			continue
		}
		if rb, rerr := hxcore.TSRead(th, bar0Phys+p.off); rerr != nil || rb != p.val {
			fmt.Printf(tr("  [warn] %s read back 0x%08x (expected 0x%08x)\n", "  [warn] %s: обратное чтение 0x%08x (ожидалось 0x%08x)\n", "  [warn] %s 读回 0x%08x (期望 0x%08x)\n"), p.name, rb, p.val)
		} else {
			fmt.Printf("  %s OK (0x%08X)\n", p.name, rb)
		}
	}
}

// 16-bit LNKCTL2 写 TLS(只读改写 bit3:0, 保留其余位)
func gen2SetTLS(wh syscall.Handle, bdf uint32, tls uint16) {
	if bdf == 0xFFFFFFFF {
		return
	}
	cap := hxcore.PcieCap(wh, bdf)
	if cap == 0 {
		return
	}
	cur, _ := hxcore.PciRd(wh, bdf, cap+0x30)
	nv := uint16(cur&0xFFF0) | (tls & 0xF)
	_ = hxcore.PciWr(wh, bdf, cap+0x30, []byte{byte(nv), byte(nv >> 8)})
	rb, _ := hxcore.PciRd(wh, bdf, cap+0x30)
	fmt.Printf(tr("    TLS=%d written to LNKCTL2 (0x%04X -> 0x%04X, read-back TLS=%d)\n", "    TLS=%d записан в LNKCTL2 (0x%04X -> 0x%04X, обратное чтение TLS=%d)\n", "    TLS=%d 写 LNKCTL2 (0x%04X -> 0x%04X, 回读 TLS=%d)\n"), tls, cur&0xFFFF, rb&0xFFFF, rb&0xF)
}

// 16-bit LNKCTL 脉冲 retrain(bit5)
func gen2RetrainPulse(wh syscall.Handle, bdf uint32) {
	if bdf == 0xFFFFFFFF {
		return
	}
	cap := hxcore.PcieCap(wh, bdf)
	if cap == 0 {
		return
	}
	ctl, _ := hxcore.PciRd(wh, bdf, cap+0x10)
	lo := uint16(ctl & 0xFFFF)
	buf := []byte{byte(lo & 0xFF), byte((lo >> 8) & 0xFF)}
	buf[0] &^= 0x20 // clear bit5
	_ = hxcore.PciWr(wh, bdf, cap+0x10, buf)
	time.Sleep(300 * time.Millisecond)
	ctl2, _ := hxcore.PciRd(wh, bdf, cap+0x10)
	lo2 := uint16(ctl2 & 0xFFFF)
	buf2 := []byte{byte(lo2 & 0xFF), byte((lo2 >> 8) & 0xFF)}
	buf2[0] |= 0x20 // set bit5
	_ = hxcore.PciWr(wh, bdf, cap+0x10, buf2)
}

// Root Link Disable 循环(PL0+TLS 保持) — 使 LNKCAP.max=2 训上 Gen2
func gen2RootLinkDisable(th syscall.Handle, wh *syscall.Handle, gpuBDF uint32, bar0Phys uint64, root uint32) {
	if root == 0xFFFFFFFF {
		fmt.Println(tr("    [warn] no root port; skipping Link Disable", "    [warn] нет корневого порта; пропуск Link Disable", "    [warn] 无 root port, 跳过 Link Disable"))
		return
	}
	cap := hxcore.PcieCap(*wh, root)
	if cap == 0 {
		fmt.Println(tr("    [warn] root has no PCIe cap; skipping Link Disable", "    [warn] у корневого порта нет возможности PCIe; пропуск Link Disable", "    [warn] root 无 PCIe cap, 跳过 Link Disable"))
		return
	}
	ctl, _ := hxcore.PciRd(*wh, root, cap+0x10)
	fmt.Printf("    ROOT Link Disable (ctl=0x%04X)\n", ctl&0xFFFF)
	lo := uint16(ctl & 0xFFFF)
	set := lo | 0x10 // bit4 = Link Disable
	_ = hxcore.PciWr(*wh, root, cap+0x10, []byte{byte(set), byte(set >> 8)})
	time.Sleep(500 * time.Millisecond)
	// PL0 + TLS 在 link down 期间保持
	gen2WritePL0(th, bar0Phys)
	gen2SetTLS(*wh, root, 2)
	gen2SetTLS(*wh, gpuBDF, 2)
	// clear bit4 → 重新训练
	ctl2, _ := hxcore.PciRd(*wh, root, cap+0x10)
	clr := uint16(ctl2&0xFFFF) &^ 0x10
	_ = hxcore.PciWr(*wh, root, cap+0x10, []byte{byte(clr), byte(clr >> 8)})
	time.Sleep(2000 * time.Millisecond)
}

// PnP 禁用/启用 40HX — 恢复 Link Disable 后的 "GPU is lost"(nvidia-smi/GPU-Z 断连)
func gen2PnpRecover40HX() bool {
	ps := `$iid=(Get-PnpDevice -Class Display | Where-Object { $_.InstanceId -match 'DEV_1F0B' } | Select-Object -First 1).InstanceId; ` +
		`if($iid){ Disable-PnpDevice -InstanceId $iid -Confirm:$false; Start-Sleep -Seconds 2; ` +
		`Enable-PnpDevice -InstanceId $iid -Confirm:$false; Start-Sleep -Seconds 4; Write-Output "PnP-OK $iid" } ` +
		`else { Write-Output 'PnP-NONE' }`
	out, err := exec.Command("powershell", "-NoProfile", "-Command", ps).CombinedOutput()
	fmt.Printf(tr("    PnP recovery: %s (err=%v)\n", "    Восстановление PnP: %s (err=%v)\n", "    PnP 恢复: %s (err=%v)\n"), strings.TrimSpace(string(out)), err)
	return err == nil && strings.Contains(string(out), "PnP-OK")
}

// 重启 NVDisplay 容器(恢复 GPU-Z / 任务管理器的显示, Link Disable 后常需)
func gen2RestartNVDisplay() {
	ps := `Restart-Service NVDisplay.ContainerLocalSystem -Force -ErrorAction SilentlyContinue; Start-Sleep -Seconds 2`
	out, err := exec.Command("powershell", "-NoProfile", "-Command", ps).CombinedOutput()
	fmt.Printf(tr("    NVDisplay container restart: %s (err=%v)\n", "    Перезапуск контейнера NVDisplay: %s (err=%v)\n", "    NVDisplay 容器重启: %s (err=%v)\n"), strings.TrimSpace(string(out)), err)
}

// -hard 回退中 PnP 导致 GPU reset, 旧 \\.\ThrottleStop / WinRing0 句柄可能失效 → 重开
func gen2ReopenDrivers(th, wh *syscall.Handle) bool {
	hxcore.CloseHandle(*th)
	hxcore.CloseHandle(*wh)
	ok := true
	if nt, e := hxcore.OpenThrottleStop(); e != nil {
		fmt.Printf(tr("    [!] ThrottleStop reopen failed: %v\n", "    [!] Не удалось повторно открыть ThrottleStop: %v\n", "    [!] ThrottleStop 重开失败: %v\n"), e)
		ok = false
	} else {
		*th = nt
	}
	if nw, e := hxcore.OpenDevice(`\\.\WinRing0_1_2_0`); e != nil {
		fmt.Printf(tr("    [!] WinRing0 reopen failed: %v\n", "    [!] Не удалось повторно открыть WinRing0: %v\n", "    [!] WinRing0 重开失败: %v\n"), e)
		ok = false
	} else {
		*wh = nw
	}
	return ok
}

// 恢复 GPU LNKCTL CCC(0x0140, Common Clock + Extended Synch) — 保 NVAPI/GPU-Z 健康
func gen2RestoreGPULnkctl(wh syscall.Handle, gpuBDF uint32) {
	cap := hxcore.PcieCap(wh, gpuBDF)
	if cap == 0 {
		return
	}
	ctl, _ := hxcore.PciRd(wh, gpuBDF, cap+0x10)
	cur := uint16(ctl & 0xFFFF)
	if (cur & 0x0140) != 0x0140 {
		want := (cur &^ 0x3) | 0x0140
		_ = hxcore.PciWr(wh, gpuBDF, cap+0x10, []byte{byte(want), byte(want >> 8)})
		rb, _ := hxcore.PciRd(wh, gpuBDF, cap+0x10)
		fmt.Printf(tr("    GPU LNKCTL restored 0x%04X -> 0x%04X\n", "    GPU LNKCTL восстановлен 0x%04X -> 0x%04X\n", "    GPU LNKCTL 恢复 0x%04X -> 0x%04X\n"), cur, rb&0xFFFF)
	}
}

// Stage2 编排: LD → retrain → PnP 恢复 → retrain-only → NVDisplay 重启。自行写结论。
func gen2HardFallback(th, wh *syscall.Handle, gpuBDF uint32, bar0Phys uint64, root uint32) {
	fmt.Println(tr("\n[Gen2 -hard] === Link Disable fallback path (Stage 2) ===", "\n[Gen2 -hard] === Путь отката Link Disable (Stage 2) ===", "\n[Gen2 -hard] === Link Disable 回退路径 (Stage 2) ==="))
	fmt.Println(tr("[Gen2 -hard] Warning: this path briefly makes nvidia-smi report 'GPU is lost' (a momentary link drop + driver reset),", "[Gen2 -hard] Предупреждение: этот путь на короткое время заставит nvidia-smi сообщить 'GPU is lost' (кратковременный разрыв канала + сброс драйвера),", "[Gen2 -hard] 警告: 此路径会让 nvidia-smi 短暂报 'GPU is lost'(链路瞬断+驱动重置),"))
	fmt.Println(tr("[Gen2 -hard] It recovers via PnP after a few seconds. Use it manually only when you have confirmed retrain-only cannot reach Gen2 on your hardware.", "[Gen2 -hard] Восстанавливается через PnP через несколько секунд. Используйте вручную только если убедились, что retrain-only не достигает Gen2 на вашем оборудовании.", "[Gen2 -hard] 约数秒后经 PnP 恢复。仅在你确认 retrain-only 在贵硬件训不上时手动使用。"))
	// 链路重置后 BAR 可能重映射 → 重新确认 BAR0
	if bar0raw, _ := hxcore.PciRd(*wh, gpuBDF, 0x10); bar0raw != 0 && bar0raw != 0xFFFFFFFF {
		bar0Phys = uint64(bar0raw & 0xFFFFFFF0)
	}
	fmt.Printf("[Gen2 -hard] BAR0 = 0x%08X\n", bar0Phys)
	didLD := false
	// 1. re-assert PL0
	gen2WritePL0(*th, bar0Phys)
	// 2. Root Link Disable 循环
	if root != 0xFFFFFFFF {
		gen2RootLinkDisable(*th, wh, gpuBDF, bar0Phys, root)
		didLD = true
	}
	cur := hxcore.LinkSpeed(*wh, gpuBDF)
	fmt.Printf(tr("[Gen2 -hard] After Link Disable: Gen%d\n", "[Gen2 -hard] После Link Disable: Gen%d\n", "[Gen2 -hard] Link Disable 后: Gen%d\n"), cur)
	// 3. 仍 Gen1 → retrain 脉冲(交替) 最多 6 轮
	if cur < 2 {
		for i := 0; i < 6; i++ {
			// 社区 byovd.py: retrain 第 3 轮(attempts==2)仍 Gen1 再走一次 Link Disable 循环
			if i == 2 && cur < 2 && root != 0xFFFFFFFF {
				fmt.Println(tr("[Gen2 -hard] retrain still failing → a second Link Disable cycle", "[Gen2 -hard] переобучение всё ещё не удаётся → второй цикл Link Disable", "[Gen2 -hard] retrain 仍失败 → 二次 Link Disable 循环"))
				gen2RootLinkDisable(*th, wh, gpuBDF, bar0Phys, root)
			}
			gen2WritePL0(*th, bar0Phys)
			gen2SetTLS(*wh, root, 2)
			gen2SetTLS(*wh, gpuBDF, 2)
			bdf := gpuBDF
			tag := "GPU"
			if root != 0xFFFFFFFF && i%2 == 0 {
				bdf, tag = root, "ROOT"
			}
			fmt.Printf("[Gen2 -hard] retrain #%d (%s)...\n", i+1, tag)
			gen2RetrainPulse(*wh, bdf)
			time.Sleep(2200 * time.Millisecond)
			cur = hxcore.LinkSpeed(*wh, gpuBDF)
			if cur >= 2 {
				break
			}
		}
	}
	// 4. PnP 恢复 "GPU is lost" + retrain-only + NVDisplay 重启
	// v2.6.0: 只要做过 Link Disable 就无条件恢复(此前仅成功时恢复 —
	// 失败时 GPU 悬在 lost 态, 用户只能设备管理器手动禁用/启用, 社区抱怨来源之一)
	if didLD {
		if cur >= 2 {
			fmt.Println(tr("[Gen2 -hard] Reached Gen2; running PnP recovery + NVDisplay restart", "[Gen2 -hard] Достигнут Gen2; выполняется восстановление PnP + перезапуск NVDisplay", "[Gen2 -hard] 已训上 Gen2, 执行 PnP 恢复 + NVDisplay 重启"))
		} else {
			fmt.Println(tr("[Gen2 -hard] Training did not succeed; still running PnP recovery to return the GPU to a normal state", "[Gen2 -hard] Обучение не удалось; всё равно выполняется восстановление PnP, чтобы вернуть GPU в нормальное состояние", "[Gen2 -hard] 训练未成, 仍执行 PnP 恢复确保 GPU 回到正常状态"))
		}
		gen2PnpRecover40HX()
		if !gen2ReopenDrivers(th, wh) {
			fmt.Println(tr("[Gen2 -hard][!] Driver reopen failed; aborting the rest of the recovery", "[Gen2 -hard][!] Не удалось повторно открыть драйвер; прерывание оставшегося восстановления", "[Gen2 -hard][!] 驱动重开失败, 中止后续恢复"))
			gen2VerdictHard(gpuBDF, cur, false)
			return
		}
		time.Sleep(3000 * time.Millisecond)
		// retrain-only(不再二次 LD)
		if bar0raw, _ := hxcore.PciRd(*wh, gpuBDF, 0x10); bar0raw != 0 && bar0raw != 0xFFFFFFFF {
			bar0Phys = uint64(bar0raw & 0xFFFFFFF0)
		}
		gen2WritePL0(*th, bar0Phys)
		gen2SetTLS(*wh, root, 2)
		gen2SetTLS(*wh, gpuBDF, 2)
		for i := 0; i < 6; i++ {
			cur = hxcore.LinkSpeed(*wh, gpuBDF)
			if cur >= 2 {
				break
			}
			bdf := gpuBDF
			if root != 0xFFFFFFFF && i%2 == 0 {
				bdf = root
			}
			gen2RetrainPulse(*wh, bdf)
			time.Sleep(2200 * time.Millisecond)
		}
		gen2RestoreGPULnkctl(*wh, gpuBDF)
		gen2RestartNVDisplay()
		cur = hxcore.LinkSpeed(*wh, gpuBDF)
		fmt.Printf(tr("[Gen2 -hard] After PnP recovery: Gen%d\n", "[Gen2 -hard] После восстановления PnP: Gen%d\n", "[Gen2 -hard] PnP 恢复后: Gen%d\n"), cur)
	}
	gen2VerdictHard(gpuBDF, cur, cur >= 2)
}

func gen2VerdictHard(gpuBDF uint32, cur uint32, success bool) {
	// v2.6.0: 与 Stage1 verdict 同一套重试策略 — 成功清重试任务, 失败按预算再排
	if success {
		deleteGen2Retry()
	} else {
		scheduleGen2Retry(retryDepth())
	}
	gpuBus := (gpuBDF >> 8) & 0xFF
	st := fmt.Sprintf(tr("Verdict (Link Disable fallback): %s\n40HX location: %02x:%02x.%x\nLink: current Gen%d\nDrivers: ThrottleStop=✓ WinRing0=✓ (BYOVD, finalized per policy)\n", "Заключение (откат Link Disable): %s\n40HX расположение: %02x:%02x.%x\nКанал: текущий Gen%d\nДрайверы: ThrottleStop=✓ WinRing0=✓ (BYOVD, завершено по политике)\n", "结论(Link Disable 回退): %s\n40HX 位置: %02x:%02x.%x\n链路: 当前 Gen%d\n驱动: ThrottleStop=✓ WinRing0=✓ (BYOVD, 按策略收尾)\n"),
		map[bool]string{true: tr("✅ Gen2 succeeded", "✅ Gen2 успешно", "✅ Gen2 成功"), false: tr("❌ Gen2 failed (see the log / report to the community)", "❌ Gen2 не удалось (см. журнал / сообщите сообществу)", "❌ Gen2 失败(见日志/发社区)")}[success],
		gpuBus, (gpuBDF>>3)&0x1F, gpuBDF&7, cur)
	if wErr := hxcore.WriteGen2Status(st); wErr != nil {
		fmt.Printf(tr("[Gen2 -hard] Status write failed: %v\n", "[Gen2 -hard] Не удалось записать состояние: %v\n", "[Gen2 -hard] 状态写入失败: %v\n"), wErr)
	}
	if !hasArg("-silent") && !hasArg("-y") {
		icon := uint(mbIconInfo)
		txt := fmt.Sprintf(tr("Gen2 fallback: current Gen%d\n", "Откат Gen2: текущий Gen%d\n", "Gen2 回退: 当前 Gen%d\n"), cur)
		if success {
			txt += tr("=== GEN2 UNLOCK SUCCEEDED ===", "=== РАЗБЛОКИРОВКА GEN2 УСПЕШНА ===", "=== GEN2 解锁成功 ===")
		} else {
			txt += tr("Still at Gen1; the fallback did not succeed (see the log for details).", "По-прежнему Gen1; откат не удался (подробности в журнале).", "仍在 Gen1, 回退未成(详见日志)。")
			icon = mbIconError
		}
		msgbox("40HX Gen2", txt, icon)
	}
}

// ---------- v2.6.0: Gen2 自动重试 + Stage2 自动回退开关 + 策略配置 ----------

// retryDepth: 当前自动重试深度(-retrydepth=N, 0=登录任务首次执行)
func retryDepth() int {
	for _, a := range os.Args {
		if strings.HasPrefix(a, "-retrydepth=") {
			if n, err := strconv.Atoi(strings.TrimPrefix(a, "-retrydepth=")); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

// gen2AutoHardEnabled: Stage2(Link Disable + PnP 恢复)自动执行开关, 默认开。
// 关闭: reg add HKLM\SOFTWARE\40HXUnlock /v Gen2AutoHard /t REG_DWORD /d 0 /f
// (40HX 是唯一显示卡、不希望登录后链路瞬断数秒黑屏的用户可关)
func gen2AutoHardEnabled() bool {
	return hxcore.ConfigInt("Gen2AutoHard", 1) != 0
}

// scheduleGen2Retry: 失败后安排一次性自动重试(SYSTEM, 静默, 默认 15 分钟后)。
// 覆盖"开机后驱动/GSP 就绪慢""链路状态恰好卡住"等时序类失败(社区 #12);
// depth 为已重试次数, 超出策略预算(Gen2RetryCount)即不再排; 成功路径 deleteGen2Retry。
func scheduleGen2Retry(depth int) {
	count, interval := hxcore.Gen2RetryPolicy()
	if depth >= count {
		fmt.Printf(tr("[Gen2] Automatic-retry budget exhausted (%d/%d); will try again at the next logon\n", "[Gen2] Бюджет автоповтора исчерпан (%d/%d); повтор при следующем входе\n", "[Gen2] 自动重试预算已用完(%d/%d), 等下次登录再试\n"), depth, count)
		return
	}
	t := time.Now().Add(time.Duration(interval) * time.Minute)
	if t.Day() != time.Now().Day() {
		fmt.Println(tr("[Gen2] Near midnight; skipping this retry scheduling (a once task is unreliable across a date change)", "[Gen2] Близко к полуночи; пропуск планирования этого повтора (задача once ненадёжна при смене даты)", "[Gen2] 接近零点, 跳过本次重试排程(once 任务跨日期不可靠)"))
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	abs, _ := filepath.Abs(exe)
	out, err := hxcore.RunOut("schtasks.exe", "/create", "/tn", gen2RetryTask,
		"/tr", fmt.Sprintf("\"%s\" -gen2 -silent -retrydepth=%d", abs, depth+1),
		"/sc", "once", "/st", t.Format("15:04"), "/ru", "SYSTEM", "/f")
	if err != nil {
		fmt.Printf(tr("[Gen2] Failed to create the retry task (does not affect the unlock): %s\n", "[Gen2] Не удалось создать задачу повтора (на разблокировку не влияет): %s\n", "[Gen2] 重试任务创建失败(不影响解锁): %s\n"), strings.TrimSpace(out))
		return
	}
	fmt.Printf(tr("[Gen2] Scheduled an automatic retry in %d minutes (%d/%d, task %s)\n", "[Gen2] Запланирован автоповтор через %d мин (%d/%d, задача %s)\n", "[Gen2] 已安排 %d 分钟后自动重试(%d/%d, 任务 %s)\n"), interval, depth+1, count, gen2RetryTask)
}

// deleteGen2Retry: Gen2 达成后清掉可能存在的重试任务
func deleteGen2Retry() {
	hxcore.RunOut("schtasks.exe", "/delete", "/tn", gen2RetryTask, "/f")
}

// gen2AcquireSingleInstance: v2.6.0 单实例保护。
// 返回 (是否取得独占, 释放函数)。未取得 = 已有别的实例在跑, 调用方应直接退出。
// 用内核全局互斥体 Global\40HXGen2SingleInstance: 跨用户/会话可见, 进程崩溃内核自动
// 释放, 比文件锁更可靠(文件锁挡不住两个进程同时 sc start 同一服务名)。
func gen2AcquireSingleInstance() (bool, func()) {
	name, _ := windows.UTF16PtrFromString("Global\\40HXGen2SingleInstance")
	h, err := windows.CreateMutex(nil, true, name)
	if err != nil {
		// 拿不到互斥体 → 放行(宁可多跑一次, 不漏解锁)
		fmt.Println(tr("[Gen2] Failed to create the single-instance mutex; proceeding anyway:", "[Gen2] Не удалось создать мьютекс единственного экземпляра; продолжаем:", "[Gen2] 单实例互斥体创建失败, 放行:"), err)
		return true, func() {}
	}
	if windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
		_ = windows.CloseHandle(windows.Handle(h))
		return false, nil
	}
	return true, func() {
		_ = windows.ReleaseMutex(windows.Handle(h))
		_ = windows.CloseHandle(windows.Handle(h))
	}
}

// waitForNvDriver: v2.6.0 时序保护 — 必须等 nvlddmkm 真正 RUNNING 后再动 GPU。
// 抢在 nv 驱动初始化前 retrain 会被 nv 起来后重置 PCIe 链路 / 覆盖 GPU 寄存器,
// 既冲掉 Gen2, 又可能触发 code19(安装器注释 §785 已实证)。服务不存在(nv 未装)则
// 直接放行; 超时(60s)仍继续, 不阻塞解锁。
func waitForNvDriver(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		out, _ := hxcore.RunOut("sc.exe", "query", "nvlddmkm")
		if strings.Contains(out, "does not exist") || strings.Contains(out, "未安装") ||
			strings.Contains(out, "1060") {
			fmt.Println(tr("[Gen2] nvlddmkm service not found; skipping the wait and unlocking directly", "[Gen2] Служба nvlddmkm не найдена; пропуск ожидания и прямая разблокировка", "[Gen2] 未检测到 nvlddmkm 服务, 跳过等待直接解锁"))
			return true
		}
		if strings.Contains(out, "RUNNING") {
			return true
		}
		if time.Now().After(deadline) {
			fmt.Printf(tr("[Gen2] nvlddmkm did not reach RUNNING within %s (see the log); continuing the unlock anyway\n", "[Gen2] nvlddmkm не перешёл в RUNNING за %s (см. журнал); разблокировка продолжается\n", "[Gen2] nvlddmkm 在 %s 内未进入 RUNNING(详见日志), 仍继续解锁\n"), timeout)
			return false
		}
		fmt.Println(tr("[Gen2] Waiting for nvlddmkm to become ready...", "[Gen2] Ожидание готовности nvlddmkm...", "[Gen2] 等待 nvlddmkm 就绪..."))
		time.Sleep(2 * time.Second)
	}
}

// cleanupByovd: v2.5 用完即卸 — 停止并删除 ThrottleStop/WinRing0 服务与驱动文件。
// 在 gen2Main 末尾(defers)执行, 游戏时系统无第三方驱动残留。
// v2.6.0: 尊重驱动运行策略 — 常驻策略保留服务与文件(GUI 有反作弊风险提示)。
func cleanupByovd() {
	if hxcore.DriverStrategy() == hxcore.DriverStrategyResident {
		fmt.Println(tr("[Gen2] Resident policy: keeping the driver services and files (removable via the GUI / uninstaller)", "[Gen2] Резидентная политика: службы и файлы драйверов сохранены (удаляются через GUI / деинсталлятор)", "[Gen2] 常驻策略: 保留驱动服务与文件(GUI/卸载器可移除)"))
		return
	}
	appRunning := throttleStopAppRunning()
	for _, d := range []struct{ name, file string }{
		{"ThrottleStop", "ThrottleStop.sys"},
		{"WinRing0_1_2_0", "WinRing0x64.sys"},
	} {
		// 本机 ThrottleStop 软件正在用该驱动 → 不删(避免打断用户软件);
		// 否则保持"用完即卸": 停服务 + 删服务 + 删文件 → 内核无驻留、磁盘无残留,
		// 反作弊(尤其 Vanguard 类的磁盘扫描)不会在游戏时扫到 vulnerable 驱动。
		if appRunning {
			continue
		}
		hxcore.RunOut("sc.exe", "stop", d.name)
		hxcore.RunOut("sc.exe", "delete", d.name)
		os.Remove(filepath.Join(os.Getenv("SystemRoot")+"\\System32\\drivers", d.file))
	}
}

// gen2Notify: 失败提示; 静默模式不弹框
func gen2Notify(txt string) {
	if !hasArg("-silent") && !hasArg("-y") {
		msgbox("40HX Gen2", txt, mbIconError)
	}
}

// gen2StatusFail: v2.4.6 — 把 Gen2 未执行/失败的原因写入状态文件,
// 供 40HXCheck 展示(SYSTEM 任务在 Session 0 无法弹窗给用户看)。
func gen2StatusFail(reason string) {
	ident := map[bool]string{true: tr("administrator/SYSTEM", "администратор/SYSTEM", "管理员/SYSTEM"), false: tr("normal user (restricted)", "обычный пользователь (ограничено)", "普通用户(受限)")}[isAdmin()]
	hxcore.WriteGen2Status(tr("❌ Gen2 not run: ", "❌ Gen2 не выполнено: ", "❌ Gen2 未执行: ") + reason + tr("\nRunning as: ", "\nЗапуск от: ", "\n运行身份: ") + ident + "\n")
}

// ===================== 卸载/状态 =====================

func uninstall() {
	if !isAdmin() {
		fmt.Println(tr("[!] Administrator rights are required.", "[!] Требуются права администратора.", "[!] 需要管理员权限。"))
		msgbox(tr("40HX Installer", "Установщик 40HX", "40HX 安装器"), tr("Administrator rights are required.\nRight-click this program -> Run as administrator.", "Требуются права администратора.\nЩёлкните программу правой кнопкой -> Запуск от имени администратора.", "需要管理员权限。\n请右键本程序 -> 以管理员身份运行。"), mbIconError)
		return
	}
	if lockOnce(`Local\40HXUninstaller_v1`) == nil {
		msgbox(tr("40HX Installer", "Установщик 40HX", "40HX 安装器"), tr("The uninstaller is already running; please do not click again.", "Деинсталлятор уже запущен, не нажимайте повторно.", "卸载程序已在运行, 请勿重复点击。"), mbIconInfo)
		return
	}
	// v2.6.0 修复: 全部走 hxcore 组件实现(与 40HXUninstaller.exe / GUI 页④ 同源)。
	// 此前内置版只删 Run键+任务+两个旧服务, 且固件启动项用 /enum {fwbootmgr}
	// 定位 — 该段输出没有各启动项 description, "40HX Unlock" 永不匹配 →
	// 启动项删不掉, EFI/GSP/驱动文件也全残留, 卸载后开机仍会执行解锁。
	fmt.Println(tr("=== Uninstall 40HX Unlock ("+hxcore.Version+", component-level) ===", "=== Удаление разблокировки 40HX ("+hxcore.Version+", покомпонентно) ===", "=== 卸载 40HX 解锁 ("+hxcore.Version+" 组件级) ==="))
	fmt.Print(tr("[1/8] Removing scheduled tasks ... ", "[1/8] Удаление задач планировщика ... ", "[1/8] 删除计划任务 ... "))
	if rem := hxcore.UninstallTasks(); len(rem) > 0 {
		fmt.Println(tr("Done", "Готово", "完成"))
	} else {
		fmt.Println(tr("not found (skipped)", "не найдено (пропущено)", "未找到(跳过)"))
	}
	fmt.Print(tr("[2/8] Removing the Gen2 Run key ... ", "[2/8] Удаление ключа Run для Gen2 ... ", "[2/8] 删除 Gen2 Run 键 ... "))
	hxcore.UninstallRunKey()
	fmt.Println(tr("Done", "Готово", "完成"))
	fmt.Print(tr("[3/8] Removing the firmware boot entry '40HX Unlock' ... ", "[3/8] Удаление записи загрузки прошивки '40HX Unlock' ... ", "[3/8] 删除固件启动项 '40HX Unlock' ... "))
	if hxcore.UninstallBootEntry() {
		fmt.Println(tr("Done", "Готово", "完成"))
	} else {
		fmt.Println(tr("not found (may already be removed)", "не найдено (возможно, уже удалено)", "未找到(可能已移除)"))
	}
	fmt.Print(tr("[4/8] Removing the unlock EFI from the ESP ... ", "[4/8] Удаление разблокировочного EFI с ESP ... ", "[4/8] 删除 ESP 解锁 EFI ... "))
	if hxcore.UninstallEspEfi() {
		fmt.Println(tr("Done", "Готово", "完成"))
	} else {
		fmt.Println(tr("not found / skipped", "не найдено / пропущено", "未找到/跳过"))
	}
	fmt.Println(tr("[5/8] Stopping and removing driver services...", "[5/8] Остановка и удаление служб драйверов...", "[5/8] 停止并删除驱动服务..."))
	hxcore.UninstallDriverServices()
	fmt.Println(tr("[6/8] Removing driver files...", "[6/8] Удаление файлов драйверов...", "[6/8] 删除驱动文件..."))
	hxcore.UninstallDriverFiles()
	fmt.Print(tr("[6.5/8] Removing EnableGpuFirmware (restoring the GSP default: off) ... ", "[6.5/8] Удаление EnableGpuFirmware (возврат GSP к значению по умолчанию: выкл) ... ", "[6.5/8] 删除 EnableGpuFirmware (恢复 GSP 默认关) ... "))
	if hxcore.UninstallGspKey() {
		fmt.Println(tr("Done", "Готово", "完成"))
	} else {
		fmt.Println(tr("not found (skipped)", "не найдено (пропущено)", "未找到(跳过)"))
	}
	fmt.Print(tr("[6.6/8] Cleaning ProgramData + policy key ... ", "[6.6/8] Очистка ProgramData + ключа политики ... ", "[6.6/8] 清理 ProgramData + 策略键 ... "))
	hxcore.UninstallProgramData()
	fmt.Println(tr("Done", "Готово", "完成"))
	fmt.Print(tr("[6.7/8] Cleaning Defender exclusions ... ", "[6.7/8] Очистка исключений Defender ... ", "[6.7/8] 清理 Defender 排除项 ... "))
	if err := hxcore.RemoveDefenderExclusions(); err != nil {
		fmt.Println(tr("not performed (ignorable):", "не выполнено (можно игнорировать):", "未执行(可忽略):"), err)
	} else {
		fmt.Println(tr("Done", "Готово", "完成"))
	}
	fmt.Println(tr("[7/8] Checking for leftovers...", "[7/8] Проверка остатков...", "[7/8] 检查残留..."))
	left := hxcore.CheckLeftover()
	fmt.Println()
	fmt.Println(tr("Uninstall complete. A reboot is recommended.", "Удаление завершено. Рекомендуется перезагрузка.", "卸载完成。建议重启电脑。"))
	fmt.Println(tr("  Note: the power settings changed at install time (Fast Startup / ASPM) are left as-is; see README §2.4 to restore them.", "  Примечание: настройки электропитания, изменённые при установке (быстрый запуск / ASPM), оставлены без изменений; способ восстановления см. в README §2.4.", "  注: 安装时调整的电源设置(快速启动/ASPM)保留未动 — 恢复方法见 README §2.4。"))
	icon := uint(mbIconInfo)
	txt := tr("Uninstall complete.\nA reboot is recommended.\n\nNote: the power settings changed at install time (Fast Startup / ASPM)\nare left as-is — they are power preferences; see README §2.4 to restore them.\n", "Удаление завершено.\nРекомендуется перезагрузка.\n\nПримечание: настройки электропитания, изменённые при установке (быстрый запуск / ASPM)\nоставлены без изменений — это ваши предпочтения питания; способ восстановления см. в README §2.4.\n", "卸载完成。\n建议重启电脑。\n\n注: 安装时调整的电源设置(快速启动/ASPM)\n保留未动 — 属电源偏好, 恢复方法见 README §2.4。\n")
	if len(left) > 0 {
		icon = mbIconError
		txt += tr("\nLeftovers remain:\n", "\nОстались следы:\n", "\n仍有残留:\n") + strings.Join(left, "\n")
	}
	txt += tr("\nDetailed log: ", "\nПодробный журнал: ", "\n详细日志: ") + filepath.Join(os.TempDir(), "40HX_installer.log")
	msgbox(tr("40HX Installer", "Установщик 40HX", "40HX 安装器"), txt, icon)
}

func status() {
	fmt.Println(tr("=== 40HX unlock status ===", "=== Статус разблокировки 40HX ===", "=== 40HX 解锁状态 ==="))
	gpuOK := hxcore.FindGPU()
	sb := hxcore.SecureBootOn()
	ts := hxcore.TestSigningOn()
	gs := hxcore.GspEnabled()
	fmt.Printf(tr("GPU 40HX detected: %v\n", "Обнаружение GPU 40HX: %v\n", "GPU 40HX 检测: %v\n"), gpuOK)
	fmt.Printf("Secure Boot: %v\n", sb)
	fmt.Printf(tr("Test signing: %v\n", "Тестовая подпись: %v\n", "测试签名: %v\n"), ts)
	fmt.Printf(tr("GSP enabled (EnableGpuFirmware=1): %v\n", "GSP включён (EnableGpuFirmware=1): %v\n", "GSP 启用 (EnableGpuFirmware=1): %v\n"), gs)
	// v2.4.1: GSP 定位诊断 — 伪装/魔改驱动会 AdapterString≠"CMP 40HX"
	if sub, adapter, fw := hxcore.GspDiag(); sub != "" {
		fmt.Printf(tr("  GSP key: Class\\%s (fw=%d)\n", "  Ключ GSP: Class\\%s (fw=%d)\n", "  GSP 键: Class\\%s (fw=%d)\n"), sub, fw)
		fmt.Printf("  AdapterString: %s\n", adapter)
	} else {
		fmt.Println("  [!] " + adapter) // 无匹配时 hxcore.GspDiag 返回诊断串
	}
	// v2.6.x: Gen2 驱动部署状态(不依赖驱动当前是否运行 — S0 用完即卸后
	// System32 文件缺失属正常终态; 判据是备份源/服务/Defender, 见 hxcore/drvstate.go)
	dep := hxcore.InspectGen2Drivers()
	if !hxcore.Gen2DriversDeployedOnce() {
		fmt.Println(tr("Gen2 drivers: never deployed — run the installer (tick drivers on page ②), then reboot to take effect", "Драйверы Gen2: никогда не разворачивались — запустите установщик (отметьте драйверы на стр. ②), затем перезагрузите", "Gen2 驱动: 从未部署 — 运行安装器(页②勾选驱动)后重启生效"))
	} else {
		for _, d := range dep {
			svcS := tr("not registered", "не зарегистрирована", "未注册")
			if d.SvcReg {
				svcS = d.SvcStart
				if d.SvcRunning {
					svcS += tr("/running", "/работает", "/运行中")
				}
			}
			fmt.Printf(tr("Gen2 driver %-16s backup=%v  System32=%s  service=%s\n", "Драйвер Gen2 %-16s резерв=%v  System32=%s  служба=%s\n", "Gen2 驱动 %-16s 备份源=%v  System32=%s  服务=%s\n"),
				d.File, map[bool]string{true: "OK", false: tr("none", "нет", "无")}[d.BackupOK], d.SysState.String(), svcS)
		}
	}
	if ex, err := hxcore.DefenderExclusionsPresent(); err != nil {
		fmt.Println(tr("Defender exclusions: query failed (", "Исключения Defender: сбой запроса (", "Defender 排除: 查询失败(") + err.Error() + ")")
	} else if ex {
		fmt.Println(tr("Defender exclusions: whitelisted (OK)", "Исключения Defender: в белом списке (OK)", "Defender 排除: 已加白(OK)"))
	} else {
		fmt.Println(tr("Defender exclusions: missing — antivirus may delete the drivers by mistake; re-run the installer to add them", "Исключения Defender: отсутствуют — антивирус может по ошибке удалить драйверы; перезапустите установщик, чтобы добавить их", "Defender 排除: 缺失 — 杀软可能误删驱动, 重跑安装器补加"))
	}
	// 驱动与解锁实测: 主判据 = 设备实际可打开(不依赖 sc.exe — 部分安全环境禁用它)
	// v2.5: TS(ThrottleStop) + WinRing0 BYOVD, 不再需要 40hx_bridge
	st := hxcore.ReadUnlockStateV2(5, 800)
	tsRun := st.TSOK
	winringRun := st.WinRingOK
	fmt.Printf("ThrottleStop: %v\n", tsRun)
	fmt.Printf("WinRing0: %v\n", winringRun)
	if tsRun && winringRun {
		fmt.Printf(tr("PCIe link: Gen%d\n", "Канал PCIe: Gen%d\n", "PCIe 链路: Gen%d\n"), st.Speed)
		if st.SS0OK {
			fmt.Printf(tr("SS0 (compute): 0x%08x %s\n", "SS0 (вычисления): 0x%08x %s\n", "SS0(算力): 0x%08x %s\n"), st.SS0, map[bool]string{true: tr("(unlocked)", "(разблокировано)", "(已解锁)"), false: tr("(locked)", "(заблокировано)", "(锁定)")}[st.Unlocked])
		}
	} else {
		fmt.Println(tr("Drivers not running (Gen2/status available once installed)", "Драйверы не запущены (Gen2/статус доступны после установки)", "驱动未运行(装好后 Gen2/状态可用)"))
	}
	ss0 := st.SS0
	ss0ok := st.SS0OK
	speed := st.Speed
	// v2.4: 弹窗带诊断与处置建议(社区用户不依赖日志)
	diag := []string{}
	if !gpuOK {
		diag = append(diag, tr("· 40HX not detected — make sure the card is seated and its driver is installed", "· 40HX не обнаружен — убедитесь, что карта установлена и её драйвер установлен", "· 未检测到 40HX —— 请确认显卡已插入且驱动已装"))
	}
	if sb {
		diag = append(diag, tr("· Secure Boot is on: disable it in the BIOS, otherwise the unlock EFI is rejected", "· Secure Boot включён: отключите его в BIOS, иначе разблокировочный EFI будет отклонён", "· Secure Boot 开启: 需进 BIOS 关闭, 否则解锁 EFI 被拒"))
	}
	if ts {
		diag = append(diag, tr("· Test signing is on — not needed in v2.5; turn it off with bcdedit /set testsigning off", "· Тестовая подпись включена — в v2.5 не нужна; отключите её командой bcdedit /set testsigning off", "· 测试签名已开启 — v2.5 不需要, 可 bcdedit /set testsigning off 关闭"))
	}
	if !gs {
		diag = append(diag, tr("· GSP not enabled: the screen may go black after unlocking. Run the installer (it sets EnableGpuFirmware=1 automatically)", "· GSP не включён: после разблокировки экран может погаснуть. Запустите установщик (он автоматически ставит EnableGpuFirmware=1)", "· GSP 未启用: 解锁后可能黑屏。运行安装器(自动设 EnableGpuFirmware=1)"))
	}
	if !tsRun || !winringRun {
		diag = append(diag, tr("· Drivers not running: they start automatically when you log in after a reboot; or run 40HXInstaller.exe -gen2 manually", "· Драйверы не запущены: они запустятся автоматически при входе после перезагрузки; либо запустите 40HXInstaller.exe -gen2 вручную", "· 驱动未运行: 重启后登录会自动拉起; 或手动运行 40HXInstaller.exe -gen2"))
	}
	if tsRun && winringRun {
		if !ss0ok {
			diag = append(diag, tr("· Drivers are running but the compute register cannot be read (abnormal)", "· Драйверы работают, но регистр вычислений не читается (аномалия)", "· 驱动已运行但读不到算力寄存器(异常)"))
		} else if ss0 == 0x88888888 {
			diag = append(diag, fmt.Sprintf(tr("· SS0=0x%08x: compute unlocked! PCIe Gen%d", "· SS0=0x%08x: вычисления разблокированы! PCIe Gen%d", "· SS0=0x%08x: 算力已解锁! PCIe Gen%d"), ss0, speed))
		} else {
			diag = append(diag, fmt.Sprintf(tr("· SS0=0x%08x: compute still locked — the 40HX Unlock EFI did not run successfully at boot", "· SS0=0x%08x: вычисления всё ещё заблокированы — разблокировочный EFI 40HX Unlock не выполнился при загрузке", "· SS0=0x%08x: 算力仍锁定 —— 重启时 40HX Unlock EFI 未成功执行"), ss0))
			// v2.4.4: 读 EFI 解锁日志(40hx_log.txt)做自动诊断, 不再需要人工看日志
			efiDiag := hxcore.AnalyzeEfiLog()
			if efiDiag != "" {
				diag = append(diag, efiDiag)
			}
		}
	}
	msg := tr("40HX unlock status\n========================\n", "Статус разблокировки 40HX\n========================\n", "40HX 解锁状态\n========================\n")
	msg += fmt.Sprintf("GPU 40HX: %v    Secure Boot: %v\n", map[bool]string{true: "✓", false: "✗"}[gpuOK], map[bool]string{true: tr("on!", "включён!", "开启!"), false: tr("off (OK)", "выкл (OK)", "关闭(OK)")}[sb])
	msg += fmt.Sprintf(tr("Test signing: %v    GSP: %v\n", "Тестовая подпись: %v    GSP: %v\n", "测试签名: %v    GSP: %v\n"), map[bool]string{true: "✓", false: "✗"}[ts], map[bool]string{true: "✓", false: "✗"}[gs])
	msg += fmt.Sprintf("ThrottleStop: %v  WinRing0: %v\n", map[bool]string{true: "✓", false: "✗"}[tsRun], map[bool]string{true: "✓", false: "✗"}[winringRun])
	if tsRun && winringRun {
		msg += fmt.Sprintf("PCIe: Gen%d    SS0: 0x%08x\n", speed, ss0)
	}
	msg += tr("\nDiagnostics:\n", "\nДиагностика:\n", "\n诊断:\n") + strings.Join(diag, "\n")
	if len(diag) == 0 {
		msg += tr("· Everything looks fine", "· Всё в порядке", "· 一切正常")
	}
	msg += tr("\n\nDetailed log: ", "\n\nПодробный журнал: ", "\n\n详细日志: ") + filepath.Join(os.TempDir(), "40HX_installer.log")
	msgbox(tr("40HX status", "Статус 40HX", "40HX 状态"), msg, mbIconInfo)
	fmt.Println(tr("=== status end ===", "=== конец статуса ===", "=== 状态结束 ==="))
}

func pause() {
	// GUI 版: 无需按 Enter; 输出已入日志, 交互收尾用消息框
}
