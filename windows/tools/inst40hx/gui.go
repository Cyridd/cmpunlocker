package main

// v2.6.0: 精简单界面 GUI (walk) — 无选项卡、无状态表格, 安装器只管安装。
// 打开时自动只读扫描一次(不展示明细): 结果只用于
//   ① 组件安装区的"环境提示"行(未检测到卡/SecureBoot/Legacy 等警告)
//   ② 缺失/未达标组件的自动预勾(已装不勾 = 不覆盖)
// 界面自上而下:
//   ① 组件安装 — [安装所选组件] / [一键完整安装(全流程)], 装完自动重扫更新提示
//   ② Gen2 策略 — 驱动运行策略 + 自动 Stage2 回退 + 失败重试次数/间隔,[保存策略]
//   ③ 操作日志 — AttachLogSink 实时输出(GUI 与 CLI 共用全部实现)
// 卸载与详细诊断不在本界面: 40HXUninstaller.exe / -uninstall / 40HXCheck.exe。
// 线程约定: OnClicked(UI 线程)只读控件 → goroutine 执行 → UI 变更一律经 sync()。

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"40hxcore"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"golang.org/x/sys/windows/registry"
)

// ---- 环境扫描(只读; 结果喂 tip 与预勾选, 不展示明细表) ----

type statusItem struct {
	name string
	ok   bool
	note string
}

func scanStatus() []statusItem {
	items := []statusItem{}
	legacy := hxcore.FirmwareIsLegacy()
	items = append(items, statusItem{tr("Boot mode", "Режим загрузки", "引导模式"), !legacy,
		map[bool]string{true: "UEFI (OK)", false: tr("Legacy+MBR — compute unlock unavailable; convert to GPT with mbr2gpt", "Legacy+MBR — разблокировка вычислений недоступна; конвертируйте в GPT через mbr2gpt", "Legacy+MBR — 算力解锁不可用, 需 mbr2gpt 转 GPT")}[legacy]})
	sbOn := hxcore.SecureBootOn()
	items = append(items, statusItem{"Secure Boot", !sbOn,
		map[bool]string{true: tr("on (must be disabled!)", "включён (нужно отключить!)", "开启(需关闭!)"), false: tr("off (OK)", "выкл (OK)", "关闭 (OK)")}[sbOn]})
	gpuOK := hxcore.FindGPU()
	items = append(items, statusItem{tr("40HX GPU", "Видеокарта 40HX", "40HX 显卡"), gpuOK,
		map[bool]string{true: tr("detected (VEN_10DE&DEV_1F0B)", "обнаружена (VEN_10DE&DEV_1F0B)", "已检测到 (VEN_10DE&DEV_1F0B)"), false: tr("not detected — make sure it is seated and its driver is installed", "не обнаружена — убедитесь, что она установлена и драйвер стоит", "未检测到 — 确认插好且驱动已装")}[gpuOK]})
	gspOK := hxcore.GspEnabled()
	items = append(items, statusItem{"GSP (EnableGpuFirmware)", gspOK,
		map[bool]string{true: tr("enabled (OK)", "включён (OK)", "已启用 (OK)"), false: tr("not enabled — the screen may go black (Code43) after unlocking", "не включён — после разблокировки возможен чёрный экран (Code43)", "未启用 — 解锁后可能 Code43 黑屏")}[gspOK]})

	espEFI := false
	if esp := hxcore.MountESP(); esp != "" {
		if _, err := os.Stat(esp + ":\\EFI\\40HX\\40HXUNLK.EFI"); err == nil {
			espEFI = true
		}
		hxcore.UnmountESP(esp)
	}
	items = append(items, statusItem{tr("ESP unlock EFI", "EFI разблокировки на ESP", "ESP 解锁 EFI"), espEFI,
		map[bool]string{true: tr("\\EFI\\40HX\\40HXUNLK.EFI deployed", "\\EFI\\40HX\\40HXUNLK.EFI развёрнут", "\\EFI\\40HX\\40HXUNLK.EFI 已部署"), false: tr("not deployed (expected on Legacy machines)", "не развёрнут (ожидаемо на Legacy-машинах)", "未部署 (Legacy 机器属预期)")}[espEFI]})
	// 启动项三态(与安装器 verifyBootEntry 同口径): 首位 / 存在但不在首位 / 未创建
	bootOK := false
	bootNote := tr("not created", "не создана", "未创建")
	if ex, first, ord := verifyBootEntry(); ex {
		if first {
			bootOK = true
			bootNote = tr("present and first in displayorder", "присутствует и первая в displayorder", "存在且 displayorder 首位")
		} else {
			bootNote = tr("present but not first in displayorder (current order: ", "присутствует, но не первый в displayorder (текущий порядок: ", "存在但不在 displayorder 首位(当前顺序: ") + ord + tr(") — set it first in BIOS", ") — поставьте первой в BIOS", ") — 需进 BIOS 置顶")
		}
	}
	items = append(items, statusItem{tr("Firmware boot entry", "Запись загрузки прошивки", "固件启动项"), bootOK, bootNote})

	taskOK, taskStatus, taskResult := hxcore.TaskInfo(gen2TaskName)
	taskNote := tr("not registered — Gen2 will not run automatically at boot", "не зарегистрирована — Gen2 не будет запускаться автоматически при загрузке", "未注册 — Gen2 不会开机自动跑")
	if taskOK {
		taskNote = tr("status ", "статус ", "状态 ") + taskStatus + tr(" last result ", " последний результат ", " 上次结果 ") + taskResult
	}
	items = append(items, statusItem{tr("Gen2 login task", "Задача входа Gen2", "Gen2 登录任务"), taskOK, taskNote})
	rkOK := runKeyPresent()
	items = append(items, statusItem{tr("Gen2 Run-key fallback", "Резерв через ключ Run для Gen2", "Gen2 Run 键兜底"), rkOK,
		map[bool]string{true: tr("written (40HXGen2)", "записан (40HXGen2)", "已写入 (40HXGen2)"), false: tr("not written", "не записан", "未写入")}[rkOK]})

	// Gen2 驱动分层状态 — 判定见 hxcore/drvstate.go。
	// S0"用完即卸"/S1"看门狗"成功后 System32 文件与服务被自清理 → 缺失≠没装过。
	deps := hxcore.InspectGen2Drivers()
	if !hxcore.Gen2DriversDeployedOnce() {
		items = append(items, statusItem{tr("Gen2 drivers (never deployed)", "Драйверы Gen2 (не разворачивались)", "Gen2 驱动(从未部署)"), false,
			tr("no backup source or service — tick [Deploy Gen2 drivers] below to install", "нет ни резервного источника, ни службы — отметьте [Установить драйверы Gen2] ниже", "备份源/服务均无 — 下方勾选 [Gen2 驱动部署] 安装")})
	} else {
		var notes []string
		curOK := true
		cleanEnd := true
		for _, d := range deps {
			svcS := tr("service not registered", "служба не зарегистрирована", "服务未注册")
			if d.SvcReg {
				svcS = tr("service ", "служба ", "服务 ") + d.SvcStart
				if d.SvcStart == "DISABLED" {
					svcS += tr(" ⚠disabled (Gen2 cannot start; reinstall to fix)", " ⚠отключена (Gen2 не запустится; переустановите для исправления)", " ⚠被禁用(Gen2 拉不起, 重装修复)")
				}
				if d.SvcRunning {
					svcS += tr("/running", "/работает", "/运行中")
				}
			}
			notes = append(notes, d.File+": System32="+d.SysState.String()+", "+svcS)
			if d.SysState != hxcore.DrvOk || !d.SvcReg || d.SvcStart == "DISABLED" {
				curOK = false
			}
			if d.SvcReg || d.SysState != hxcore.DrvAbsent {
				cleanEnd = false
			}
		}
		if cleanEnd && hxcore.DriverStrategy() != hxcore.DriverStrategyResident {
			items = append(items, statusItem{tr("Gen2 drivers (unload-after-use final state)", "Драйверы Gen2 (конечное состояние: выгружены)", "Gen2 驱动(用完即卸终态)"), true,
				tr("was deployed; already self-cleaned per policy — the next login task will redeploy it automatically (normal)", "было развёрнуто; уже самоочищено по политике — следующая задача входа развернёт заново (норма)", "曾部署; 已按策略自清理 — 下次登录任务会自动重部署(属正常)")})
		} else {
			items = append(items, statusItem{tr("Gen2 driver deployment status", "Статус развёртывания драйверов Gen2", "Gen2 驱动部署状态"), curOK, strings.Join(notes, " | ")})
		}
	}
	if exOK, err := hxcore.DefenderExclusionsPresent(); err != nil {
		items = append(items, statusItem{tr("Defender exclusions", "Исключения Defender", "Defender 排除"), false,
			tr("query failed (", "сбой запроса (", "查询失败(") + err.Error() + tr(") — rescan as administrator or ignore", ") — пересканируйте от имени администратора или проигнорируйте", ") — 以管理员重扫或忽略")})
	} else {
		items = append(items, statusItem{tr("Defender exclusions", "Исключения Defender", "Defender 排除"), exOK,
			map[bool]string{true: tr("both .sys files + the ProgramData backup dir are whitelisted", "оба файла .sys + каталог резервов в ProgramData в белом списке", "两个 .sys + ProgramData 备份目录均已加白"), false: tr("not whitelisted — antivirus may delete the drivers by mistake (reinstall to re-add)", "не в белом списке — антивирус может по ошибке удалить драйверы (переустановите)", "未加白 — 杀软可能误删驱动(重装补)")}[exOK]})
	}

	fsOn := hxcore.FastStartupOn()
	items = append(items, statusItem{tr("Fast Startup", "Быстрый запуск", "快速启动"), !fsOn,
		map[bool]string{true: tr("on (recommended off — the EFI may not run)", "включён (рекомендуется отключить — EFI может не запуститься)", "开启(建议关闭 — EFI 可能不跑)"), false: tr("off (OK)", "выкл (OK)", "关闭 (OK)")}[fsOn]})
	if ac, dc, aspmOK := hxcore.ASPMSavings(); aspmOK {
		off := ac == 0 && dc == 0
		items = append(items, statusItem{"PCIe ASPM", off,
			map[bool]string{true: tr("off (OK)", "выкл (OK)", "关闭 (OK)"), false: fmt.Sprintf(tr("on (AC=%d DC=%d) — may drop to Gen1 when idle", "включено (AC=%d DC=%d) — при простое возможно снижение до Gen1", "开启(AC=%d DC=%d) — 空闲可能降速 Gen1"), ac, dc)}[off]})
	}
	return items
}

