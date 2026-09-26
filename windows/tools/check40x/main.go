// 40HXCheck — CMP 40HX 解锁独立诊断工具 v2.0.1
//
// 双击即诊, 只读为主; v2.5 起若发现"算力/Gen2 无法实测(驱动未运行)"且驱动文件
// 在包内, 会临时拉起 ThrottleStop + WinRing0 实测后自清理(用完即卸, 保持无痕):
//
//	① 最优先显示: 算力解锁状态 + PCIe Gen2 状态
//	② 其次: GPU/Secure Boot/GSP/测试签名
//	③ 明细与建议
//
// 安装器(40HXInstaller)负责"装", 本工具负责"查"。
//
// 实现共享 tools/40hxcore (与安装器同一份探测/诊断代码, 不会漂移)。
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	hxcore "40hxcore"
	"golang.org/x/sys/windows"
)

const (
	logsDirName  = "40HXUnlock"              // %LOCALAPPDATA%\40HXUnlock\logs
	gen2TaskName = "40HX PCIe Gen2 Bring-up" // 与安装器 setupGen2Task 同名
)

// tr is a thin shim over the shared hxcore translator so call sites stay short.
func tr(en, ru, zh string) string { return hxcore.T(en, ru, zh) }

// appTitle is the message-box caption. It is a function, not a const, because the
// display language is only resolved at runtime (hxcore.InitLanguage in main).
func appTitle() string { return tr("CMP 40HX Unlock Diagnostics", "Диагностика разблокировки CMP 40HX", "CMP 40HX 解锁诊断") }

var (
	procMsgBoxW = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
)

// ---- v2.5: 临时驱动管理 (ThrottleStop + WinRing0, 用完即卸) ----
const (
	fileTS = "ThrottleStop.sys"
	fileWR = "WinRing0x64.sys"
	svcTS  = "ThrottleStop"
	svcWR  = "WinRing0_1_2_0"
)

func sysDrvDir() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	return filepath.Join(root, "System32", "drivers")
}

// driverSrcDir: 在包结构中定位 drivers/ (v2.5: exe 旁 gen2/drivers / ProgramData 备份)。
func driverSrcDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	pd := filepath.Join(os.Getenv("ProgramData"), "40HXUnlock", "drivers") // 安装器留下的备份源
	for _, c := range []string{
		pd,
		filepath.Join(dir, "gen2", "drivers"),
		filepath.Join(dir, "drivers"),
	} {
		if _, e1 := os.Stat(filepath.Join(c, fileTS)); e1 == nil {
			if _, e2 := os.Stat(filepath.Join(c, fileWR)); e2 == nil {
				return c
			}
		}
	}
	return ""
}

func svcState(name string) string {
	_, _, st := hxcore.ServiceInfo(name)
	return st
}

// throttleStopAppRunning: 本机是否正在运行 ThrottleStop 软件(同名驱动共存, 不删它的)。
func throttleStopAppRunning() bool {
	out, _ := hxcore.RunOut("tasklist.exe", "/fi", "imagename eq ThrottleStop.exe")
	return strings.Contains(out, "ThrottleStop.exe")
}

