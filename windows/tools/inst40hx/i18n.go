package main

import (
	"os"
	"strings"
)

// Language controls installer UI and diagnostic output. Chinese is kept for
// compatibility with the original release; English is the default for new
// installations.
type Language uint8

const (
	LanguageEnglish Language = iota
	LanguageRussian
	LanguageChinese
)

var installerLanguage = LanguageEnglish

func initLanguage() {
	value := os.Getenv("CMP40HX_LANG")
	if i := argIndex("-lang"); i >= 0 && i+1 < len(os.Args) {
		value = os.Args[i+1]
	}
	installerLanguage = parseLanguage(value)
}

func parseLanguage(value string) Language {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ru", "rus", "russian", "русский":
		return LanguageRussian
	case "zh", "cn", "中文", "chinese":
		return LanguageChinese
	default:
		return LanguageEnglish
	}
}

func languageIndex() int { return int(installerLanguage) }

func setLanguage(index int) {
	if index < int(LanguageEnglish) || index > int(LanguageChinese) {
		return
	}
	installerLanguage = Language(index)
}

func languageLabel(index int) string {
	switch Language(index) {
	case LanguageRussian:
		return "Русский"
	case LanguageChinese:
		return "中文"
	default:
		return "English"
	}
}

// tr returns the selected translation. Keeping all three values at call sites
// makes it explicit which text is user-facing and avoids changing protocol,
// registry, task, or log parsing identifiers.
func tr(en, ru, zh string) string {
	switch installerLanguage {
	case LanguageRussian:
		return ru
	case LanguageChinese:
		return zh
	default:
		return en
	}
}