func runKeyPresent() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Run`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("40HXGen2")
	return err == nil
}

// ---- 日志面板写入器 (AttachLogSink 目标) ----

type guiLog struct {
	mw *walk.MainWindow
	te *walk.TextEdit
}

func (g *guiLog) Write(p []byte) (int, error) {
	s := string(p)
	if g.mw != nil && g.te != nil {
		// EDIT 控件换行需要 CRLF: 统一把 \n 规范成 \r\n, 否则日志会挤成一段
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "\n")
		s = strings.ReplaceAll(s, "\n", "\r\n")
		g.mw.Synchronize(func() { g.te.AppendText(s) })
	}
	return len(p), nil
}

// ---- GUI 状态 ----

type guiState struct {
	mw                                                           *walk.MainWindow
	log                                                          *guiLog
	teLog                                                        *walk.TextEdit
	tip                                                          *walk.Label
	langBox                                                      *walk.ComboBox
	langLabel                                                    *walk.Label
	gbInstall, gbPolicy, gbLog                                   *walk.GroupBox
	lblPolicy, lblPolicyModes, lblRetry, lblRetrySep, lblMinutes *walk.Label
	busyBy                                                       string // 当前占用互斥的操作名(""=空闲)。只在 UI 线程读写:
	// begin() 在 OnClicked(UI线程) 调用, end() 经 sync 回到 UI 线程 → 无线程竞争。
	lastScan    []statusItem
	lastGuide   string // 上次打印的环境指引(变化才打印, 防重扫刷屏)
	lastAV      string // 上次识别到的第三方杀软(同上)
	lastDefWarn string // "无 Defender 模块"提示去重

	ckGsp, ckDrv, ckEfi, ckTask      *walk.CheckBox
	ckFast, ckAspm, ckPerf, ckDefOff *walk.CheckBox
	ckRebar                          *walk.CheckBox
	pbInstall, pbFull                *walk.PushButton

	rbStrategy                    [3]*walk.RadioButton
	ckAutoHard                    *walk.CheckBox
	neRetryCnt                    *walk.NumberEdit
	neRetryMin                    *walk.NumberEdit
	pbSave, pbGen2, pbGen2Install *walk.PushButton
}

func (st *guiState) sync(f func()) {
	if st.mw != nil {
		st.mw.Synchronize(f)
	} else {
		f()
	}
}

// refreshTexts updates controls that are visible in the main window. The
// language selector is intentionally live so users do not need to restart the
// installer just to switch between English, Russian, and Chinese.
func (st *guiState) refreshTexts() {
	if st.mw == nil {
		return
	}
	st.mw.SetTitle(tr("CMP 40HX Unlock Manager "+hxcore.Version, "Менеджер разблокировки CMP 40HX "+hxcore.Version, "CMP 40HX 解锁管理器 "+hxcore.Version))
	if st.langLabel != nil {
		st.langLabel.SetText(tr("Language:", "Язык:", "语言："))
	}
	if st.gbInstall != nil {
		st.gbInstall.SetTitle(tr("1. Components and environment (missing items are preselected)", "1. Компоненты и окружение (недостающие пункты выбраны)", "① 组件安装与环境设置 (按当前状态预勾选; 勾选 = 执行/刷新)"))
	}
	if st.gbPolicy != nil {
		st.gbPolicy.SetTitle(tr("2. Gen2 policy (changes apply immediately)", "2. Политика Gen2 (изменения применяются сразу)", "② Gen2 策略 (保存即生效; 登录任务与 -gen2 读取, 详见 README §2.5)"))
	}
	if st.gbLog != nil {
		st.gbLog.SetTitle(tr("3. Operation log (live)", "3. Журнал операций (онлайн)", "③ 操作日志 (实时)"))
	}
	if st.tip != nil && len(st.lastScan) == 0 {
		st.tip.SetText(tr("Scanning environment...", "Проверка окружения...", "正在扫描环境…"))
	}
	if st.ckGsp != nil {
		st.ckGsp.SetText(tr("Enable GSP (EnableGpuFirmware=1)", "Включить GSP (EnableGpuFirmware=1)", "GSP 启用 (EnableGpuFirmware=1)"))
		st.ckEfi.SetText(tr("Compute EFI + firmware boot entry", "EFI разблокировки + запись загрузки", "算力 EFI + 固件启动项"))
		st.ckRebar.SetText(tr("ReBar Unlock (8 GB BAR1)", "Разблокировка ReBar (BAR1 8 ГБ)", "ReBar 解锁 (8 GB BAR1)"))
		st.ckDrv.SetText(tr("Deploy Gen2 drivers + Defender exclusions", "Установить драйверы Gen2 + исключения Defender", "Gen2 驱动部署 + Defender 排除"))
		st.ckTask.SetText(tr("Gen2 login auto-start", "Автозапуск Gen2 при входе", "Gen2 登录自启"))
		st.ckFast.SetText(tr("Power: disable Fast Startup", "Питание: отключить быстрый запуск", "电源: 关闭快速启动"))
		st.ckAspm.SetText(tr("Power: disable PCIe link power saving", "Питание: отключить энергосбережение PCIe", "电源: 关闭 PCIe 链路省电"))
		st.ckPerf.SetText(tr("Power: high-performance plan", "Питание: план высокой производительности", "电源: 高性能电源计划"))
		st.ckDefOff.SetText(tr("Disable Defender real-time protection", "Отключить защиту Defender в реальном времени", "关闭 Defender 实时防护"))
	}
	if st.pbInstall != nil {
		st.pbInstall.SetText(tr("Install selected", "Установить выбранное", "安装所选组件"))
		st.pbFull.SetText(tr("Full installation", "Полная установка", "一键完整安装 (全流程)"))
		st.pbSave.SetText(tr("Save policy", "Сохранить политику", "保存策略"))
		st.pbGen2.SetText(tr("Run Gen2 now (this run only)", "Запустить Gen2 сейчас (только этот запуск)", "立即执行 Gen2 (仅本次解锁)"))
		st.pbGen2Install.SetText(tr("Run Gen2 and install auto-start", "Запустить Gen2 и установить автозапуск", "执行 Gen2 并安装自启 (本次+开机自动)"))
	}
	if st.lblPolicy != nil {
		st.lblPolicy.SetText(tr("Driver policy: how the two Gen2 drivers are handled after unlocking", "Политика драйверов: что делать с двумя драйверами Gen2 после разблокировки", "驱动策略: Gen2 解锁用的两个驱动, 跑完后怎么处理"))
	}
	if st.lblPolicyModes != nil {
		st.lblPolicyModes.SetText(tr("1. Unload after use (default)   2. Retry on failure   3. Resident watchdog", "1. Выгружать после работы (по умолчанию)   2. Повторять при ошибке   3. Постоянный watchdog", "① 用完即卸(默认, 每次自动清理, 游戏/反作弊最干净)   ② 失败自动重试   ③ 常驻守护(驱动保留, 每分钟自查 Gen2, TLS 丢失自动重训)"))
	}
	if st.rbStrategy[0] != nil {
		st.rbStrategy[0].SetText(tr("Unload after use (default)", "Выгружать после использования (по умолчанию)", "用完即卸 (默认/推荐)"))
		st.rbStrategy[1].SetText(tr("Retry on failure", "Повторять при ошибке", "失败自动重试"))
		st.rbStrategy[2].SetText(tr("Resident watchdog", "Постоянный watchdog", "常驻守护 (定时看 Gen2)"))
	}
	if st.ckAutoHard != nil {
		st.ckAutoHard.SetText(tr("Automatically use Stage2 fallback when Gen2 is not achieved (Link Disable + PnP recovery)", "Автоматически использовать Stage2, если Gen2 не достигнут (Link Disable + восстановление PnP)", "Gen2 未达成时自动执行 Stage2 回退 (Link Disable + PnP 恢复; 关掉可避免唯一显示卡登录后瞬断数秒)"))
	}
	if st.lblRetry != nil {
		st.lblRetry.SetText(tr("Retry count:", "Повторы:", "失败自动重试:"))
	}
	if st.lblRetrySep != nil {
		st.lblRetrySep.SetText(tr(" / interval:", " / интервал:", "次 / 间隔:"))
	}
	if st.lblMinutes != nil {
		st.lblMinutes.SetText(tr("minutes", "мин", "分钟"))
	}
}

// begin: 在 UI 线程(OnClicked)同步抢全局互斥 — 一次只允许一个长操作在跑。
// 抢到即占住(busyBy)并同步禁用按钮, 杜绝"连点/快速双击"在 goroutine 里抢锁的竞态。
func (st *guiState) begin(what string) bool {
	if st.busyBy != "" {
		fmt.Println(tr("[!] Running ", "[!] Выполняется ", "[!] 正在执行 ") + st.busyBy + " — " + what + tr(" was skipped; wait for it to finish and try again", " пропущено; дождитесь завершения и повторите", " 已跳过, 请等它完成后再试"))
		return false
	}
	st.busyBy = what
	return true
}

func (st *guiState) end() {
	st.sync(func() { st.busyBy = "" })
}

// setActionsEnabled: 初始自动扫描期间禁用执行按钮, 防止与手动操作并发抢 IO。
func (st *guiState) setActionsEnabled(on bool) {
	st.sync(func() {
		for _, b := range []*walk.PushButton{st.pbInstall, st.pbFull, st.pbSave, st.pbGen2} {
			if b != nil {
				b.SetEnabled(on)
			}
		}
	})
}

// summaryText: 把"不处理就装不上/装了也不生效"的最关键状态合成安装区顶部提示。
func (st *guiState) summaryText(items []statusItem) string {
	m := map[string]statusItem{}
	for _, it := range items {
		m[it.name] = it
	}
	if it, ok := m[tr("40HX GPU", "Видеокарта 40HX", "40HX 显卡")]; ok && !it.ok {
		return tr("WARNING: CMP 40HX not detected - check the card and driver before installing", "ВНИМАНИЕ: CMP 40HX не обнаружена - проверьте карту и драйвер перед установкой", "⚠ 未检测到 40HX — 请先确认显卡插好且驱动已装, 否则安装无意义")
	}
	var warns []string
	if it, ok := m["Secure Boot"]; ok && !it.ok {
		warns = append(warns, tr("Secure Boot is enabled; disable it in BIOS", "Secure Boot включен; отключите его в BIOS", "Secure Boot 开启, 需进 BIOS 关闭"))
	}
	if it, ok := m[tr("Boot mode", "Режим загрузки", "引导模式")]; ok && !it.ok {
		warns = append(warns, tr("Legacy+MBR boot; convert the disk with mbr2gpt before deploying the compute EFI", "Загрузка Legacy+MBR; перед установкой compute EFI конвертируйте диск через mbr2gpt", "Legacy+MBR 引导, 算力 EFI 装不上(需 mbr2gpt 转 GPT)"))
	}
	if it, ok := m[tr("Gen2 drivers (never deployed)", "Драйверы Gen2 (не разворачивались)", "Gen2 驱动(从未部署)")]; ok && !it.ok {
		warns = append(warns, tr("Gen2 drivers have not been deployed", "Драйверы Gen2 еще не установлены", "Gen2 驱动从未部署"))
	}
	if it, ok := m[tr("Gen2 login task", "Задача входа Gen2", "Gen2 登录任务")]; ok && !it.ok {
		warns = append(warns, tr("Gen2 login auto-start is not registered", "Автозапуск Gen2 при входе не зарегистрирован", "Gen2 登录自启未注册"))
	}
	// 启动项: 仅 UEFI 下检查(存在但不在首位 / 未创建)
	if it, ok := m[tr("Firmware boot entry", "Запись загрузки прошивки", "固件启动项")]; ok && !it.ok {
		if bl, ok2 := m[tr("Boot mode", "Режим загрузки", "引导模式")]; !ok2 || bl.ok {
			if strings.Contains(it.note, tr("not first", "не первый", "不在")) {
				warns = append(warns, tr("The boot entry exists but is not first; move it to the top in BIOS", "Запись загрузки есть, но не первая; поднимите ее на первое место в BIOS", "启动项存在但不在首位, 需 BIOS 置顶"))
			} else {
				warns = append(warns, tr("Firmware boot entry is missing", "Запись загрузки прошивки отсутствует", "固件启动项未创建"))
			}
		}
	}
	if len(warns) == 0 {
		return tr("Environment ready - missing components are preselected; choose Install selected or Full installation", "Окружение готово - недостающие компоненты выбраны; нажмите Установить выбранное или Полная установка", "✓ 环境就绪 — 缺失组件已自动预勾, 点[安装所选组件]或[一键完整安装]即可")
	}
	s := "⚠ " + strings.Join(warns, "; ")
	if r := []rune(s); len(r) > 90 {
		s = string(r[:90]) + "…"
	}
	return s
}

// envGuide: "已知问题 → 解决步骤"的安装指引(参照 v2.4 弹窗文案的引导风格,
// 只输出当前确实存在的问题对应的处理步骤, 供日志阅读)。
func (st *guiState) envGuide(items []statusItem) string {
	m := map[string]statusItem{}
	for _, it := range items {
		m[it.name] = it
	}
	var g []string
	if it, ok := m[tr("40HX GPU", "Видеокарта 40HX", "40HX 显卡")]; ok && !it.ok {
		g = append(g, tr("· 40HX not detected: ①check power and that the PCIe slot is seated; ②check Device Manager for Code43 (install the driver first if missing); ③disable CSM in BIOS (pure UEFI), then rescan", "· 40HX не обнаружена: ①проверьте питание и посадку в слоте PCIe; ②проверьте Диспетчер устройств на Code43 (сначала установите драйвер, если его нет); ③отключите CSM в BIOS (чистый UEFI) и пересканируйте", "· 未检测到 40HX: ①确认供电与 PCIe 插稳; ②设备管理器看是否有 code43(未装驱动先装); ③BIOS 关 CSM(纯 UEFI)后再扫"))
	}
	if it, ok := m["Secure Boot"]; ok && !it.ok {
		g = append(g, tr("· Secure Boot is on: reboot, press Del/F2 to enter BIOS → Security/Boot → Secure Boot=Disabled → F10 to save → return to the OS and re-run this tool", "· Secure Boot включён: перезагрузитесь, нажмите Del/F2 для входа в BIOS → Security/Boot → Secure Boot=Disabled → F10 для сохранения → вернитесь в ОС и перезапустите инструмент", "· Secure Boot 开启: 重启按 Del/F2 进 BIOS → Security/Boot → Secure Boot=Disabled → F10 保存 → 回系统重跑本工具"))
	}
	if it, ok := m[tr("Boot mode", "Режим загрузки", "引导模式")]; ok && !it.ok {
		g = append(g, tr("· Legacy+MBR boot: without an EFI partition the compute unlock cannot be installed → in an admin CMD run in order: mbr2gpt /validate /allowfullos → mbr2gpt /convert /allowfullos → reboot into UEFI (disable CSM) → re-run this tool (full steps in README §2.4)", "· Загрузка Legacy+MBR: без раздела EFI разблокировку вычислений установить нельзя → в CMD от администратора выполните по порядку: mbr2gpt /validate /allowfullos → mbr2gpt /convert /allowfullos → перезагрузитесь в UEFI (отключите CSM) → перезапустите инструмент (полные шаги в README §2.4)", "· Legacy+MBR 引导: 无 EFI 分区算力解锁装不上 → 管理员 CMD 依次: mbr2gpt /validate /allowfullos → mbr2gpt /convert /allowfullos → 重启改 UEFI(关 CSM) → 重跑本工具(完整步骤见 README §2.4)"))
	}
	if it, ok := m[tr("Firmware boot entry", "Запись загрузки прошивки", "固件启动项")]; ok && !it.ok {
		if bl, ok2 := m[tr("Boot mode", "Режим загрузки", "引导模式")]; !ok2 || bl.ok {
			if strings.Contains(it.note, tr("not first", "не первый", "不在")) {
				g = append(g, tr("· A boot entry exists but is not first in displayorder: enter the BIOS and set '40HX Unlock' as the first boot entry (otherwise it may not run at startup)", "· Запись загрузки существует, но не первая в displayorder: войдите в BIOS и сделайте '40HX Unlock' первой записью загрузки (иначе при старте может не выполниться)", "· 启动项存在但不在 displayorder 首位: 进 BIOS 把 '40HX Unlock' 设为第一启动项(否则开机可能不执行)"))
			} else {
				g = append(g, tr("· Firmware boot entry not created: tick [Compute EFI + firmware boot entry] above to create and pin it automatically; if the BIOS list still does not show it, repair the boot with PE (firPE)/DiskGenius or manually set the UEFI disk as the first boot (uses the bootx64 fallback)", "· Запись загрузки прошивки не создана: отметьте [EFI разблокировки + запись загрузки] выше, чтобы создать и поднять её автоматически; если список BIOS всё равно её не показывает, восстановите загрузку через PE (firPE)/DiskGenius или вручную сделайте диск UEFI первым в загрузке (сработает резерв bootx64)", "· 固件启动项未创建: 勾选上方[算力 EFI 部署 + 固件启动项]即可自动创建并置顶; 若 BIOS 列表仍不显示, 用 PE(firPE)/DiskGenius 修复引导或手动把 UEFI 盘设为首启(走 bootx64 兜底)"))
			}
		}
	}
	if it, ok := m[tr("Gen2 drivers (never deployed)", "Драйверы Gen2 (не разворачивались)", "Gen2 驱动(从未部署)")]; ok && !it.ok {
		g = append(g, tr("· Gen2 drivers never deployed: tick [Deploy Gen2 drivers + Defender exclusions] to install (it whitelists them automatically and handles third-party antivirus trust prompts)", "· Драйверы Gen2 не разворачивались: отметьте [Установить драйверы Gen2 + исключения Defender] (они автоматически добавятся в белый список, а подсказки доверия стороннего антивируса будут обработаны)", "· Gen2 驱动从未部署: 勾选[Gen2 驱动部署 + Defender 排除]安装(会自动加白并处理第三方杀软信任提示)"))
	}
	if it, ok := m[tr("Gen2 login task", "Задача входа Gen2", "Gen2 登录任务")]; ok && !it.ok {
		g = append(g, tr("· Gen2 login auto-start not registered: tick [Gen2 login auto-start] to install; or run 40HXInstaller.exe -task as administrator", "· Автозапуск Gen2 при входе не зарегистрирован: отметьте [Автозапуск Gen2 при входе]; или запустите 40HXInstaller.exe -task от администратора", "· Gen2 登录自启未注册: 勾选[Gen2 登录自启]安装; 或管理员运行 40HXInstaller.exe -task"))
	}
	if it, ok := m[tr("ESP unlock EFI", "EFI разблокировки на ESP", "ESP 解锁 EFI")]; ok && !it.ok {
		if bl, ok2 := m[tr("Boot mode", "Режим загрузки", "引导模式")]; !ok2 || bl.ok {
			g = append(g, tr("· Compute unlock EFI not deployed (if you just uninstalled or are not installing the compute unlock for now: this is expected — the compute lock will not clear on its own, but Gen2 is unaffected; to restore the compute unlock, tick [Compute EFI + firmware boot entry] above)", "· EFI разблокировки вычислений не развёрнут (если вы только что удалили или пока не ставите разблокировку вычислений: это ожидаемо — блокировка вычислений сама не снимется, но на Gen2 это не влияет; чтобы восстановить разблокировку, отметьте [EFI разблокировки + запись загрузки] выше)", "· 算力解锁 EFI 未部署(若你是刚卸载/暂不装算力: 属预期 — 算力锁不会自动解, Gen2 不受影响; 想恢复算力解锁就勾上方 [算力 EFI 部署 + 固件启动项] 安装)"))
		}
	}
	return strings.Join(g, "\n")
}

// scanOnce: 只读扫描一次并刷新顶部提示(不展示明细)。
// 同时输出: 环境处理指引(已知问题→解决步骤)与第三方杀软报告 — 仅在内容变化时打印。
func (st *guiState) scanOnce() {
	items := scanStatus()
	st.lastScan = items
	tip := st.summaryText(items)
	st.sync(func() {
		if st.tip != nil {
			st.tip.SetText(tip)
		}
	})
	// 第三方杀软探测(只读): 它不读 Defender 排除列表, 需手动放行
	av := hxcore.DetectThirdPartyAV()
	avKey := strings.Join(av, ",")
	if avKey != st.lastAV {
		if len(av) > 0 {
			fmt.Println(tr("[AV] Detected third-party security software: ", "[AV] Обнаружено стороннее защитное ПО: ", "[杀软] 检测到第三方安全软件: ") + strings.Join(av, " / ") +
				tr(" — please allow these 4 driver paths in its trust/whitelist:", " — разрешите эти 4 пути драйверов в его списке доверия/белом списке:", " — 请在信任/白名单放行 4 个驱动路径:"))
			fmt.Println("        C:\\Windows\\System32\\drivers\\ThrottleStop.sys")
			fmt.Println("        C:\\Windows\\System32\\drivers\\WinRing0x64.sys")
			fmt.Println(tr("        ThrottleStop.sys / WinRing0x64.sys under %ProgramData%\\40HXUnlock\\drivers\\", "        ThrottleStop.sys / WinRing0x64.sys в %ProgramData%\\40HXUnlock\\drivers\\", "        %ProgramData%\\40HXUnlock\\drivers\\ 下的 ThrottleStop.sys / WinRing0x64.sys"))
		}
		st.lastAV = avKey
	}
}

// applySmartDefaults: 按最近一次扫描预勾选 — 组件缺失/未达标才勾(已装不勾=不覆盖)。
func (st *guiState) applySmartDefaults() {
	items := st.lastScan
	if len(items) == 0 {
		items = scanStatus()
		st.lastScan = items
	}
	flags := map[string]bool{}
	for _, it := range items {
		flags[it.name] = it.ok
	}
	if !flags[tr("40HX GPU", "Видеокарта 40HX", "40HX 显卡")] {
		fmt.Println(tr("[i] 40HX not detected — leaving the install section fully unchecked (confirm the GPU/driver first)", "[i] 40HX не обнаружена — раздел установки оставлен без отметок (сначала проверьте видеокарту/драйвер)", "[i] 未检测到 40HX — 安装区保持全不勾(请先确认显卡/驱动)"))
		st.sync(func() {
			st.ckGsp.SetChecked(false)
			st.ckDrv.SetChecked(false)
			st.ckEfi.SetChecked(false)
			st.ckRebar.SetChecked(false)
			st.ckTask.SetChecked(false)
			st.ckFast.SetChecked(false)
			st.ckAspm.SetChecked(false)
			st.ckPerf.SetChecked(false)
			st.ckDefOff.SetChecked(false)
		})
		return
	}
	aspmOK := true
	if v, present := flags["PCIe ASPM"]; present {
		aspmOK = v
	}
	needGsp := !flags["GSP (EnableGpuFirmware)"]
	// 驱动部署需要与否不能只看 System32 文件(用完即卸终态会缺失):
	// 从未部署 / 服务被 DISABLED / 文件 0字节或与备份不一致 → 才需要装
	needDrv := hxcore.Gen2DriversNeedDeploy()
	needEfi := false
	if flags[tr("Boot mode", "Режим загрузки", "引导模式")] {
		needEfi = !flags[tr("ESP unlock EFI", "EFI разблокировки на ESP", "ESP 解锁 EFI")] || !flags[tr("Firmware boot entry", "Запись загрузки прошивки", "固件启动项")]
	} else {
		fmt.Println(tr("[i] Legacy+MBR boot: the compute EFI cannot be installed — not preselected (convert to GPT with mbr2gpt first)", "[i] Загрузка Legacy+MBR: compute EFI установить нельзя — не выбрано (сначала конвертируйте в GPT через mbr2gpt)", "[i] Legacy+MBR 引导: 算力 EFI 不可装 — 未预勾(需先 mbr2gpt 转 GPT)"))
	}
	needTask := !flags[tr("Gen2 login task", "Задача входа Gen2", "Gen2 登录任务")]
	needFast := !flags[tr("Fast Startup", "Быстрый запуск", "快速启动")]
	needAspm := !aspmOK
	needPerf := !hxcore.HighPerfPlanActive()
	// Defender 实时防护: 开着→预勾(如实); 已关→不勾; 模块缺失/查不到→不勾+简短提示一次
	needDefOff, defKnown := false, false
	if on, err := hxcore.DefenderRealtimeProtectionOn(); err == nil {
		defKnown = true
		needDefOff = on
	} else {
		defWarn := tr("This machine has no Defender management module (for third-party antivirus, allow the drivers in its trust list)", "На этой машине нет модуля управления Defender (для стороннего антивируса разрешите драйверы в его списке доверия)", "本机无 Defender 管理模块(第三方杀软请在其信任列表放行驱动)")
		if !errors.Is(err, hxcore.ErrMpUnavailable) {
			defWarn = tr("Defender status query failed: ", "Сбой запроса статуса Defender: ", "Defender 状态查询失败: ") + err.Error()
		}
		if defWarn != st.lastDefWarn {
			fmt.Println("[i] " + defWarn)
			st.lastDefWarn = defWarn
		}
	}
	var pre []string
	st.sync(func() {
		// 显式双向设置: 缺失/未达标 → 勾(待执行); 已就绪 → 取消勾(不覆盖)。
		// v3.0.0 修复: 旧实现只 SetChecked(true), 装完重扫后已就绪项仍保持勾选。
		st.ckGsp.SetChecked(needGsp)
		if needGsp {
			pre = append(pre, "GSP")
		}
		st.ckDrv.SetChecked(needDrv)
		if needDrv {
			pre = append(pre, tr("Gen2 drivers", "Драйверы Gen2", "Gen2 驱动"))
		}
		st.ckEfi.SetChecked(needEfi)
		if needEfi {
			pre = append(pre, tr("Compute EFI+boot entry", "Compute EFI+запись загрузки", "算力 EFI+启动项"))
		}
		// ReBAR is applied by that same unlock EFI at boot. Preselect it exactly
		// when the EFI/boot entry is being (re)deployed so a fresh install turns
		// it on; when the entry is already in place we leave it unchecked (no
		// re-assert). Full installation always re-enables it regardless.
		st.ckRebar.SetChecked(needEfi)
		if needEfi {
			pre = append(pre, tr("ReBar 8 GB", "ReBar 8 ГБ", "ReBar 8 GB"))
		}
		st.ckTask.SetChecked(needTask)
		if needTask {
			pre = append(pre, tr("Gen2 login auto-start", "Автозапуск Gen2 при входе", "Gen2 登录自启"))
		}
		st.ckFast.SetChecked(needFast)
		if needFast {
			pre = append(pre, tr("disable Fast Startup", "откл. быстрый запуск", "关快速启动"))
		}
		st.ckAspm.SetChecked(needAspm)
		if needAspm {
			pre = append(pre, tr("disable ASPM", "откл. ASPM", "关ASPM"))
		}
		st.ckPerf.SetChecked(needPerf)
		if needPerf {
			pre = append(pre, tr("high-performance plan", "план высокой производительности", "高性能计划"))
		}
		wantDefOff := defKnown && needDefOff
		st.ckDefOff.SetChecked(wantDefOff)
		if wantDefOff {
			pre = append(pre, tr("disable Defender real-time protection", "откл. защиту Defender", "关 Defender 实时防护"))
		}
		// 勾选与否由上面扫描判定; 状态与信任/白名单说明只进日志, 不上 UI 标签
	})
	if len(pre) > 0 {
		fmt.Println(tr("[i] Preselected: ", "[i] Выбрано: ", "[i] 预勾: ") + strings.Join(pre, " / ") + tr(" → click [Install selected] to run; the rest are ready and left unchecked (not overwritten)", " → нажмите [Установить выбранное] для выполнения; остальное готово и не отмечено (не перезаписывается)", " → 点[安装所选组件]执行; 其余已就绪不勾(不覆盖)"))
		return
	}
	// 全都没勾: 一行列出原因(均已就绪, 勾了会覆盖/刷新)
	var ready []string
	if !needGsp {
		ready = append(ready, tr("GSP enabled", "GSP включён", "GSP 已启用"))
	}
	if !needDrv {
		ready = append(ready, tr("Gen2 drivers ready", "Драйверы Gen2 готовы", "Gen2 驱动已就绪"))
	}
	if !flags[tr("Boot mode", "Режим загрузки", "引导模式")] {
		ready = append(ready, tr("compute EFI (mbr2gpt first)", "compute EFI (сначала mbr2gpt)", "算力 EFI(先 mbr2gpt)"))
	} else if !needEfi {
		ready = append(ready, tr("EFI+boot entry ready", "EFI+запись загрузки готовы", "EFI+启动项已就绪"))
	}
	if !needTask {
		ready = append(ready, tr("auto-start registered", "автозапуск зарегистрирован", "自启已注册"))
	}
	if !needFast {
		ready = append(ready, tr("Fast Startup off", "быстрый запуск выкл", "快速启动已关"))
	}
	if !needAspm {
		ready = append(ready, tr("ASPM off", "ASPM выкл", "ASPM 已关"))
	}
	if !needPerf {
		ready = append(ready, tr("already high-performance plan", "уже план высокой производительности", "已是高性能计划"))
	}
	if defKnown && !needDefOff {
		ready = append(ready, tr("Defender real-time protection off", "защита Defender выкл", "Defender 实时防护已关"))
	}
	fmt.Println(tr("[i] All ready, nothing selected (not overwritten): ", "[i] Всё готово, ничего не выбрано (не перезаписывается): ", "[i] 均已就绪, 未勾选(不覆盖): ") + strings.Join(ready, " | "))
}

// printDefErr: Defender 相关错误的简短呈现 — 模块缺失给固定短句+放行路径, 其余原样输出。
func (st *guiState) printDefErr(prefix string, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, hxcore.ErrMpUnavailable) {
		fmt.Println(prefix + tr("This machine has no Defender management module — automatic whitelisting / real-time-protection toggling is unavailable", "На этой машине нет модуля управления Defender — автоматическое добавление в белый список / переключение защиты недоступно", "本机无 Defender 管理模块 — 自动加白/实时防护开关不可用"))
		fmt.Println(prefix + tr("For third-party antivirus, allow these in its trust/whitelist:", "Для стороннего антивируса разрешите это в его списке доверия/белом списке:", "第三方杀软请在其信任/白名单放行:"))
		fmt.Println("      C:\\Windows\\System32\\drivers\\ThrottleStop.sys / WinRing0x64.sys")
		fmt.Println(tr("      the two same-named .sys files under %ProgramData%\\40HXUnlock\\drivers\\", "      два одноимённых файла .sys в %ProgramData%\\40HXUnlock\\drivers\\", "      %ProgramData%\\40HXUnlock\\drivers\\ 下同名两个 .sys"))
		return
	}
	fmt.Println(prefix + err.Error())
}

// loadPolicyUI: 启动时回读当前策略(须在 UI 线程调用 — Create 之后、Run 之前)
func (st *guiState) loadPolicyUI() {
	strat := hxcore.DriverStrategy()
	for i, rb := range st.rbStrategy {
		rb.SetChecked(i == strat)
	}
	st.ckAutoHard.SetChecked(hxcore.ConfigInt("Gen2AutoHard", 1) != 0)
	cnt, interval := hxcore.Gen2RetryPolicy()
	st.neRetryCnt.SetValue(float64(cnt))
	st.neRetryMin.SetValue(float64(interval))
}

// savePolicy: Gen2 策略落盘 (HKLM\SOFTWARE\40HXUnlock, -gen2 登录任务读取)
func (st *guiState) savePolicy() {
	defer st.end()
	strat := 0
	for i, rb := range st.rbStrategy {
		if rb.Checked() {
			strat = i
		}
	}
	if err := hxcore.SetConfigInt("DriverStrategy", strat); err != nil {
		fmt.Println(tr("[Policy] Save failed:", "[Политика] Сбой сохранения:", "[策略] 保存失败:"), err)
		return
	}
	auto := 0
	if st.ckAutoHard.Checked() {
		auto = 1
	}
	cnt, interval := int(st.neRetryCnt.Value()), int(st.neRetryMin.Value())
	hxcore.SetConfigInt("Gen2AutoHard", auto)
	hxcore.SetConfigInt("Gen2RetryCount", cnt)
	hxcore.SetConfigInt("Gen2RetryIntervalMin", interval)
	fmt.Printf(tr("[Policy] Saved: driver strategy=%d Gen2AutoHard=%d retries=%d/interval=%d min (applied by the login task / -gen2)\n", "[Политика] Сохранено: стратегия драйверов=%d Gen2AutoHard=%d повторы=%d/интервал=%d мин (применяется задачей входа / -gen2)\n", "[策略] 已保存: 驱动策略=%d Gen2AutoHard=%d 重试=%d次/间隔=%d分钟 (登录任务/-gen2 生效)\n"), strat, auto, cnt, interval)
	if strat == hxcore.DriverStrategyResident {
		if ok, _, _ := hxcore.TaskInfo(gen2TaskName); !ok {
			fmt.Println(tr("[Note] The resident watchdog needs the login auto-start task: in section ① tick [Gen2 login auto-start] and click [Install selected], or use section ② [Run Gen2 and install auto-start] in one step", "[Примечание] Постоянному watchdog нужна задача автозапуска при входе: в разделе ① отметьте [Автозапуск Gen2 при входе] и нажмите [Установить выбранное], либо используйте раздел ② [Запустить Gen2 и установить автозапуск] в один шаг", "[提示] 常驻守护需登录自启任务承载: 请在 ① 区勾[Gen2 登录自启]点[安装所选组件], 或用 ② [执行 Gen2 并安装自启] 一步到位"))
		} else {
			fmt.Println(tr("[Note] Resident watchdog selected: click section ② [Run Gen2 and install auto-start] again (or ① [Gen2 login auto-start]) to refresh the task command line so it carries the watchdog argument (-guard)", "[Примечание] Выбран постоянный watchdog: нажмите раздел ② [Запустить Gen2 и установить автозапуск] ещё раз (или ① [Автозапуск Gen2 при входе]), чтобы обновить командную строку задачи с аргументом watchdog (-guard)", "[提示] 已选常驻守护: 请再点一次 ② [执行 Gen2 并安装自启](或 ① [Gen2 登录自启])刷新任务命令行, 使其携带守护参数(-guard)"))
		}
	}
}

func runGUI() {
	if !isAdmin() {
		selfElevate()
		return
	}
	st := &guiState{log: &guiLog{}}

	createErr := MainWindow{
		AssignTo: &st.mw,
		Title:    tr("CMP 40HX Unlock Manager "+hxcore.Version, "Менеджер разблокировки CMP 40HX "+hxcore.Version, "CMP 40HX 解锁管理器 "+hxcore.Version),
		MinSize:  Size{Width: 780, Height: 660},
		Size:     Size{Width: 860, Height: 800},
		Layout:   VBox{Spacing: 6},
		Children: []Widget{
			Composite{
				Layout: HBox{Spacing: 6},
				Children: []Widget{
					Label{AssignTo: &st.langLabel, Text: tr("Language:", "Язык:", "语言：")},
					ComboBox{AssignTo: &st.langBox, Model: []string{"English", "Русский", "中文"}, CurrentIndex: languageIndex(), OnCurrentIndexChanged: func() {
						if st.langBox == nil {
							return
						}
						// walk raises CurrentIndexChanged more than once for a
						// single pick (CBN_SELCHANGE, then CBN_SELENDOK, plus
						// one per highlighted item while the list is open), so
						// only react when the language really changed.
						if !setLanguage(st.langBox.CurrentIndex()) {
							return
						}
						st.refreshTexts()
						fmt.Println(tr("[i] Language changed.", "[i] Язык изменен.", "[i] 语言已切换。"))
					}},
				},
			},
			GroupBox{
				AssignTo: &st.gbInstall,
				Title:    tr("1. Components and environment (missing items are preselected)", "1. Компоненты и окружение (недостающие пункты выбраны)", "① 组件安装与环境设置 (按当前状态预勾选; 勾选 = 执行/刷新)"),
				Layout:   VBox{Spacing: 4},
				Children: []Widget{
					Label{AssignTo: &st.tip, Text: tr("Scanning environment...", "Проверка окружения...", "正在扫描环境…")},
					Composite{
						Layout: Grid{Columns: 2},
						Children: []Widget{
							CheckBox{AssignTo: &st.ckGsp, Text: tr("Enable GSP (EnableGpuFirmware=1)", "Включить GSP (EnableGpuFirmware=1)", "GSP 启用 (EnableGpuFirmware=1)")},
							CheckBox{AssignTo: &st.ckEfi, Text: tr("Compute EFI + firmware boot entry", "EFI разблокировки + запись загрузки", "算力 EFI + 固件启动项")},
							CheckBox{AssignTo: &st.ckRebar, Text: tr("ReBar Unlock (8 GB BAR1)", "Разблокировка ReBar (BAR1 8 ГБ)", "ReBar 解锁 (8 GB BAR1)")},
							CheckBox{AssignTo: &st.ckDrv, Text: tr("Deploy Gen2 drivers + Defender exclusions", "Установить драйверы Gen2 + исключения Defender", "Gen2 驱动部署 + Defender 排除")},
							CheckBox{AssignTo: &st.ckTask, Text: tr("Gen2 login auto-start", "Автозапуск Gen2 при входе", "Gen2 登录自启")},
							CheckBox{AssignTo: &st.ckFast, Text: tr("Power: disable Fast Startup", "Питание: отключить быстрый запуск", "电源: 关闭快速启动")},
							CheckBox{AssignTo: &st.ckAspm, Text: tr("Power: disable PCIe link power saving", "Питание: отключить энергосбережение PCIe", "电源: 关闭 PCIe 链路省电")},
							CheckBox{AssignTo: &st.ckPerf, Text: tr("Power: high-performance plan", "Питание: план высокой производительности", "电源: 高性能电源计划")},
							CheckBox{AssignTo: &st.ckDefOff, Text: tr("Disable Defender real-time protection", "Отключить защиту Defender в реальном времени", "关闭 Defender 实时防护")},
						},
					},
					Composite{
						Layout: HBox{},
						Children: []Widget{
							PushButton{AssignTo: &st.pbInstall, Text: tr("Install selected", "Установить выбранное", "安装所选组件"), OnClicked: func() {
								// begin 在 UI 线程同步抢锁: 抢到即占住, 连点到不了这里
								if !st.begin(tr("Component install", "Установка компонентов", "组件安装")) {
									return
								}
								sel := map[string]bool{
									"gsp":    st.ckGsp.Checked(),
									"drv":    st.ckDrv.Checked(),
									"efi":    st.ckEfi.Checked(),
									"rebar":  st.ckRebar.Checked(),
									"task":   st.ckTask.Checked(),
									"fast":   st.ckFast.Checked(),
									"aspm":   st.ckAspm.Checked(),
									"perf":   st.ckPerf.Checked(),
									"defoff": st.ckDefOff.Checked(),
								}
								go st.installSelected(sel)
							}},
							PushButton{AssignTo: &st.pbFull, Text: tr("Full installation", "Полная установка", "一键完整安装 (全流程)"), OnClicked: func() {
								if !st.begin(tr("Full install", "Полная установка", "完整安装")) {
									return
								}
								go func() {
									defer st.end()
									st.sync(func() { st.pbFull.SetEnabled(false) })
									defer st.sync(func() { st.pbFull.SetEnabled(true) })
									install()
									st.scanOnce() // 装完自动重扫, 顶部提示/预勾随之更新
									st.applySmartDefaults()
								}()
							}},
						},
					},
				},
			},
			GroupBox{
				AssignTo: &st.gbPolicy,
				Title:    tr("2. Gen2 policy (changes apply immediately)", "2. Политика Gen2 (изменения применяются сразу)", "② Gen2 策略 (保存即生效; 登录任务与 -gen2 读取, 详见 README §2.5)"),
				Layout:   VBox{Spacing: 4},
				Children: []Widget{
					Label{AssignTo: &st.lblPolicy, Text: tr("Driver policy: how the two Gen2 drivers are handled after unlocking", "Политика драйверов: что делать с двумя драйверами Gen2 после разблокировки", "驱动策略: Gen2 解锁用的两个驱动, 跑完后怎么处理")},
					Label{AssignTo: &st.lblPolicyModes, Text: tr("1. Unload after use (default)   2. Retry on failure   3. Resident watchdog", "1. Выгружать после работы (по умолчанию)   2. Повторять при ошибке   3. Постоянный watchdog", "① 用完即卸(默认, 每次自动清理, 游戏/反作弊最干净)   ② 失败自动重试   ③ 常驻守护(驱动保留, 每分钟自查 Gen2, TLS 丢失自动重训)")},
					Composite{
						Layout: Grid{Columns: 3},
						Children: []Widget{
							RadioButton{AssignTo: &st.rbStrategy[0], Text: tr("Unload after use (default)", "Выгружать после использования (по умолчанию)", "用完即卸 (默认/推荐)")},
							RadioButton{AssignTo: &st.rbStrategy[1], Text: tr("Retry on failure", "Повторять при ошибке", "失败自动重试")},
							RadioButton{AssignTo: &st.rbStrategy[2], Text: tr("Resident watchdog", "Постоянный watchdog", "常驻守护 (定时看 Gen2)")},
						},
					},
					CheckBox{AssignTo: &st.ckAutoHard, Text: tr("Automatically use Stage2 fallback when Gen2 is not achieved (Link Disable + PnP recovery)", "Автоматически использовать Stage2, если Gen2 не достигнут (Link Disable + восстановление PnP)", "Gen2 未达成时自动执行 Stage2 回退 (Link Disable + PnP 恢复; 关掉可避免唯一显示卡登录后瞬断数秒)")},
					Composite{
						Layout: HBox{},
						Children: []Widget{
							Label{AssignTo: &st.lblRetry, Text: tr("Retry count:", "Повторы:", "失败自动重试:")},
							NumberEdit{AssignTo: &st.neRetryCnt, MinValue: 0.0, MaxValue: 12.0, MinSize: Size{Width: 56}},
							Label{AssignTo: &st.lblRetrySep, Text: tr(" / interval:", " / интервал:", "次 / 间隔:")},
							NumberEdit{AssignTo: &st.neRetryMin, MinValue: 1.0, MaxValue: 240.0, MinSize: Size{Width: 56}},
							Label{AssignTo: &st.lblMinutes, Text: tr("minutes", "мин", "分钟")},
						},
					},
					Composite{
						Layout: HBox{},
						Children: []Widget{
							PushButton{AssignTo: &st.pbSave, Text: tr("Save policy", "Сохранить политику", "保存策略"), OnClicked: func() {
								if !st.begin(tr("Save policy", "Сохранить политику", "保存策略")) {
									return
								}
								go st.savePolicy()
							}},
							PushButton{AssignTo: &st.pbGen2, Text: tr("Run Gen2 now (this run only)", "Запустить Gen2 сейчас (только этот запуск)", "立即执行 Gen2 (仅本次解锁)"), OnClicked: func() {
								if !st.begin(tr("Run Gen2 now", "Запустить Gen2 сейчас", "立即执行 Gen2")) {
									return
								}
								go func() {
									defer st.end()
									st.sync(func() { st.pbGen2.SetEnabled(false) })
									defer st.sync(func() { st.pbGen2.SetEnabled(true) })
									fmt.Println(tr("[Gen2] Running once now (equivalent to the -gen2 command line): temporarily load the driver -> unlock -> unload by default when done.", "[Gen2] Однократный запуск сейчас (эквивалент командной строки -gen2): временно загрузить драйвер -> разблокировать -> по умолчанию выгрузить по завершении.", "[Gen2] 立即执行一次(等价命令行 -gen2): 临时加载驱动→解锁→默认用完即卸。"))
									fmt.Println(tr("[Gen2] Note: this button [applies to this session only] and does not install boot-time auto-start;", "[Gen2] Внимание: эта кнопка [действует только для текущего сеанса] и не устанавливает автозапуск при загрузке;", "[Gen2] 注意: 本按钮【仅本次生效】, 不会安装开机自启;"))
									fmt.Println(tr("[Gen2] To auto-unlock on every boot, click [Run Gen2 and install auto-start] on the right, or tick [Gen2 login auto-start] in section ① and install.", "[Gen2] Чтобы разблокировать автоматически при каждой загрузке, нажмите [Запустить Gen2 и установить автозапуск] справа или отметьте [Автозапуск Gen2 при входе] в разделе ① и установите.", "[Gen2] 想装好后每次开机自动解锁, 请点右侧[执行 Gen2 并安装自启], 或勾①区 [Gen2 登录自启] 并安装。"))
									gen2Main()
									if hxcore.DriverStrategy() == hxcore.DriverStrategyResident {
										fmt.Println(tr("[Note] 'Resident watchdog' selected: unlocked for this session; the per-minute self-check is carried by the login auto-start task (from next login), and the watchdog will not run unless auto-start is registered.", "[Примечание] Выбран 'постоянный watchdog': разблокировано в этом сеансе; ежеминутная самопроверка выполняется задачей автозапуска при входе (со следующего входа), и watchdog не будет работать, пока не зарегистрирован автозапуск.", "[提示] 已选'常驻守护': 本次已解锁; 每分钟自查由登录自启任务承担(下次登录起), 未注册自启则守护不会运行。"))
									}
									st.scanOnce() // Gen2 后驱动/终态可能变化, 更新提示
								}()
							}},
							PushButton{AssignTo: &st.pbGen2Install, Text: tr("Run Gen2 and install auto-start", "Запустить Gen2 и установить автозапуск", "执行 Gen2 并安装自启 (本次+开机自动)"), OnClicked: func() {
								if !st.begin(tr("Unlock and install auto-start", "Разблокировать и установить автозапуск", "解锁并安装自启")) {
									return
								}
								go func() {
									defer st.end()
									st.sync(func() { st.pbGen2Install.SetEnabled(false) })
									defer st.sync(func() { st.pbGen2Install.SetEnabled(true) })
									fmt.Println(tr("== Run Gen2 and install auto-start ==", "== Запустить Gen2 и установить автозапуск ==", "== 执行 Gen2 并安装自启 =="))
									fmt.Println(tr("[Gen2] Step 1/2: unlock this session first (temporarily load the driver -> unlock -> unload by default when done)...", "[Gen2] Шаг 1/2: сначала разблокировать текущий сеанс (временно загрузить драйвер -> разблокировать -> по умолчанию выгрузить по завершении)...", "[Gen2] 步骤1/2: 先解锁本次(临时加载驱动→解锁→默认用完即卸)..."))
									gen2Main()
									fmt.Println(tr("[Gen2] Step 2/2: install boot-time auto-start — driver deployment + Defender whitelist + login task + Run key...", "[Gen2] Шаг 2/2: установить автозапуск при загрузке — развёртывание драйверов + белый список Defender + задача входа + ключ Run...", "[Gen2] 步骤2/2: 安装开机自启 — 驱动部署+Defender 加白 + 登录任务 + Run 键..."))
									installDrivers()
									if err := hxcore.AddDefenderExclusions(); err != nil {
										st.printDefErr("  [Defender] ", err)
									} else {
										fmt.Println(tr("  [Defender] driver files / backup directory whitelisted", "  [Defender] файлы драйверов / каталог резервных копий добавлены в белый список", "  [Defender] 驱动文件/备份目录已加白"))
									}
									setRunKey()
									if err := setupGen2Task(); err != nil {
										fmt.Println(tr("  [!] Login auto-start registration failed:", "  [!] Сбой регистрации автозапуска при входе:", "  [!] 登录自启注册失败:"), err)
										fmt.Println(tr("  [!] You can install it later: tick [Gen2 login auto-start] in section ① and click [Install selected]", "  [!] Можно установить позже: отметьте [Автозапуск Gen2 при входе] в разделе ① и нажмите [Установить выбранное]", "  [!] 可稍后在 ① 区勾 [Gen2 登录自启] 点[安装所选组件] 补装"))
									} else {
										fmt.Println(tr("  [Auto-start] Registered — Gen2 will unlock automatically at next login (already unlocked this session, no reboot needed)", "  [Автозапуск] Зарегистрировано — Gen2 разблокируется автоматически при следующем входе (уже разблокировано в этом сеансе, перезагрузка не нужна)", "  [自启] 注册完成 — 下次登录会自动解锁 Gen2(本次已先解锁, 无需重启)"))
										if hxcore.DriverStrategy() == hxcore.DriverStrategyResident {
											fmt.Println(tr("  [Auto-start] Resident watchdog enabled: the login task will self-check Gen2 every minute and re-apply automatically if TLS is lost", "  [Автозапуск] Постоянный watchdog включён: задача входа будет проверять Gen2 каждую минуту и повторно применять автоматически при потере TLS", "  [自启] 常驻守护已启用: 登录任务将每分钟自查 Gen2, TLS 丢失自动重训"))
										}
									}
									st.scanOnce()
								}()
							}},
						},
					},
				},
			},
			GroupBox{
				AssignTo: &st.gbLog,
				Title:    tr("3. Operation log (live)", "3. Журнал операций (онлайн)", "③ 操作日志 (实时)"),
				Layout:   VBox{},
				Children: []Widget{
					TextEdit{AssignTo: &st.teLog, ReadOnly: true, VScroll: true,
						MinSize: Size{Height: 120}, StretchFactor: 2},
				},
			},
		},
	}.Create()
	if createErr != nil {
		msgbox(tr("40HX Installer", "Установщик 40HX", "40HX 安装器"), tr("GUI initialization failed: ", "Сбой инициализации GUI: ", "GUI 初始化失败: ")+createErr.Error()+tr("\nPlease use the command-line mode (40HXInstaller.exe -h).", "\nИспользуйте режим командной строки (40HXInstaller.exe -h).", "\n请使用命令行方式 (40HXInstaller.exe -h)。"), mbIconError)
		return
	}
	st.log.mw = st.mw
	st.log.te = st.teLog
	AttachLogSink(st.log)
	st.refreshTexts()

	st.loadPolicyUI() // Gen2 策略回读(UI 线程, Run 之前)

	fmt.Println(tr("CMP 40HX Unlock Manager "+hxcore.Version+" started (administrator).", "Менеджер разблокировки CMP 40HX "+hxcore.Version+" запущен (администратор).", "CMP 40HX 解锁管理器 "+hxcore.Version+" 已启动(管理员)。"))
	go func() {
		// 打开自动扫描一次: 预勾选 + 顶部提示; 期间禁用执行按钮防并发。
		// 结果由 applySmartDefaults 打印([i] 已预勾… / [i] 组件均已就绪…)。
		st.setActionsEnabled(false)
		defer st.setActionsEnabled(true)
		st.scanOnce()
		st.applySmartDefaults()
	}()
	st.mw.Run()
}

// installSelected: 安装所选组件与设置(顺序执行, 输出全部进日志面板)。
// 分段风格同 CLI 全流程; 不再出现孤立的 [x/8] 编号(那是 install() 的编排编号)。
func (st *guiState) installSelected(sel map[string]bool) {
	defer st.end()
	st.sync(func() { st.pbInstall.SetEnabled(false) })
	defer st.sync(func() { st.pbInstall.SetEnabled(true) })
	nameOf := map[string]string{
		"gsp": tr("Enable GSP", "Включить GSP", "GSP 启用"), "drv": tr("Gen2 driver deployment", "Развёртывание драйверов Gen2", "Gen2 驱动部署"), "efi": tr("Compute EFI + boot entry", "Compute EFI + запись загрузки", "算力 EFI + 启动项"),
		"rebar": tr("ReBar Unlock (8 GB BAR1)", "Разблокировка ReBar (BAR1 8 ГБ)", "ReBar 解锁 (8 GB BAR1)"),
		"task": tr("Gen2 login auto-start", "Автозапуск Gen2 при входе", "Gen2 登录自启"), "fast": tr("Disable Fast Startup", "Откл. быстрый запуск", "关闭快速启动"), "aspm": tr("Disable ASPM", "Откл. ASPM", "关闭 ASPM"),
		"perf": tr("High-performance power plan", "План электропитания высокой производительности", "高性能电源计划"), "defoff": tr("Disable Defender real-time protection", "Откл. защиту Defender в реальном времени", "关闭 Defender 实时防护"),
	}
	var parts []string
	for _, k := range []string{"gsp", "drv", "efi", "rebar", "task", "fast", "aspm", "perf", "defoff"} {
		if sel[k] {
			parts = append(parts, nameOf[k])
		}
	}
	if len(parts) == 0 {
		fmt.Println(tr("[i] No component/setting is selected — tick something first, then click [Install selected]", "[i] Не выбран ни один компонент/параметр — сначала отметьте что-нибудь, затем нажмите [Установить выбранное]", "[i] 未勾选任何组件/设置 — 请先勾选再点[安装所选组件]"))
		return
	}
	fmt.Println(tr("== Running: ", "== Выполняется: ", "== 执行: ") + strings.Join(parts, " / ") + " ==")
	if sel["gsp"] {
		fmt.Println(tr("──── Enable GSP (EnableGpuFirmware=1, the key to unlocking without a black screen) ────", "──── Включение GSP (EnableGpuFirmware=1, ключ к разблокировке без чёрного экрана) ────", "──── GSP 启用 (EnableGpuFirmware=1, 解锁不黑屏的关键) ────"))
		if err := enableGsp(); err != nil {
			fmt.Println(tr("  [GSP] Failed:", "  [GSP] Сбой:", "  [GSP] 失败:"), err)
		} else {
			fmt.Println(tr("  [GSP] EnableGpuFirmware=1 set (GSP-RM takes effect after reboot)", "  [GSP] EnableGpuFirmware=1 установлено (GSP-RM вступит в силу после перезагрузки)", "  [GSP] EnableGpuFirmware=1 已设置 (重启后 GSP-RM 生效)"))
		}
	}
	if sel["drv"] {
		fmt.Println(tr("──── Gen2 driver deployment + Defender exclusion (ThrottleStop/WinRing0) ────", "──── Развёртывание драйверов Gen2 + исключение Defender (ThrottleStop/WinRing0) ────", "──── Gen2 驱动部署 + Defender 排除 (ThrottleStop/WinRing0) ────"))
		installDrivers()
		if err := hxcore.AddDefenderExclusions(); err != nil {
			st.printDefErr("  [Defender] ", err)
		} else {
			fmt.Println(tr("  [Defender] driver files / backup directory whitelisted", "  [Defender] файлы драйверов / каталог резервных копий добавлены в белый список", "  [Defender] 驱动文件/备份目录已加白"))
		}
	}
	if sel["efi"] {
		fmt.Println(tr("──── Compute EFI deployment + firmware boot entry (dual-path write + pin to top) ────", "──── Развёртывание compute EFI + запись загрузки прошивки (двойная запись + закрепление сверху) ────", "──── 算力 EFI 部署 + 固件启动项 (双路写入 + 置顶) ────"))
		installEFI()
	}
	if sel["rebar"] {
		fmt.Println(tr("──── ReBar Unlock (ensure the boot entry lets the EFI resize BAR1 to 8 GB) ────", "──── Разблокировка ReBar (запись загрузки разрешает EFI увеличить BAR1 до 8 ГБ) ────", "──── ReBar 解锁 (确保启动项允许 EFI 把 BAR1 放大到 8 GB) ────"))
		applyRebarLoadOption(true)
	}
	if sel["task"] {
		fmt.Println(tr("──── Gen2 login auto-start (SYSTEM task + Run-key fallback) ────", "──── Автозапуск Gen2 при входе (задача SYSTEM + резервный ключ Run) ────", "──── Gen2 登录自启 (SYSTEM 任务 + Run 键兜底) ────"))
		setRunKey()
		if err := setupGen2Task(); err != nil {
			fmt.Println(tr("  [!] Login auto-start registration failed:", "  [!] Сбой регистрации автозапуска при входе:", "  [!] 登录自启注册失败:"), err)
			fmt.Println(tr("  [!] You can run it later as administrator: 40HXInstaller.exe -task", "  [!] Можно запустить позже от имени администратора: 40HXInstaller.exe -task", "  [!] 可稍后以管理员运行: 40HXInstaller.exe -task"))
		}
	}
	if sel["fast"] || sel["aspm"] || sel["perf"] {
		fmt.Println(tr("──── Power settings (details; all can be reverted in the system power options) ────", "──── Настройки электропитания (детали; всё можно вернуть в системных параметрах питания) ────", "──── 电源设置 (细项; 均可在系统电源选项中改回) ────"))
	}
	if sel["fast"] {
		if hxcore.FastStartupOn() {
			if err := hxcore.SetFastStartupOff(); err != nil {
				fmt.Println(tr("  [Power] Failed to disable Fast Startup:", "  [Питание] Не удалось отключить быстрый запуск:", "  [电源] 关闭快速启动失败:"), err)
			} else {
				fmt.Println(tr("  [Power] Fast Startup disabled (HiberbootEnabled=0)", "  [Питание] Быстрый запуск отключён (HiberbootEnabled=0)", "  [电源] 快速启动已关闭 (HiberbootEnabled=0)"))
			}
		} else {
			fmt.Println(tr("  [Power] Fast Startup: already off (OK)", "  [Питание] Быстрый запуск: уже отключён (OK)", "  [电源] 快速启动: 原本已关(OK)"))
		}
	}
	if sel["aspm"] {
		if ac, dc, ok := hxcore.ASPMSavings(); !ok {
			fmt.Println(tr("  [Power] PCIe ASPM: this machine does not expose the setting, skipped", "  [Питание] PCIe ASPM: эта машина не предоставляет этот параметр, пропущено", "  [电源] PCIe ASPM: 本机未公开该设置, 跳过"))
		} else if ac == 0 && dc == 0 {
			fmt.Println(tr("  [Power] PCIe ASPM: already off (OK)", "  [Питание] PCIe ASPM: уже отключён (OK)", "  [电源] PCIe ASPM: 原本已关(OK)"))
		} else {
			if err := hxcore.SetASPMOff(); err != nil {
				fmt.Println(tr("  [Power] Failed to disable ASPM:", "  [Питание] Не удалось отключить ASPM:", "  [电源] ASPM 关闭失败:"), err)
			} else {
				fmt.Printf(tr("  [Power] PCIe ASPM disabled (was AC=%d/DC=%d)\n", "  [Питание] PCIe ASPM отключён (было AC=%d/DC=%d)\n", "  [电源] PCIe ASPM 已关闭(原 AC=%d/DC=%d)\n"), ac, dc)
			}
		}
	}
	if sel["perf"] {
		if hxcore.HighPerfPlanActive() {
			fmt.Println(tr("  [Power] Power plan: already high-performance (OK)", "  [Питание] Схема питания: уже высокая производительность (OK)", "  [电源] 电源计划: 已是高性能(OK)"))
		} else if err := hxcore.SetHighPerfPlan(); err != nil {
			fmt.Println(tr("  [Power] Failed to switch to the high-performance plan:", "  [Питание] Не удалось переключиться на план высокой производительности:", "  [电源] 切换高性能计划失败:"), err)
		} else {
			fmt.Println(tr("  [Power] Switched to the high-performance power plan (revertible in the power options)", "  [Питание] Переключено на план высокой производительности (можно вернуть в параметрах питания)", "  [电源] 已切换到高性能电源计划 (可在电源选项改回)"))
		}
	}
	if sel["defoff"] {
		fmt.Println(tr("──── Windows Defender real-time protection ────", "──── Защита Windows Defender в реальном времени ────", "──── Windows Defender 实时防护 ────"))
		on, err := hxcore.DefenderRealtimeProtectionOn()
		if err != nil {
			st.printDefErr("  [!] ", err)
		} else if !on {
			fmt.Println(tr("  [Defender] Real-time protection is already off (no action needed)", "  [Defender] Защита в реальном времени уже отключена (действий не требуется)", "  [Defender] 实时防护当前已关闭(无需操作)"))
		} else if err := hxcore.SetDefenderRealtimeProtection(false); err != nil {
			st.printDefErr("  [!] ", err)
			if !errors.Is(err, hxcore.ErrMpUnavailable) {
				fmt.Println(tr("  [!] Common cause: Tamper Protection is enabled in Windows Security — turn it off first and retry", "  [!] Частая причина: в Центре безопасности Windows включена защита от подделки — сначала отключите её и повторите", "  [!] 常见原因: Windows 安全中心开了'篡改防护' — 请先关闭它再重试"))
			}
		} else {
			fmt.Println(tr("  [Defender] Real-time protection disabled", "  [Defender] Защита в реальном времени отключена", "  [Defender] 实时防护已关闭"))
			fmt.Println(tr("  [Defender] To restore: run Set-MpPreference -DisableRealtimeMonitoring $False in an admin PowerShell", "  [Defender] Восстановление: выполните Set-MpPreference -DisableRealtimeMonitoring $False в PowerShell от имени администратора", "  [Defender] 恢复: 管理员 PowerShell 运行 Set-MpPreference -DisableRealtimeMonitoring $False"))
		}
	}
	fmt.Println(tr("== Done; rescanning status automatically ==", "== Готово; автоматическое повторное сканирование состояния ==", "== 执行完成, 自动重扫状态 =="))
	st.scanOnce()
	st.applySmartDefaults()
}