// ensureDrivers: 确保 TS/WinRing0 服务 RUNNING。已运行→不管(外部管理);
// 否则从包 drivers 部署+启动。
// 返回 (deployed 本工具是否部署/尝试拉起过, ok 是否两个都在运行, fail 拉起失败线索)。
// 杀软隔离常把 .sys 替换成 0 字节占位(文件仍在)→ 仅判"不存在"会漏, 故按
// 缺失/0字节自愈重部署; 重部署后补 Defender 排除防再删。
// 说明: "拉起"本身就是一次驱动加载测试 — 失败大多能归因(见 classifyLoadErr)。
func ensureDrivers() (deployed bool, ok bool, fail string) {
	src := driverSrcDir()
	allRunning := svcState(svcTS) == "RUNNING" && svcState(svcWR) == "RUNNING"
	if allRunning {
		return false, true, ""
	}
	if src == "" {
		return false, false, tr("no driver directory to load temporarily was found in the release package (gen2\\drivers)", "в пакете не найден каталог драйверов для временной загрузки (gen2\\drivers)", "发布包内未找到可临时拉起的驱动目录(gen2\\drivers)")
	}
	var fails []string
	for _, d := range []struct{ svc, file string }{
		{svcTS, fileTS}, {svcWR, fileWR},
	} {
		dst := filepath.Join(sysDrvDir(), d.file)
		if b, e := os.ReadFile(dst); e != nil || len(b) == 0 {
			if sb, e2 := os.ReadFile(filepath.Join(src, d.file)); e2 == nil {
				os.WriteFile(dst, sb, 0o644)
				_ = hxcore.AddDefenderExclusions() // best-effort 防再删
			}
		}
		if svcState(d.svc) == "RUNNING" {
			continue
		}
		deployed = true
		hxcore.RunOut("sc.exe", "create", d.svc, "type=", "kernel",
			"start=", "demand", "binPath=", `\SystemRoot\System32\drivers\`+d.file)
		if _, err := hxcore.RunOut("sc.exe", "start", d.svc); err != nil {
			// 服务可能被标记为删除(1072)/禁用(1058)→ 清标记后重建+启动一次
			// (对齐安装器 ensureSvcLoaded; 卸载残留态下诊断也能自愈加载)
			hxcore.RunOut("sc.exe", "delete", d.svc)
			hxcore.RunOut("sc.exe", "create", d.svc, "type=", "kernel",
				"start=", "demand", "binPath=", `\SystemRoot\System32\drivers\`+d.file)
			if out2, err2 := hxcore.RunOut("sc.exe", "start", d.svc); err2 != nil {
				fails = append(fails, d.file+": "+strings.TrimSpace(out2))
			}
		}
	}
	time.Sleep(400 * time.Millisecond)
	ok = svcState(svcTS) == "RUNNING" && svcState(svcWR) == "RUNNING"
	if !ok && len(fails) > 0 {
		fail = strings.Join(fails, " || ")
	}
	return deployed, ok, fail
}

// classifyLoadErr: 把"驱动拉起失败"的原始错误转成用户能懂的归因与处理。
func classifyLoadErr(raw string) string {
	r := strings.ToLower(raw)
	switch {
	case strings.Contains(r, "1275"):
		return tr("Windows security settings blocked the driver load (error 1275) — usually Defender Core Isolation / Memory Integrity, the Vulnerable Driver Blocklist, or Smart App Control. Temporarily turn these off in Windows Security and retry (you can turn them back on after loading).", "Настройки безопасности Windows заблокировали загрузку драйвера (ошибка 1275) — обычно это изоляция ядра / целостность памяти Defender, список блокировки уязвимых драйверов или Smart App Control. Временно отключите их в Центре безопасности Windows и повторите (после загрузки можно снова включить).", "Windows 安全设置阻止了驱动加载(错误 1275)——多为 Defender 的『内核隔离/内存完整性』、『易受攻击驱动程序阻止列表』或 Smart App Control 开启; 请到 Windows 安全中心临时关闭这些保护后重试(加载完可再开)")
	case strings.Contains(r, "577"):
		return tr("The driver image was rejected by the system (error 577) — the file was modified or a security policy blocked it. Re-run 40HXInstaller to redeploy the original driver, and check the untrusted-driver setting in Windows Security.", "Образ драйвера отклонен системой (ошибка 577) — файл изменен или заблокирован политикой безопасности. Перезапустите 40HXInstaller для повторного развертывания оригинального драйвера и проверьте параметр недоверенных драйверов в Центре безопасности.", "驱动映像被系统拒绝(错误 577)——文件被改动或安全策略拦截; 请重跑 40HXInstaller 重新部署原版驱动, 并检查安全中心的『不受信任驱动』设置")
	case strings.Contains(r, "1058"):
		return tr("The service is disabled (error 1058) — this tool already tried to re-enable and rebuild it", "Служба отключена (ошибка 1058) — инструмент уже попытался снова включить и пересоздать ее", "服务被禁用(错误 1058)——本工具已尝试改回并重建")
	case strings.Contains(r, "1072"):
		return tr("The service is in a marked-for-deletion leftover state (error 1072) — this tool already rebuilt and retried it", "Служба в состоянии \"помечена на удаление\" (ошибка 1072) — инструмент уже пересоздал ее и повторил попытку", "服务处于『标记删除』残留态(错误 1072)——本工具已重建重试")
	case strings.Contains(r, "拒绝访问"), strings.Contains(r, "access is denied"), strings.Contains(r, "error 5"), strings.Contains(r, " 5:"):
		return tr("Insufficient privileges (error 5) — run this tool as administrator", "Недостаточно прав (ошибка 5) — запустите инструмент от имени администратора", "权限不足(错误 5)——请以管理员身份运行本工具")
	case strings.Contains(r, "1060"), strings.Contains(r, "不存在"):
		return tr("Service not found (error 1060) — the driver file was not deployed successfully; re-run the installer and retry", "Служба не найдена (ошибка 1060) — файл драйвера не был развернут; перезапустите установщик и повторите", "服务未找到(错误 1060)——驱动文件未部署成功, 重跑安装器后重试")
	}
	return tr("Driver start failed — usually a third-party AV HIPS / driver block. Allow both .sys files in its trust/allow list and retry; if it still fails, send the logs to the author.", "Не удалось запустить драйвер — обычно из-за HIPS / блокировки драйверов стороннего антивируса. Добавьте оба файла .sys в доверенные/белый список и повторите; если не помогло, отправьте журналы автору.", "驱动启动失败——多因第三方杀软的 HIPS/驱动拦截, 请到其信任/白名单放行两个 .sys 后重试; 仍不行发日志给作者")
}

// cleanupDrivers: 自清理 — 停服务、删服务、删驱动文件(保持无痕, 不给反作弊留磁盘残留)。
// 本机 ThrottleStop 软件正在用该驱动时不删(避免打断用户软件)。
func cleanupDrivers() {
	if throttleStopAppRunning() {
		return
	}
	// 尊重驱动运行策略: "常驻"时本工具绝不卸; 其余(用完即卸/失败自动重试)正常自清理。
	switch hxcore.DriverStrategy() {
	case hxcore.DriverStrategyResident:
		fmt.Println(tr("  Resident strategy: keeping the driver service and files (diagnostics will not clean them up)", "  Резидентная стратегия: служба и файлы драйвера сохраняются (диагностика их не удаляет)", "  常驻策略: 保留驱动服务与文件(诊断不清理)"))
		return
	}
	for _, d := range []struct{ svc, file string }{
		{svcTS, fileTS}, {svcWR, fileWR},
	} {
		hxcore.RunOut("sc.exe", "stop", d.svc)
		hxcore.RunOut("sc.exe", "delete", d.svc)
		os.Remove(filepath.Join(sysDrvDir(), d.file))
	}
}

func msgbox(text string, icon uint) {
	t, _ := syscall.UTF16PtrFromString(appTitle())
	b, _ := syscall.UTF16PtrFromString(text)
	procMsgBoxW.Call(0, uintptr(unsafe.Pointer(b)), uintptr(unsafe.Pointer(t)), uintptr(icon))
}

// isAdmin: 与安装器同款实现 (TokenElevation 在受限环境可能误报 0, 再试 SCM 全权)
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
	}
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ALL_ACCESS)
	if err == nil {
		windows.CloseServiceHandle(scm)
		return true
	}
	return false
}

// selfElevate: 非管理员时 ShellExecute runas 提权重启(诊断要挂 ESP 读 40hx_log)
func selfElevate() {
	exe, _ := os.Executable()
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	args := append([]string{}, os.Args[1:]...)
	args = append(args, "-elevated")
	params, _ := syscall.UTF16PtrFromString(hxcore.JoinWindowsArgs(args))
	proc := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")
	r, _, _ := proc.Call(0,
		uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)), 0, 1)
	if r <= 32 {
		msgbox(tr("Administrator rights are required to read the EFI unlock log (40hx_log.txt).\nRight-click this program -> Run as administrator.", "Для чтения журнала разблокировки EFI (40hx_log.txt) нужны права администратора.\nЩелкните программу правой кнопкой -> Запуск от имени администратора.", "需要管理员权限才能读取 EFI 解锁日志(40hx_log.txt)。\n请右键本程序 -> 以管理员身份运行。"), 0x30)
	}
	os.Exit(0)
}

// logsDir: %LOCALAPPDATA%\40HXUnlock\logs (统一日志收集目录, 用户好找)
func logsDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	d := filepath.Join(base, logsDirName, "logs")
	os.MkdirAll(d, 0o755)
	return d
}

// collectLogs: 把相关日志汇集到固定目录, 返回目录路径
func collectLogs(diagSnapshot string) string {
	dir := logsDir()
	// 1. 安装器/Gen2 日志 (%TEMP%\40HX_installer.log)
	if b, err := os.ReadFile(filepath.Join(os.TempDir(), "40HX_installer.log")); err == nil {
		os.WriteFile(filepath.Join(dir, "installer.log"), b, 0o644)
	}
	// 2. EFI 解锁链日志 (ESP 根 40hx_log.txt) — 管理员下可读;
	//    v3.0.0: 仅当解锁 EFI 本体还在时才收集 — 卸载 EFI 后该文件是历史残留,
	//    拷进 logs 会在回溯时被误当"本次 EFI 运行日志"。
	if esp := hxcore.MountESP(); esp != "" {
		if _, efiErr := os.Stat(esp + `:\EFI\40HX\40HXUNLK.EFI`); efiErr == nil {
			if b, err := os.ReadFile(esp + ":\\40hx_log.txt"); err == nil {
				os.WriteFile(filepath.Join(dir, "40hx_log.txt"), b, 0o644)
			}
		}
		hxcore.UnmountESP(esp)
	}
	// 3. 本次诊断快照(最新) + 时间戳归档(保留历史便于对比)
	os.WriteFile(filepath.Join(dir, "diagnose.txt"), []byte(diagSnapshot), 0o644)
	ts := time.Now().Format("20060102_150405")
	os.WriteFile(filepath.Join(dir, "diagnose_"+ts+".txt"), []byte(diagSnapshot), 0o644)
	return dir
}

// indentLines: 多行文本统一加 4 空格缩进(状态文件内容展示用)
func indentLines(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, ln := range lines {
		lines[i] = "    " + ln
	}
	return strings.Join(lines, "\n")
}

// copyToClipboard: PowerShell Set-Clipboard(失败静默 — 仅增强, 不阻塞)
func copyToClipboard(s string) bool {
	f, err := os.CreateTemp("", "40hx_clip_*.txt")
	if err != nil {
		return false
	}
	p := f.Name()
	f.WriteString(s)
	f.Close()
	defer os.Remove(p)
	out, err := hxcore.RunOut("powershell.exe", "-NoProfile", "-Command",
		"Get-Content -LiteralPath '"+p+"' -Raw -Encoding UTF8 | Set-Clipboard")
	return err == nil && !strings.Contains(out, "denied")
}

// ---- v2.6.0: 加强诊断快照, 便于社区反馈定位 ----
// 环境/驱动/原始 PCIe 寄存器/已知限制 四段富文本, 写入 diagnose.txt 与剪贴板。

// osVersion: Windows 版本/构建号 (cmd /c ver)
func osVersion() string {
	out, _ := hxcore.RunOut("cmd.exe", "/c", "ver")
	out = strings.TrimSpace(out)
	if out == "" {
		out = tr("unknown", "неизвестно", "未知")
	}
	arch := os.Getenv("PROCESSOR_ARCHITECTURE")
	if arch == "" {
		arch = "?"
	}
	return fmt.Sprintf("%s [%s]", out, arch)
}

// driverDetail: 单驱动的部署详情 — 数据源 = hxcore.InspectGen2Drivers(),
// 与安装器 GUI 页① / -status 完全同一套状态判定(备份源 / System32 四态 /
// 服务注册与启动类型 / 运行态), 保证"诊断与安装器对同一台机器说法一致"。
func driverDetail(svc, file string) string {
	for _, d := range hxcore.InspectGen2Drivers() {
		if d.Service != svc || d.File != file {
			continue
		}
		sysS := tr("System32 missing", "System32 отсутствует", "System32缺失")
		switch d.SysState {
		case hxcore.DrvZero:
			sysS = tr("System32 0 bytes ⚠ AV quarantine placeholder", "System32 0 байт ⚠ заглушка карантина антивируса", "System32 0字节 ⚠杀软隔离占位")
		case hxcore.DrvSizeMismatch:
			sysS = fmt.Sprintf(tr("System32 size=%d bytes ⚠ differs from backup (replaced?)", "System32 размер=%d байт ⚠ не совпадает с резервной копией (заменен?)", "System32大小=%d字节 ⚠与备份不一致(被替换?)"), d.SysSize)
		case hxcore.DrvOk:
			sysS = fmt.Sprintf(tr("System32 size=%d bytes", "System32 размер=%d байт", "System32大小=%d字节"), d.SysSize)
		}
		svcS := tr("service not registered", "служба не зарегистрирована", "服务未注册")
		if d.SvcReg {
			svcS = tr("service ", "служба ", "服务") + d.SvcStart
			if d.SvcStart == "DISABLED" {
				svcS += tr(" ⚠ disabled (Gen2 cannot start; re-run the installer to fix)", " ⚠ отключена (Gen2 не запустится; перезапустите установщик для исправления)", " ⚠被禁用(Gen2 拉不起, 重跑安装器修复)")
			} else if d.SvcRunning {
				svcS += tr("/running", "/работает", "/运行中")
			} else {
				svcS += tr("(demand, waiting for the logon task to start it)", "(demand, ожидает запуска задачей входа)", "(demand, 待登录任务拉起)")
			}
		}
		backS := tr("no backup source (never installed)", "нет источника резервной копии (не устанавливался)", "无备份源(从未安装)")
		if d.BackupOK {
			backS = tr("backup source OK (deployed before)", "источник резервной копии OK (ранее развернут)", "备份源OK(曾部署)")
			if d.SysState == hxcore.DrvAbsent && !d.SvcReg {
				backS += tr("; transient auto-cleanup is normal, it will redeploy at next logon", "; авто-очистка (используй-и-удали) — это нормально, повторное развертывание при следующем входе", "; 用完即卸已自清理属正常, 下次登录自动重部署")
			}
		}
		return fmt.Sprintf("  %-16s %s | %s | %s\n", d.File, sysS, svcS, backS)
	}
	return fmt.Sprintf(tr("  %-16s (state detection not covered)\n", "  %-16s (определение состояния не выполнено)\n", "  %-16s (状态检测未覆盖)\n"), file)
}

// spdName: PCIe 链路速率编码 → 名称
func spdName(s uint32) string {
	names := []string{"?", "Gen1(2.5GT/s)", "Gen2(5GT/s)", "Gen3(8GT/s)", "Gen4(16GT/s)", "Gen5"}
	if s >= uint32(len(names)) {
		return "?"
	}
	return names[s]
}

// rebarSizeStr: BAR1 大小 (MiB) → 友好显示 ("8 GB" / "256 MB")。0 = 未读到。
func rebarSizeStr(mb uint32) string {
	if mb == 0 {
		return "?"
	}
	if mb >= 1024 && mb%1024 == 0 {
		return fmt.Sprintf("%d GB", mb/1024)
	}
	return fmt.Sprintf("%d MB", mb)
}

// rawPcieDump: 原始 PCIe 链路寄存器 + BAR0 BOOT_0 (Gen2 定位核心证据)
func rawPcieDump() string {
	wh, err := hxcore.OpenDevice(`\\.\WinRing0_1_2_0`)
	if err != nil {
		return tr("  (WinRing0 unavailable, skipping raw register read)\n", "  (WinRing0 недоступен, чтение сырых регистров пропущено)\n", "  (WinRing0 不可用, 跳过原始寄存器读取)\n")
	}
	defer hxcore.CloseHandle(wh)
	bdf, ok := hxcore.FindGPUPCI(wh)
	if !ok {
		return tr("  (FindGPUPCI did not locate the 40HX, skipping)\n", "  (FindGPUPCI не нашел 40HX, пропуск)\n", "  (FindGPUPCI 未定位 40HX, 跳过)\n")
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("  BDF=0x%05X (bus=%d dev=%d fn=%d)\n", bdf,
		(bdf>>8)&0xFF, (bdf>>3)&0x1F, bdf&0x7))
	cap := hxcore.PcieCap(wh, bdf)
	if cap == 0 {
		return sb.String() + tr("  (no PCIe Capability)\n", "  (нет PCIe Capability)\n", "  (无 PCIe Capability)\n")
	}
	rd := func(off uint32) uint32 {
		v, e := hxcore.PciRd(wh, bdf, off)
		if e != nil {
			return 0xFFFFFFFF
		}
		return v
	}
	lnkcap, lnkctl, lnksta := rd(cap+0x0C), rd(cap+0x10), rd(cap+0x12)
	lnkctl2, lnksta2 := rd(cap+0x30), rd(cap+0x32)
	sb.WriteString(fmt.Sprintf("  LNKCAP =0x%08X  MaxLinkSpeed=%s\n", lnkcap, spdName(lnkcap&0xF)))
	sb.WriteString(fmt.Sprintf("  LNKCTL =0x%08X (ASPM=%d RetrainLink=%d)\n", lnkctl, (lnkctl>>0)&3, (lnkctl>>5)&1))
	sb.WriteString(fmt.Sprintf(tr("  LNKSTA =0x%08X current=%s width=x%d\n", "  LNKSTA =0x%08X текущая=%s ширина=x%d\n", "  LNKSTA =0x%08X 当前=%s 宽度=x%d\n"), lnksta, spdName(lnksta&0xF), (lnksta>>4)&0x3F))
	sb.WriteString(fmt.Sprintf(tr("  LNKCTL2=0x%08X target=%s\n", "  LNKCTL2=0x%08X цель=%s\n", "  LNKCTL2=0x%08X 目标=%s\n"), lnkctl2, spdName(lnkctl2&0xF)))
	sb.WriteString(fmt.Sprintf("  LNKSTA2=0x%08X\n", lnksta2))
	// BAR0 BOOT_0 — 确认 BAR0 真指向 40HX MMIO
	if bar0raw, e := hxcore.PciRd(wh, bdf, 0x10); e == nil {
		bar0 := uint64(bar0raw & 0xFFFFFFF0)
		th, e2 := hxcore.OpenThrottleStop()
		if e2 == nil {
			defer hxcore.CloseHandle(th)
			if v, e3 := hxcore.TSRead(th, bar0+0x0); e3 == nil {
				fam := (v >> 24) & 0xFF
				tag := tr("unknown", "неизвестно", "未知")
				if fam == 0x16 {
					tag = "TU10x (40HX OK)"
				}
				sb.WriteString(fmt.Sprintf(tr("  BAR0+0x00 BOOT_0=0x%08X (family=0x%02X %s)\n", "  BAR0+0x00 BOOT_0=0x%08X (семейство=0x%02X %s)\n", "  BAR0+0x00 BOOT_0=0x%08X (家族=0x%02X %s)\n"), v, fam, tag))
			} else {
				sb.WriteString(fmt.Sprintf(tr("  BAR0+0x00 BOOT_0 read failed: %v\n", "  BAR0+0x00 BOOT_0 не прочитан: %v\n", "  BAR0+0x00 BOOT_0 读取失败: %v\n"), e3))
			}
			// XVE Resizable BAR Control (0x88bc0) — 当前 BAR1 大小与 size 编码,
			// 与 EFI/checker 主判定同一寄存器。size 字段 bits[13:8] = log2(MiB)。
			if rb, e4 := hxcore.TSRead(th, bar0+hxcore.XveRbarCtlOffset); e4 == nil {
				enc := (rb >> 8) & 0x3F
				var szMB uint32
				if enc < 32 {
					szMB = uint32(1) << enc
				}
				sb.WriteString(fmt.Sprintf(tr("  XVE ReBAR CTL=0x%08X sizeField=%d -> BAR1=%s\n", "  XVE ReBAR CTL=0x%08X поле размера=%d -> BAR1=%s\n", "  XVE ReBAR CTL=0x%08X 大小字段=%d -> BAR1=%s\n"), rb, enc, rebarSizeStr(szMB)))
			}
		} else {
			sb.WriteString(tr("  (ThrottleStop unavailable, skipping BOOT_0)\n", "  (ThrottleStop недоступен, BOOT_0 пропущен)\n", "  (ThrottleStop 不可用, 跳过 BOOT_0)\n"))
		}
	}
	return sb.String()
}

// knownIssuesBlock: 已知限制/潜在问题 — 社区反馈时对照
func knownIssuesBlock() string {
	var sb strings.Builder
	sb.WriteString(tr("\n--- Known limitations / potential issues ("+hxcore.Version+", not fully covered by the community) ---\n", "\n--- Известные ограничения / возможные проблемы ("+hxcore.Version+", не полностью проверено сообществом) ---\n", "\n--- 已知限制 / 潜在问题 ("+hxcore.Version+", 社区未全覆盖项) ---\n"))
	sb.WriteString(tr("· Driver/GSP holds a link policy (GSP-RM): at idle/low load nvlddmkm writes the GPU-side TLS back to Gen1; the automatic Stage2 fallback or LD (-hard) clears it. Confirmed **not firmware write-protection** (batch numbers .06/.04 are not a criterion); **do NOT flash the VBIOS** — if it still fails, send diagnostics and logs to the author.\n", "· Драйвер/GSP удерживает политику канала (GSP-RM): при простое/низкой нагрузке nvlddmkm возвращает TLS со стороны GPU в Gen1; автоматический откат Stage2 или LD (-hard) снимает это. Подтверждено, что это **не защита прошивки от записи** (номера партий .06/.04 не являются критерием); **НЕ прошивайте VBIOS** — если не помогло, отправьте диагностику и журналы автору.\n", "· 驱动/GSP 持有链路策略(GSP-RM): 空闲/低负载时 nvlddmkm 把 GPU 侧 TLS 回写 Gen1; Stage2 自动回退或 LD(-hard) 可解除。已确认**不是固件写保护**(批次号 .06/.04 不能当判据), **不要刷 VBIOS** — 仍失败发诊断与日志反馈作者。\n"))
	sb.WriteString(tr("· Off-brand boards / multi-GPU: retrain-only cannot reach Gen2 on some boards/topologies and needs the Root Link Disable fallback (a brief link drop). Since v2.6 the logon task automatically tries Stage2 once (Gen2AutoHard); if it still fails, run `40HXInstaller.exe -gen2 -hard` by hand.\n", "· Некачественные платы / несколько карт: retrain-only не достигает Gen2 на некоторых платах/топологиях и требует отката Root Link Disable (кратковременный разрыв канала). С v2.6 задача входа автоматически один раз пробует Stage2 (Gen2AutoHard); если не помогло, выполните `40HXInstaller.exe -gen2 -hard` вручную.\n", "· 寨板/多卡: retrain-only 在部分主板/拓扑训不上 Gen2, 需 Root Link Disable 回退(瞬断链路)。v2.6 登录任务默认已自动尝试一次 Stage2(Gen2AutoHard); 仍失败可手动 `40HXInstaller.exe -gen2 -hard`。\n"))
	sb.WriteString(tr("· EFI cannot find the card (older versions only scanned bus 0-7): the current EFI extended this to 0-16 plus a CF8 0-255 fallback, covering AGESA/high buses (MSI B450 measured at bus 0x10). If it is still not found after reinstalling the current EFI, the firmware likely did not initialize that headless slot — in BIOS set Above4G + Re-Size BAR / Init Display First = PEG / move the card to the top CPU slot, and send 40hx_log.txt (with the \"diag: CF8 visible devices\" device-map section) to the author.\n", "· EFI не находит карту (старые версии сканировали только шину 0-7): текущий EFI расширил это до 0-16 плюс резерв CF8 0-255, охватывая AGESA/высокие шины (MSI B450 измерена на шине 0x10). Если после переустановки текущего EFI карта все еще не найдена, прошивка, вероятно, не инициализировала этот безголовый слот — в BIOS включите Above4G + Re-Size BAR / Init Display First = PEG / переставьте карту в верхний слот CPU и отправьте 40hx_log.txt (с разделом карты устройств \"diag: CF8 visible devices\") автору.\n", "· EFI 找不到卡(旧版只扫 bus 0-7): v3.0 已扩到 0-16 + CF8 全 0-255 兜底, 覆盖 AGESA/高总线(微星 B450 实测 bus 0x10); 若重装 v3.0 EFI 后仍 not found, 多为固件没初始化该无头槽 — 走 BIOS: Above4G+Re-Size BAR / Init Display First=PEG / 插 CPU 顶槽, 并把 40hx_log.txt(含 \"diag: CF8 visible devices\" 设备映射段)发作者。\n"))
	sb.WriteString(tr("· Idle power-saving downshift: dropping to Gen1 at low load is normal and returns to Gen2 under load; if diagnostics show TLS=Gen2 the configuration succeeded, this is not a failure.\n", "· Понижение скорости для экономии энергии в простое: падение до Gen1 при низкой нагрузке нормально и возвращается к Gen2 под нагрузкой; если диагностика показывает TLS=Gen2, настройка удалась, это не сбой.\n", "· 空闲省电降速: 负载低时链路降到 Gen1 属正常, 负载自动回 Gen2; 诊断 TLS=Gen2 即配置成功, 非失败。\n"))
	sb.WriteString(tr("· Coexistence with the ThrottleStop app: this tool shares ThrottleStop\x27s driver name, reuses its driver, and neither removes nor interrupts it; if there is a conflict, close ThrottleStop and re-run.\n", "· Сосуществование с приложением ThrottleStop: инструмент использует то же имя драйвера, повторно использует его драйвер и не удаляет и не прерывает его; при конфликте закройте ThrottleStop и перезапустите.\n", "· ThrottleStop 软件共存: 本工具驱动与 ThrottleStop 同名, 自动复用其驱动、不删除不打断; 若冲突可关闭 ThrottleStop 后重跑。\n"))
	sb.WriteString(tr("· AV quarantine: third-party AV may quarantine the driver .sys; Defender exclusions are added, for other AV please allow this project\x27s files in its security center.\n", "· Карантин антивируса: сторонний антивирус может поместить .sys драйвера в карантин; исключения Defender добавлены, для других антивирусов разрешите файлы этого проекта в их центре безопасности.\n", "· 杀软隔离: 第三方杀软可能隔离驱动 .sys; 已加 Defender 排除, 其他杀软请在安全中心放行本项目文件。\n"))
	sb.WriteString(tr("· Driver signing: a renamed/recompiled .sys loses its original signature and would need test-signing / an EV certificate; official release files keep the original signature.\n", "· Подпись драйвера: переименованный/перекомпилированный .sys теряет исходную подпись и потребует тестовой подписи / EV-сертификата; официальные выпуски сохраняют исходную подпись.\n", "· 驱动签名: 改名重编的 .sys 会丢失原签名, 需测试签名/EV 证书; 正式发布件保持原签名。\n"))
	sb.WriteString(tr("· When reporting an issue please attach: this diagnostic txt + %TEMP%\\40HX_installer.log + motherboard/CPU/GPU/OS version.\n", "· При создании issue приложите: этот диагностический txt + %TEMP%\\40HX_installer.log + материнская плата/CPU/GPU/версия ОС.\n", "· 报告 issue 时请附: 本诊断 txt + %TEMP%\\40HX_installer.log + 主板/CPU/GPU/系统版本。\n"))
	return sb.String()
}

// drvLogEvidence: 驱动加载不了(drvOK=false)时, 回读历史日志判断"驱动是否曾经成功运行过",
// 返回 (everRan 是否曾成功, summary 摘要). 用于给出针对性建议而非笼统"驱动未就绪" —
// 社区反馈时据此区分"解锁已生效只是本次无权限确认" vs "从未部署需重装"。
func drvLogEvidence() (bool, string) {
	var parts []string
	// 1. gen2_status.txt (计划任务历史快照)
	if gs := hxcore.ReadGen2Status(); gs != "" {
		for _, ln := range strings.Split(gs, "\n") {
			t := strings.TrimSpace(ln)
			if strings.Contains(t, "Gen2") || strings.Contains(t, "无需操作") || strings.Contains(t, "ACHIEVED") {
				parts = append(parts, tr("scheduled-task history: ", "история задачи планировщика: ", "计划任务历史: ")+t)
				break
			}
		}
	}
	// 2. installer.log (安装/任务实跑日志) — 含驱动加载与 Gen2 达成铁证
	logPath := filepath.Join(os.TempDir(), "40HX_installer.log")
	if b, err := os.ReadFile(logPath); err == nil {
		s := string(b)
		// Cross-component contract with the installer's log (inst40hx/main.go):
		// "GEN2 ACHIEVED" is a stable ASCII marker kept identical in every
		// localized variant; the BAR0-verified line and the service-start-failed
		// line are localized, so match all three language forms here.
		if strings.Contains(s, "GEN2 ACHIEVED") ||
			strings.Contains(s, "BAR0 校验通过") || strings.Contains(s, "BAR0 verified") || strings.Contains(s, "BAR0 проверен") {
			parts = append(parts, tr("installer.log: the driver loaded successfully and reached Gen2 before", "installer.log: драйвер ранее успешно загрузился и достиг Gen2", "installer.log: 驱动曾成功加载并达成 Gen2"))
		}
		if (strings.Contains(s, "启动服务") && strings.Contains(s, "失败")) ||
			strings.Contains(s, "Failed to start service") || strings.Contains(s, "Не удалось запустить службу") {
			parts = append(parts, tr("installer.log: a driver start once failed (possible AV quarantine / 1072 leftover)", "installer.log: запуск драйвера однажды не удался (возможен карантин антивируса / остаток 1072)", "installer.log: 曾出现驱动启动失败(可能杀软隔离/1072残留)"))
		}
	}
	if len(parts) == 0 {
		return false, ""
	}
	return true, strings.Join(parts, "; ")
}

func check() {
	var sb strings.Builder
	w := func(format string, a ...interface{}) { sb.WriteString(fmt.Sprintf(format, a...)) }
	var tips []string
	// v2.6.0: 版本标题写入 sb → 弹窗/diagnose.txt 都可见 (此前 fmt.Println 只进 log)
	w("==============================================\n")
	w(tr("  CMP 40HX Unlock Diagnostics  "+hxcore.Version+"   %s\n", "  Диагностика разблокировки CMP 40HX  "+hxcore.Version+"   %s\n", "  CMP 40HX 解锁诊断  "+hxcore.Version+"   %s\n"), time.Now().Format("2006-01-02 15:04:05"))
	w("==============================================\n")
	gpuOK := hxcore.FindGPU()
	sbOn := hxcore.SecureBootOn()
	tsOn := hxcore.TestSigningOn()
	gsOn := hxcore.GspEnabled()
	sub, _, _ := hxcore.GspDiag() // 只需判断 GSP 键是否存在(开关式显示, 不展示寄存器/Adapter 细节)

	// --- A. 主判定: 算力 + Gen2 最优先 (v2.5) ---
	selfM, drvOK, drvFail := ensureDrivers()
	st := hxcore.ReadUnlockStateV2(6, 800)
	bar := strings.Repeat("=", 46)
	w("\n%s\n", bar)
	state := tr("cannot measure (driver not ready)", "нельзя измерить (драйвер не готов)", "无法实测 (驱动未就绪)")
	switch {
	case st.SS0OK && st.Unlocked && st.Speed >= 2:
		state = tr("full compute + Gen2 reached", "полная вычислит. мощность + Gen2 достигнут", "算力满血 + Gen2 达成")
	case st.SS0OK && st.Unlocked && st.TLS >= 2:
		state = tr("full compute + Gen2 target configured (link not yet at Gen2)", "полная мощность + цель Gen2 настроена (канал еще не на Gen2)", "算力满血 + Gen2 目标已配置 (当前链路未到 Gen2)")
	case st.SS0OK && st.Unlocked:
		state = tr("full compute, Gen2 not reached", "полная мощность, Gen2 не достигнут", "算力满血, Gen2 未达成")
	case st.SS0OK:
		state = tr("not unlocked (SS0 locked)", "не разблокировано (SS0 заблокирован)", "未解锁 (SS0 锁定)")
	}
	w(tr("  Unlock status : %s\n", "  Статус разблокировки : %s\n", "  解锁状态 : %s\n"), state)
	comp := tr("unreadable", "нечитаемо", "不可读")
	if st.SS0OK {
		comp = fmt.Sprintf("%s (SS0=0x%08X SS1=0x%08X)",
			map[bool]string{true: tr("✓ full", "✓ полная", "✓ 满血"), false: tr("✗ locked", "✗ заблокировано", "✗ 锁定")}[st.Unlocked], st.SS0, st.SS1)
	}
	spd := tr("unreadable", "нечитаемо", "不可读")
	if st.Speed >= 1 {
		names := map[uint32]string{1: "Gen1 (2.5 GT/s)", 2: "Gen2 (5.0 GT/s)", 3: "Gen3 (8.0 GT/s)", 4: "Gen4 (16 GT/s)"}
		spd = names[st.Speed]
		if spd == "" {
			spd = fmt.Sprintf("Gen%d", st.Speed)
		}
		if st.Width >= 1 {
			spd += fmt.Sprintf(" ×%d", st.Width)
		}
		if st.Speed < 2 && st.TLS >= 2 {
			spd += fmt.Sprintf(tr(" (target Gen%d — idle power-saving downshift is normal; if it stays Gen1 under sustained load see the verdict)", " (цель Gen%d — понижение для экономии в простое нормально; если под постоянной нагрузкой остается Gen1, см. заключение)", " (目标 Gen%d — 空闲省电降速属正常; 若持续负载仍 Gen1 见结论)"), st.TLS)
		}
	}
	w(tr("  Compute  : %s\n", "  Вычисления : %s\n", "  算力     : %s\n"), comp)
	w("  PCIe     : %s\n", spd)
	// ReBAR (Resizable BAR) — GPU-Z 式判定。NVIDIA APP/控制面板不显示此项。
	rebar := tr("unreadable (driver not ready)", "нечитаемо (драйвер не готов)", "不可读 (驱动未就绪)")
	if st.RebarOK {
		if st.RebarOn {
			rebar = fmt.Sprintf(tr("✓ enabled (BAR1 %s)", "✓ включён (BAR1 %s)", "✓ 已启用 (BAR1 %s)"), rebarSizeStr(st.RebarSizeMB))
		} else {
			rebar = fmt.Sprintf(tr("✗ not enabled (BAR1 %s; stock 256 MB — ReBAR unlock not in effect)", "✗ не включён (BAR1 %s; сток 256 MB — разблокировка ReBAR не действует)", "✗ 未启用 (BAR1 %s; 原生 256 MB — ReBAR 解锁未生效)"), rebarSizeStr(st.RebarSizeMB))
		}
	}
	w("  ReBAR    : %s\n", rebar)
	w("%s\n", bar)

	// --- B. 基础状态 ---
	gspTxt := tr("✗ not enabled (the installer sets it automatically after the NVIDIA driver is installed)", "✗ не включено (установщик задаст автоматически после установки драйвера NVIDIA)", "✗ 未启用(装 NVIDIA 驱动后由安装器自动设)")
	if gsOn {
		gspTxt = tr("✓ enabled", "✓ включено", "✓ 已启用")
	} else if sub == "" {
		gspTxt = tr("— GSP key not found (install the NVIDIA driver first before GSP can be set)", "— ключ GSP не найден (сначала установите драйвер NVIDIA, затем можно задать GSP)", "— 未找到 GSP 键(需先装好 NVIDIA 驱动才能设置 GSP)")
	}
	w("GPU 40HX: %s   Secure Boot: %s   GSP: %s\n",
		map[bool]string{true: "✓", false: "✗"}[gpuOK],
		map[bool]string{true: tr("on (must disable!)", "вкл (нужно отключить!)", "开启(需关闭!)"), false: tr("off (OK)", "выкл (OK)", "关闭(OK)")}[sbOn], gspTxt)
	w(tr("Test signing: %s  (not needed since v2.5, recommend disabling)\n", "Тестовая подпись: %s  (не нужна с v2.5, рекомендуется отключить)\n", "测试签名: %s  (v2.5 不需要, 建议关闭)\n"),
		map[bool]string{true: tr("on", "вкл", "已开启"), false: tr("off", "выкл", "关闭")}[tsOn])
	// v3.1: Secure Boot 共存 —— 解锁 EFI 是否带签名, 决定了 Secure Boot 能不能开着用。
	// 未签名的 EFI 在 Secure Boot 下固件会直接拒绝执行, 这是"装完重启没解锁"的
	// 常见原因之一; 带签名(且密钥已登记进 db)的则可以共存。这里如实报实测结果。
	efi := collectEFIFacts()
	w(tr("Unlock EFI: %s\n", "Разблокировочный EFI: %s\n", "解锁 EFI: %s\n"), efi.Line)
	// v2.6.0: 引导模式 + 电源设置 — Legacy/MBR、快速启动、ASPM 是三类高频根因
	// (分别对应"EFI 装不上"/"关机再开 EFI 没跑"/"空闲 Gen1 误报失败")
	bootMode := "UEFI (OK)"
	if hxcore.FirmwareIsLegacy() {
		bootMode = tr("Legacy BIOS+MBR (no EFI partition, compute unlock unavailable!)", "Legacy BIOS+MBR (нет раздела EFI, разблокировка вычислений недоступна!)", "Legacy BIOS+MBR (无 EFI 分区, 算力解锁不可用!)")
	}
	fsOn := hxcore.FastStartupOn()
	ac, dc, aspmOK := hxcore.ASPMSavings()
	aspmStr := tr("not detectable (skipped)", "не определяется (пропуск)", "不可检测(跳过)")
	if aspmOK {
		if ac == 0 && dc == 0 {
			aspmStr = tr("off (OK)", "выкл (OK)", "关闭(OK)")
		} else {
			aspmStr = fmt.Sprintf(tr("on (AC=%d DC=%d) — may downshift to Gen1 at idle, recommend disabling", "вкл (AC=%d DC=%d) — может понижаться до Gen1 в простое, рекомендуется отключить", "开启(AC=%d DC=%d) — 空闲可能降速 Gen1, 建议关闭"), ac, dc)
		}
	}
	w(tr("Boot mode: %s\n", "Режим загрузки: %s\n", "引导模式: %s\n"), bootMode)
	w(tr("Fast Startup: %s   PCIe ASPM: %s\n", "Быстрый запуск: %s   PCIe ASPM: %s\n", "快速启动: %s   PCIe ASPM: %s\n"),
		map[bool]string{true: tr("on (recommend disabling)", "вкл (рекомендуется отключить)", "开启(建议关闭)"), false: tr("off (OK)", "выкл (OK)", "关闭(OK)")}[fsOn],
		aspmStr)

	// --- C. 明细: Gen2 驱动(逐个)/任务/历史记录 ---
	// 逐个驱动显示: 可能一个被杀软拦了、另一个正常, 只显示汇总会误导。
	tsTxt := tr("✗ not running", "✗ не работает", "✗ 未运行")
	if st.TSOK {
		tsTxt = tr("✓ available", "✓ доступно", "✓ 可用")
	} else if svcState(svcTS) == "RUNNING" {
		tsTxt = tr("⚠ service is up, but the device cannot be opened", "⚠ служба работает, но устройство не открывается", "⚠ 服务在, 但设备打不开")
	}
	wrTxt := tr("✗ not running", "✗ не работает", "✗ 未运行")
	if st.WinRingOK {
		wrTxt = tr("✓ available", "✓ доступно", "✓ 可用")
	} else if svcState(svcWR) == "RUNNING" {
		wrTxt = tr("⚠ service is up, but the device cannot be opened", "⚠ служба работает, но устройство не открывается", "⚠ 服务在, 但设备打不开")
	}
	w(tr("Gen2 drivers: ThrottleStop %s   WinRing0 %s\n", "Драйверы Gen2: ThrottleStop %s   WinRing0 %s\n", "Gen2 驱动: ThrottleStop %s   WinRing0 %s\n"), tsTxt, wrTxt)
	if selfM {
		w("%s", tr("  ↑ temporarily loaded by this diagnostic and unloaded after the test — does not mean it is installed\n", "  ↑ временно загружено этой диагностикой и выгружено после теста — не означает, что установлено\n", "  ↑ 由本诊断临时拉起, 测完即卸 — 不代表已安装\n"))
	} else if st.TSOK || st.WinRingOK {
		w("%s", tr("  ↑ driver is deployed / externally loaded\n", "  ↑ драйвер развернут / загружен извне\n", "  ↑ 驱动为已部署/外部加载\n"))
	}
	if !drvOK && drvFail != "" {
		w(tr("  └ start failed: %s\n", "  └ сбой запуска: %s\n", "  └ 启动失败: %s\n"), classifyLoadErr(drvFail))
	}
	w("\n")
	taskOK, taskStatus, taskResult := hxcore.TaskInfo(gen2TaskName)
	w(tr("Gen2 task: %s\n", "Задача Gen2: %s\n", "Gen2 任务: %s\n"),
		map[bool]string{true: tr("registered (", "зарегистрирована (", "已注册 (") + taskStatus + tr(", last result: ", ", последний результат: ", ", 上次结果: ") + taskResult + ")",
			false: tr("not registered (fix: right-click and run 40HXInstaller.exe -task as administrator)", "не зарегистрирована (исправление: щелкните правой кнопкой и запустите 40HXInstaller.exe -task от имени администратора)", "未注册 (修复: 右键管理员运行 40HXInstaller.exe -task)")}[taskOK])
	// gen2_status.txt 是上次 Gen2 任务写入的"历史快照", 不是本次实测:
	// 只有驱动实测不可用时才作为参考展示, 且明确标注为历史记录,
	// 避免"卸载后还显示 ✅ Gen2"的误导。
	if !st.SS0OK || st.Speed < 2 {
		if gs := hxcore.ReadGen2Status(); gs != "" {
			w(tr("  Note: history from the last Gen2 task exists (not this run\x27s measurement):\n%s\n", "  Примечание: есть история последней задачи Gen2 (не измерение этого запуска):\n%s\n", "  注: 存在上次 Gen2 任务的历史记录(非本次实测):\n%s\n"), indentLines(gs))
			if !drvOK {
				w("%s", tr("       ↑ the driver is not running now — this is a historical leftover, not the current state\n", "       ↑ драйвер сейчас не работает — это старые данные, не текущее состояние\n", "       ↑ 驱动当前未运行 — 此为历史残留, 不代表当前状态\n"))
			}
		}
	}
	if drvOK && !st.SS0OK {
		w("%s", tr("(the driver is running but the compute register cannot be read — abnormal)\n", "(драйвер работает, но регистр вычислений не читается — аномалия)\n", "(驱动已运行但读不到算力寄存器 — 异常)\n"))
	}

	// --- D. 结论与建议 ---
	verdict := ""
	switch {
	case st.Unlocked && st.Speed >= 2:
		verdict = tr(">>> Unlock succeeded: full Tensor + Gen2", ">>> Разблокировка удалась: полный Tensor + Gen2", ">>> 解锁成功: Tensor 满血 + Gen2")
		if st.Width >= 1 {
			verdict += fmt.Sprintf(" ×%d", st.Width)
		} else {
			verdict += tr(" (link width not measured)", " (ширина канала не измерена)", " (链路宽度未测到)")
		}
	case st.Unlocked && st.TLS >= 2:
		if st.Speed == 1 {
			verdict = tr(">>> Full compute + Gen2 target configured (currently Gen1: idle power-saving downshift or not yet retrained this run; returns to Gen2 under load/after retrain)", ">>> Полная мощность + цель Gen2 настроена (сейчас Gen1: понижение для экономии в простое или еще не переобучено в этом запуске; вернется к Gen2 под нагрузкой/после переобучения)", ">>> 算力满血 + Gen2 目标已配置 (当前 Gen1: 空闲省电降速或本次尚未训上; 负载/重训后回 Gen2)")
		} else {
			verdict = tr(">>> Full compute + Gen2 target configured (current link speed not measured)", ">>> Полная мощность + цель Gen2 настроена (текущая скорость канала не измерена)", ">>> 算力满血 + Gen2 目标已配置 (当前链路速率未测到)")
		}
	case st.Unlocked:
		verdict = tr(">>> Full compute; Gen2 not reached — the logon task already runs at every logon (including automatic Stage2); if it still fails, click [Run Gen2 now] in GUI section ② (see README §5.2)", ">>> Полная мощность; Gen2 не достигнут — задача входа уже выполняется при каждом входе (включая автоматический Stage2); если не помогло, нажмите [Выполнить Gen2 сейчас] в разделе ② GUI (см. README §5.2)", ">>> 算力满血; Gen2 未达成 — 登录任务每次登录已自动执行(含自动 Stage2); 仍失败可在 GUI ② 区点[立即执行 Gen2] 再试(详见 README §5.2)")
	case st.SS0OK:
		verdict = tr(">>> Not unlocked this boot (SS0 locked)", ">>> В этой загрузке не разблокировано (SS0 заблокирован)", ">>> 本次开机未解锁 (SS0 锁定)")
		default:
			if !drvOK {
				if !isAdmin() {
					verdict = tr(">>> Driver not loaded: this tool needs administrator rights to load the kernel driver and measure state. Right-click this program -> Run as administrator", ">>> Драйвер не загружен: инструменту нужны права администратора для загрузки драйвера ядра и измерения состояния. Щелкните правой кнопкой -> Запуск от имени администратора", ">>> 驱动未加载: 本工具需管理员权限加载内核驱动以实测状态。请右键本程序 -> 以管理员身份运行")
				} else if gpuOK && gsOn {
					if ever, ev := drvLogEvidence(); ever {
						verdict = tr(">>> The driver is not running now, but logs show Gen2 was reached successfully before (", ">>> Драйвер сейчас не работает, но журналы показывают, что Gen2 ранее был достигнут (", ">>> 驱动当前未运行, 但日志显示此前已成功达成 Gen2 (") + ev + tr(").  Unlock is in effect; right-click and run this tool as administrator to read live state", "). Разблокировка действует; щелкните правой кнопкой и запустите инструмент от имени администратора для чтения состояния в реальном времени", ")。解锁已生效, 右键以管理员运行本工具可读实时状态")
					} else {
						verdict = tr(">>> Driver unavailable — run from the 40HXUnlock release folder (which contains gen2\\drivers), or re-run 40HXInstaller.exe as administrator to install the driver first", ">>> Драйвер недоступен — запустите из папки выпуска 40HXUnlock (в ней есть gen2\\drivers) или сначала перезапустите 40HXInstaller.exe от имени администратора для установки драйвера", ">>> 驱动不可用 — 请从 40HXUnlock 发布目录(含 gen2\\drivers)运行, 或先管理员重跑 40HXInstaller.exe 安装驱动")
					}
				} else {
					verdict = tr(">>> Cannot complete the unlock verdict (see the items above)", ">>> Не удалось вынести заключение о разблокировке (см. пункты выше)", ">>> 无法完成解锁判定 (见上方分项)")
				}
			} else {
				verdict = tr(">>> Cannot complete the unlock verdict (see the items above)", ">>> Не удалось вынести заключение о разблокировке (см. пункты выше)", ">>> 无法完成解锁判定 (见上方分项)")
			}
		}
	w("\n%s\n", verdict)
	if st.SS0OK && !st.Unlocked {
		if reason := hxcore.AnalyzeEfiLog(); reason != "" {
			w("%s\n", reason)
		}
	}

	// v2.6.0: 弹窗只显示"简洁结论 + 基础状态"; 下方详细建议/原始寄存器只进 diagnose.txt(日志)与剪贴板,
	// 不放 GUI —— 用户要求提示放 txt 不放弹窗, 社区看完整诊断去 logs 目录即可。
	guiHead := sb.String()

	if !gpuOK {
		tips = append(tips, tr("· 40HX not detected: make sure the card is seated and its driver is installed", "· 40HX не обнаружена: убедитесь, что карта вставлена и драйвер установлен", "· 未检测到 40HX: 确认显卡已插且驱动已装"))
	}
	if sbOn {
		tips = append(tips, tr("· Secure Boot is on: the firmware only runs the unlock EFI if it is signed by a key enrolled in db.\n  If the EFI above shows 'unsigned', disable Secure Boot in BIOS — or sign the EFI with your own key and enroll it (see README §Secure Boot).",
			"· Secure Boot включён: прошивка выполнит разблокировочный EFI, только если он подписан ключом, зарегистрированным в db.\n  Если выше указано «без подписи», отключите Secure Boot в BIOS — либо подпишите EFI своим ключом и зарегистрируйте его (см. README §Secure Boot).",
			"· Secure Boot 开启: 固件只执行由已登记进 db 的密钥签名的解锁 EFI。\n  若上面显示『未签名』, 请进 BIOS 关闭 Secure Boot — 或用你自己的密钥签名并登记该密钥(见 README §Secure Boot)。"))
	}
	if tsOn {
		tips = append(tips, tr("· Test signing is on (not needed since v2.5): disable with bcdedit /set testsigning off", "· Тестовая подпись включена (не нужна с v2.5): отключите через bcdedit /set testsigning off", "· 测试签名已开启 (v2.5 不需要): bcdedit /set testsigning off 可关闭"))
	}
	if !gsOn {
		tips = append(tips, tr("· GSP not enabled: double-click 40HXInstaller.exe → ① check [Enable GSP] and click [Install selected components]", "· GSP не включен: дважды щелкните 40HXInstaller.exe → ① отметьте [Включить GSP] и нажмите [Установить выбранные компоненты]", "· GSP 未启用: 双击 40HXInstaller.exe → ① 勾 [GSP 启用] 点[安装所选组件]"))
	}
	if gpuOK && st.SS0OK && !st.Unlocked {
		tips = append(tips, tr("· EFI compute unlock not in effect (SS0 locked): if the '40HX Unlock' EFI is not deployed / was uninstalled on this machine, this is expected and compute stays locked — to restore it re-run 40HXInstaller.exe and check [Compute EFI deploy + firmware boot entry]; if the EFI is installed, confirm the boot went through the '40HX Unlock' entry / Above 4G is on / Secure Boot is off", "· Разблокировка вычислений через EFI не действует (SS0 заблокирован): если EFI '40HX Unlock' не развернут / удален на этой машине, это ожидаемо и вычисления остаются заблокированными — чтобы восстановить, перезапустите 40HXInstaller.exe и отметьте [Развертывание вычислительного EFI + запись загрузки прошивки]; если EFI установлен, проверьте, что загрузка прошла через запись '40HX Unlock' / Above 4G включен / Secure Boot выключен", "· EFI 算力解锁未生效(SS0 锁定): 若本机未部署/已卸载 '40HX Unlock' 解锁 EFI, 此提示属预期, 算力会保持锁定 — 想恢复请重跑 40HXInstaller.exe 勾选[算力 EFI 部署+固件启动项]; 若 EFI 已装, 则确认开机走了 '40HX Unlock' 启动项 / Above 4G 已开 / Secure Boot 已关"))
	}
	// v2.6.0: 社区高频根因的四条定向提示
	if hxcore.FirmwareIsLegacy() {
		tips = append(tips, tr("· Boot mode is Legacy BIOS+MBR: no EFI partition, so compute unlock cannot be installed —\n  follow README §2.4 to convert to GPT with mbr2gpt, then re-run the installer (Gen2 is unaffected)", "· Режим загрузки Legacy BIOS+MBR: нет раздела EFI, разблокировку вычислений установить нельзя —\n  по README §2.4 преобразуйте в GPT через mbr2gpt, затем перезапустите установщик (на Gen2 не влияет)", "· 引导模式为 Legacy BIOS+MBR: 没有 EFI 分区, 算力解锁装不上 —\n  按README §2.4 用 mbr2gpt 转 GPT 后重跑安装器 (Gen2 不受影响)"))
	}
	if fsOn {
		tips = append(tips, tr("· Fast Startup (hybrid hibernation) is on: a shutdown-then-boot may skip a full UEFI boot → the EFI does not run;\n  the installer disables it automatically; manually: Control Panel > Power Options, uncheck 「Fast Startup」", "· Быстрый запуск (гибридный гибернат) включен: выключение и включение может пропустить полную загрузку UEFI → EFI не выполняется;\n  установщик отключает его автоматически; вручную: Панель управления > Электропитание, снимите флажок 「Быстрый запуск」", "· 快速启动(混合休眠)开启: 关机再开可能不做完整 UEFI 引导 → EFI 不执行;\n  安装器会自动关闭; 手动: 控制面板电源选项取消勾选『快速启动』"))
	}
	if aspmOK && (ac > 0 || dc > 0) {
		tips = append(tips, tr("· PCIe link power saving (ASPM) is on: dropping to Gen1 at idle is normal saving and recovers under load;\n  to stay at Gen2 disable it: powercfg -setacvalueindex SCHEME_CURRENT SUB_PCIEXPRESS ASPM 0\n  (then the same with -setdcvalueindex, then -setactive SCHEME_CURRENT to apply)", "· Энергосбережение канала PCIe (ASPM) включено: падение до Gen1 в простое — нормальная экономия, восстанавливается под нагрузкой;\n  чтобы оставаться на Gen2, отключите: powercfg -setacvalueindex SCHEME_CURRENT SUB_PCIEXPRESS ASPM 0\n  (затем то же с -setdcvalueindex, затем -setactive SCHEME_CURRENT для применения)", "· PCIe 链路省电(ASPM)开启: 空闲时降到 Gen1 属正常省电, 负载自动回升;\n  想常驻 Gen2 可关闭: powercfg -setacvalueindex SCHEME_CURRENT SUB_PCIEXPRESS ASPM 0\n  (再加 -setdcvalueindex 同参数, 然后 -setactive SCHEME_CURRENT 生效)"))
	}
	if !taskOK {
		tips = append(tips, tr("· The Gen2 scheduled task is not registered: Gen2 will not unlock automatically after logon —\n  double-click 40HXInstaller.exe → ② click [Run Gen2 and install autostart] to do it in one step (unlock now + register autostart); or ① check [Gen2 logon autostart] and click [Install selected components]", "· Задача планировщика Gen2 не зарегистрирована: Gen2 не разблокируется автоматически после входа —\n  дважды щелкните 40HXInstaller.exe → ② нажмите [Выполнить Gen2 и установить автозапуск] за один шаг (разблокировать сейчас + зарегистрировать автозапуск); или ① отметьте [Автозапуск Gen2 при входе] и нажмите [Установить выбранные компоненты]", "· Gen2 计划任务未注册: 登录后不会自动解锁 Gen2 —\n  双击 40HXInstaller.exe → ② 点[执行 Gen2 并安装自启]一步到位（本次解锁+注册自启）; 或 ① 勾[Gen2 登录自启]点[安装所选组件]"))
	}
	if st.SS0OK && st.TLS < 2 && st.TLS >= 1 && st.Unlocked {
		tips = append(tips, tr("· Gen2 target rate (TLS) is still Gen1: the unlock write did not take — if the log shows all four PL0 registers OK but LNKCTL2 reads back as Gen1,\n  the driver usually rewrites the link policy within milliseconds; the logon task (if registered) tries automatically after logon and runs the Stage2 fallback; if it still fails, first retry [Run Gen2 now] in GUI ② (the Stage2 fallback is included automatically by default) — only use the command line `40HXInstaller.exe -gen2 -hard` when auto-fallback is turned off in ② or you want to force it. Confirmed not to be firmware write protection, **do NOT flash the VBIOS** — if it still fails, send diagnostics and installer.log for feedback (see README §5.2)", "· Целевая скорость Gen2 (TLS) все еще Gen1: запись разблокировки не применилась — если лог показывает все четыре регистра PL0 OK, но LNKCTL2 читается как Gen1,\n  драйвер обычно переписывает политику канала в течение миллисекунд; задача входа (если зарегистрирована) автоматически пробует после входа и запускает откат Stage2; если по-прежнему не удаётся, сначала повторите [Выполнить Gen2 сейчас] в GUI ② (откат Stage2 включён автоматически по умолчанию) — командную строку `40HXInstaller.exe -gen2 -hard` используйте только если авто-откат в ② выключен или нужно принудительно. Подтверждено, что это не защита прошивки от записи, **НЕ прошивайте VBIOS** — если всё равно не удаётся, пришлите диагностику и installer.log (см. README §5.2)", "· Gen2 目标速率(TLS)仍是 Gen1: 解锁写入未生效 — 若日志显示 PL0 四寄存器全 OK 而回读 LNKCTL2 仍 Gen1,\n  多为驱动持链路策略毫秒内回写; 登录任务(前提: 已注册)在登录后会自动尝试并跑 Stage2 回退; 仍失败先在 GUI ② 点[立即执行 Gen2]（默认自动含 Stage2 回退）重试 — 仅当 ② 里关掉了自动回退或想强制触发时才用命令行 `40HXInstaller.exe -gen2 -hard`。已确认不是固件写保护, **不要刷 VBIOS** — 仍不行发诊断与 installer.log 反馈(见 README §5.2)"))
	}
	if st.SS0OK && st.Unlocked && st.Speed < 2 && st.TLS >= 2 {
		tips = append(tips, tr("· Gen2 target is configured (TLS=Gen2) but the current link is Gen1: usually idle power-saving downshift (normal, recovers under load); if it stays Gen1 under sustained load, retrain directly via [Run Gen2 now] in GUI ② (Gen2AutoHard is on by default and runs the Stage2 fallback automatically); only use the command line `40HXInstaller.exe -gen2 -hard` when auto-fallback is off in ② or you want to force it (briefly drops the link, see README §3.3)", "· Цель Gen2 настроена (TLS=Gen2), но текущий канал Gen1: обычно понижение для экономии в простое (нормально, восстанавливается под нагрузкой); если под постоянной нагрузкой остаётся Gen1, переобучите напрямую через [Выполнить Gen2 сейчас] в GUI ② (Gen2AutoHard включён по умолчанию и запускает откат Stage2 автоматически); командную строку `40HXInstaller.exe -gen2 -hard` используйте только если авто-откат в ② выключен или нужно принудительно (кратковременно разрывает канал, см. README §3.3)", "· Gen2 目标已配置(TLS=Gen2)但当前链路 Gen1: 多为空闲省电降速(正常, 负载自动回升); 若持续负载仍 Gen1: 直接在 GUI ② 点[立即执行 Gen2] 重训（Gen2AutoHard 默认开会自动走 Stage2 回退）; 仅当 ② 关掉自动回退或想强制触发时, 再用命令行 `40HXInstaller.exe -gen2 -hard`(瞬断链路, 见 README §3.3)"))
	}
	if !taskOK && st.SS0OK && st.Unlocked && (st.Speed >= 2 || st.TLS >= 2) {
		tips = append(tips, tr("· Note: the current Gen2 status is measured from the GPU target-rate register (TLS); if no unlock flow has run this boot (autostart task not registered), the value is most likely a leftover from the previous run — a full shutdown-then-boot / GPU reset will re-lock it. Use GUI ② [Run Gen2 and install autostart] to do it in one step (unlock now + register boot autostart)", "· Примечание: текущий статус Gen2 измерен из регистра целевой скорости GPU (TLS); если в этой загрузке не выполнялся ни один процесс разблокировки (задача автозапуска не зарегистрирована), значение, скорее всего, осталось от предыдущего запуска — полное выключение и включение / сброс видеокарты снова заблокирует. Используйте GUI ② [Выполнить Gen2 и установить автозапуск] за один шаг (разблокировать сейчас + зарегистрировать автозапуск при загрузке)", "· 注: 当前 Gen2 状态来自 GPU 目标速率寄存器(TLS)实测; 若本次开机还没有任何解锁流程执行过(自启任务未注册), 该值多半是上次运行残留 — 完全关机再开/显卡复位后会回锁。请用 GUI ② [执行 Gen2 并安装自启] 一步到位(本次解锁+注册开机自启)"))
	}
	if throttleStopAppRunning() && !drvOK {
		tips = append(tips, tr("· The ThrottleStop app is running and this tool\x27s driver is not ready: the tool reuses its driver automatically; if it still fails, close ThrottleStop and re-run the installer", "· Приложение ThrottleStop запущено, а драйвер этого инструмента не готов: инструмент автоматически повторно использует его драйвер; если не помогло, закройте ThrottleStop и перезапустите установщик", "· 检测到 ThrottleStop 软件在运行且本工具驱动未就绪: 本工具自动复用其驱动; 若仍失败, 关闭 ThrottleStop 后重跑安装器"))
	}
	if !drvOK {
		if drvFail != "" {
			tips = append(tips, tr("· Could not start the Gen2 driver: ", "· Не удалось запустить драйвер Gen2: ", "· Gen2 驱动未能拉起: ")+classifyLoadErr(drvFail))
		}
		if ever, ev := drvLogEvidence(); ever {
			tips = append(tips, tr("· The driver is not running now, but logs show Gen2 was reached before (", "· Драйвер сейчас не работает, но журналы показывают, что Gen2 ранее был достигнут (", "· 驱动当前未运行, 但日志显示此前已成功达成 Gen2 (")+ev+tr(") — the unlock is in effect; right-click and run this tool as administrator to read live state", ") — разблокировка действует; щёлкните правой кнопкой и запустите инструмент от имени администратора, чтобы прочитать состояние в реальном времени", ") — 解锁已生效, 右键以管理员运行本工具可读实时状态"))
		} else {
			tips = append(tips, tr("· The driver was never deployed successfully (no success record in installer.log/gen2_status): double-click 40HXInstaller.exe → in section ① click [One-click full install] to finish deployment and autostart registration", "· Драйвер ни разу не был успешно развёрнут (нет записи об успехе в installer.log/gen2_status): дважды щёлкните 40HXInstaller.exe → в разделе ① нажмите [Полная установка в один клик] для завершения развёртывания и регистрации автозапуска", "· 驱动从未成功部署(无 installer.log/gen2_status 成功记录): 双击 40HXInstaller.exe → ① 区点[一键完整安装]完成部署与自启注册"))
		}
	}
	if len(tips) > 0 {
		w(tr("\nSuggestions:\n%s\n", "\nРекомендации:\n%s\n", "\n建议:\n%s\n"), strings.Join(tips, "\n"))
	}
	// 弹窗只带第一条"下一步"(其余在 diagnose.txt/剪贴板) — 让安装者一眼知道该干嘛。
	popupTip := ""
	if len(tips) > 0 {
		popupTip = tr("\n\n▶ Next step: ", "\n\n▶ Следующий шаг: ", "\n\n▶ 下一步: ") + strings.SplitN(tips[0], "\n", 2)[0]
	}

	// v2.6.0: 加强诊断快照 — 环境/驱动/原始 PCIe 寄存器/已知限制, 便于社区反馈
	w("%s", tr("\n========== Environment ==========\n", "\n========== Окружение ==========\n", "\n========== 环境 ==========\n"))
	w("  OS      : %s\n", osVersion())
	w(tr("  Administrator : %s   Tool version: "+hxcore.Version+"\n", "  Администратор : %s   Версия инструмента: "+hxcore.Version+"\n", "  管理员   : %s   工具版本: "+hxcore.Version+"\n"), map[bool]string{true: tr("yes", "да", "是"), false: tr("no", "нет", "否")}[isAdmin()])
	w("%s", tr("\n========== Drivers ==========\n", "\n========== Драйверы ==========\n", "\n========== 驱动 ==========\n"))
	w("%s", driverDetail(svcTS, fileTS))
	w("%s", driverDetail(svcWR, fileWR))
	if selfM {
		w("%s", tr("  ↑ this diagnostic loaded it TEMPORARILY to read hardware state and cleans up after the test — it does not mean it is installed\n", "  ↑ эта диагностика загрузила его ВРЕМЕННО для чтения состояния оборудования и очищает после теста — это не означает, что он установлен\n", "  ↑ 本诊断【临时拉起】以读取硬件状态, 测完自动清理 — 不代表已安装\n"))
	} else if st.TSOK || st.WinRingOK {
		w("%s", tr("  ↑ the driver is deployed / externally loaded; this tool does not clean it up\n", "  ↑ драйвер развёрнут / загружен извне; этот инструмент его не удаляет\n", "  ↑ 驱动为已部署/外部加载, 本工具不清理\n"))
	}
	w(tr("  ThrottleStop app running: %s\n", "  Приложение ThrottleStop запущено: %s\n", "  ThrottleStop 软件运行: %s\n"), map[bool]string{true: tr("yes (coexists, its driver is not removed)", "да (сосуществует, его драйвер не удаляется)", "是(共存, 不删其驱动)"), false: tr("no", "нет", "否")}[throttleStopAppRunning()])
	w("%s", tr("\n========== Raw PCIe registers (40HX) ==========\n", "\n========== Сырые регистры PCIe (40HX) ==========\n", "\n========== 原始 PCIe 寄存器 (40HX) ==========\n"))
	w("%s", rawPcieDump())
	w("%s", knownIssuesBlock())

	out := sb.String()
	fmt.Println(out)

	// v2.5: 本工具拉起过驱动则用完即卸 (保持系统无第三方驱动)
	if selfM {
		cleanupDrivers()
	}

	dir := collectLogs(out)
	copied := copyToClipboard(out)
	note := ""
	if copied {
		note = tr("\n\nThe full diagnostics (with troubleshooting suggestions and raw registers) were copied to the clipboard — just paste into the issue.", "\n\nПолная диагностика (с рекомендациями и сырыми регистрами) скопирована в буфер обмена — просто вставьте в issue.", "\n\n完整诊断(含排查建议与原始寄存器)已复制到剪贴板 — 直接粘贴到 issue 即可。")
	}
	ok := st.Unlocked && (st.Speed >= 2 || st.TLS >= 2)
	// 弹窗只给简洁结论 + 指向详细日志; 详细内容已在 diagnose.txt 与剪贴板。
	msgbox(guiHead+popupTip+tr("\n\nThe full diagnostics (with troubleshooting suggestions and raw PCIe registers) were written to:\n", "\n\nПолная диагностика (с рекомендациями и сырыми регистрами PCIe) записана в:\n", "\n\n完整诊断(含排查建议与原始 PCIe 寄存器)已写入:\n")+dir+tr("\\diagnose.txt\nFor troubleshooting guidance see AI辅助安装提示词.txt (AI-assisted install prompts) inside the package", "\\diagnose.txt\nРуководство по устранению неполадок см. в AI辅助安装提示词.txt (подсказки для установки с помощью ИИ) внутри пакета", "\\diagnose.txt\n排查引导见包内 AI辅助安装提示词.txt")+note,
		map[bool]uint{true: 0x40, false: 0x30}[ok])
}

func main() {
	// Resolve the display language before any output or UAC prompt: -lang flag /
	// CMP40HX_LANG env / the value the installer persisted to the registry, else
	// English. This is what makes the checker follow the installer's choice.
	hxcore.InitLanguage(os.Args)
	// v3.1: -json prints a machine-readable report to stdout. It is still a
	// hardware probe, so it keeps the same administrator requirement.
	jsonMode := hasArg("-json")
	if len(os.Args) < 2 || (os.Args[1] != "-elevated" && !hasArg("-elevated")) {
		if !isAdmin() {
			selfElevate()
			return
		}
	}
	// 输出镜像到统一日志目录。JSON 模式下 stdout 必须是纯 JSON 文档,
	// 因此只在非 JSON 模式把 stdout 重定向到日志(否则 JSON 会被写进文件而不是终端)。
	dir := logsDir()
	if !jsonMode {
		if f, err := os.Create(filepath.Join(dir, "40HXCheck.log")); err == nil {
			os.Stdout = f
			os.Stderr = f
			fmt.Fprintf(f, "==== 40HXCheck %s ====\n", time.Now().Format("2006-01-02 15:04:05"))
		}
	}
	if jsonMode {
		runJSON()
		return
	}
	check()
}

// hasArg reports whether the given switch appears anywhere in os.Args.
func hasArg(name string) bool {
	for _, a := range os.Args {
		if a == name {
			return true
		}
	}
	return false
}

// runJSON 采集与文本模式同源的状态, 输出 JSON 后按结论设置退出码。
// 判定逻辑复用 check() 用的同一批探测器, 不重复实现。
func runJSON() {
	// ensureDrivers 会按需临时拉起驱动 —— 必须在读状态之前调用,
	// 否则没有驱动可读, JSON 里所有实测项都会是"未测量"。
	selfM, drvOK, _ := ensureDrivers()
	st := hxcore.ReadUnlockStateV2(6, 800)
	efi := collectEFIFacts()
	aspm := "unknown"
	var ac, dc uint32
	if a, d, ok := hxcore.ASPMSavings(); ok {
		ac, dc = a, d
		if a == 0 && d == 0 {
			aspm = "off"
		} else {
			aspm = "on"
		}
	}
	// JSON 只读、不应留痕: 若驱动是本工具临时拉起的, 输出后再清理。
	// 注意不能用 defer —— writeJSONReport 以 os.Exit 结束, defer 永远不执行。
	code := writeJSONReport(*st, drvOK, efi, aspm, ac, dc)
	if selfM {
		cleanupDrivers()
	}
	os.Exit(code)
}