// localizeLogText handles legacy messages that are still emitted by shared
// installer code. New UI text should use tr directly; this compatibility map
// keeps existing command-line and Gen2 diagnostics readable in the selected
// language without changing their control flow or format values.
func localizeLogText(s string) string {
	if installerLanguage == LanguageChinese || s == "" {
		return s
	}
	if installerLanguage == LanguageEnglish {
		r := strings.NewReplacer(
			"一键完整安装", "full installation", "一键安装", "one-click install", "安装器", "installer",
			"卸载工具", "uninstaller", "解锁管理器", "unlock manager", "管理器", "manager",
			"解锁", "unlock", "安装", "install", "卸载", "uninstall", "组件", "component",
			"环境", "environment", "设置", "settings", "执行", "run", "操作", "operation",
			"检测到", "detected", "未检测到", "not detected", "检测", "check", "找到", "found",
			"未找到", "not found", "需要", "required", "管理员权限", "administrator privileges",
			"权限", "permissions", "失败", "failed", "成功", "success", "完成", "complete",
			"已完成", "completed", "已启用", "enabled", "未启用", "not enabled", "已设置", "set",
			"未设置", "not set", "已关闭", "disabled", "开启", "enabled", "关闭", "disabled",
			"重启", "reboot", "重试", "retry", "跳过", "skip", "中止", "abort", "警告", "warning",
			"错误", "error", "注意", "note", "正常", "normal", "状态", "status", "当前", "current",
			"运行中", "running", "运行", "run", "加载", "load", "卸载", "unload", "驱动", "driver",
			"服务", "service", "任务", "task", "登录", "login", "自启动", "auto-start", "自启", "auto-start",
			"固件启动项", "firmware boot entry", "启动项", "boot entry", "固件", "firmware", "启动", "boot",
			"路径", "path", "文件", "file", "目录", "directory", "备份", "backup", "原", "original",
			"链路", "link", "重训", "retrain", "恢复", "restore", "电源", "power", "快速启动", "Fast Startup",
			"实时防护", "real-time protection", "高性能", "high performance", "策略", "policy", "默认", "default",
			"推荐", "recommended", "仅本次", "this run only", "下次", "next", "本次", "this run",
			"已", "", "未", "not ", "无", "no ", "有", "has ", "请", "please ", "确认", "confirm",
			"用户", "user", "显示", "display", "读取", "read", "写入", "write", "回读", "readback",
			"校验", "verify", "挂载", "mount", "复制", "copy", "删除", "delete", "清理", "cleanup",
			"保留", "keep", "暂时", "temporarily", "等待", "wait", "已找到", "found", "已注册", "registered",
			"未注册", "not registered", "已部署", "deployed", "未部署", "not deployed", "即", " ",
			"请勿", "do not", "否则", "otherwise", "仍", "still", "可能", "may", "可", "can ",
			"按", "press ", "看", "see ", "见", "see ", "详细", "details", "说明", "description",
			"重要", "important", "部分", "some", "本机", "this system", "系统", "system", "显卡", "GPU",
			"算力", "compute", "固件", "firmware", "链路", "link", "目标", "target", "速率", "speed",
			"即将", "about to", "无需", "no need to", "一切正常", "all good", "保留未动", "left unchanged",
			"分钟", "minutes", "次", "times", "秒", "seconds", "字节", "bytes", "第", "step ",
		)
		return r.Replace(s)
	}
	r := strings.NewReplacer(
		"一键完整安装", "полная установка", "一键安装", "установка в один клик", "安装器", "установщик",
		"卸载工具", "деинсталлятор", "解锁管理器", "менеджер разблокировки", "管理器", "менеджер",
		"解锁", "разблокировка", "安装", "установка", "卸载", "удаление", "组件", "компонент",
		"环境", "окружение", "设置", "настройки", "执行", "запуск", "操作", "операция",
		"检测到", "обнаружено", "未检测到", "не обнаружено", "检测", "проверка", "找到", "найдено",
		"未找到", "не найдено", "需要", "требуется", "管理员权限", "права администратора",
		"权限", "права", "失败", "ошибка", "成功", "успешно", "完成", "завершено", "已完成", "завершено",
		"已启用", "включено", "未启用", "не включено", "已设置", "установлено", "未设置", "не установлено",
		"已关闭", "отключено", "开启", "включено", "关闭", "отключить", "重启", "перезагрузка",
		"重试", "повтор", "跳过", "пропуск", "中止", "прервано", "警告", "предупреждение", "错误", "ошибка",
		"注意", "внимание", "正常", "норма", "状态", "состояние", "当前", "текущее", "运行中", "запущено",
		"运行", "запуск", "加载", "загрузка", "驱动", "драйвер", "服务", "служба", "任务", "задача",
		"登录", "вход", "自启动", "автозапуск", "自启", "автозапуск", "固件启动项", "запись загрузки прошивки",
		"启动项", "запись загрузки", "固件", "прошивка", "启动", "загрузка", "路径", "путь", "文件", "файл",
		"目录", "каталог", "备份", "резервная копия", "原", "исходный", "链路", "линия", "重训", "переобучение",
		"恢复", "восстановление", "电源", "питание", "快速启动", "быстрый запуск", "实时防护", "защита в реальном времени",
		"高性能", "высокая производительность", "策略", "политика", "默认", "по умолчанию", "推荐", "рекомендуется",
		"仅本次", "только этот запуск", "下次", "следующий", "本次", "этот запуск", "已", "", "未", "не ",
		"无", "нет ", "有", "есть ", "请", "пожалуйста, ", "确认", "подтвердите", "用户", "пользователь",
		"显示", "отображение", "读取", "чтение", "写入", "запись", "回读", "чтение назад", "校验", "проверка",
		"挂载", "подключение", "复制", "копирование", "删除", "удаление", "清理", "очистка", "保留", "оставлено",
		"等待", "ожидание", "已注册", "зарегистрировано", "未注册", "не зарегистрировано", "已部署", "развернуто",
		"未部署", "не развернуто", "请勿", "не следует", "否则", "иначе", "仍", "все еще", "可能", "может",
		"详细", "подробности", "说明", "описание", "重要", "важно", "部分", "некоторые", "本机", "эта система",
		"系统", "система", "显卡", "GPU", "算力", "вычисления", "目标", "цель", "速率", "скорость",
		"无需", "не требуется", "一切正常", "все в порядке", "分钟", "мин", "次", "раз", "秒", "сек",
		"字节", "байт", "第", "шаг ",
	)
	return r.Replace(s)
}
