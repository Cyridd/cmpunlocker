package hxcore

import (
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// Shared language state for the whole 40HX tool chain.
//
// The installer, the checker, and the uninstaller all link this package, so
// putting the language here is what makes "pick a language in the installer and
// the other two follow" work. The selection is persisted as the DWORD
// Language under ConfigKeyPath (HKLM\SOFTWARE\40HXUnlock), next to the Gen2
// policy values, and is removed together with that key on uninstall.
//
// English is the default. The original upstream tools were Chinese-only; a
// missing or unreadable registry value must therefore resolve to English
// rather than to the historical Chinese default.

// Version is the product version reported by every tool in this tree. Keeping
// it in one place stops the installer, checker, and uninstaller from drifting
// apart, which they had done before.
const Version = "v2.0.1"

type Language uint8

const (
	LanguageEnglish Language = iota
	LanguageRussian
	LanguageChinese
)

// LanguageValueName is the DWORD under ConfigKeyPath holding the selection.
const LanguageValueName = "Language"

// LanguageEnvVar overrides the persisted value for a single run.
const LanguageEnvVar = "CMP40HX_LANG"

var currentLanguage = LanguageEnglish

// InitLanguage resolves the language once at start-up. Precedence, highest
// first: the -lang argument, the CMP40HX_LANG environment variable, the value
// the installer persisted, and finally English.
//
// args is normally os.Args. Passing it in keeps this package free of
// assumptions about how each tool parses its command line.
func InitLanguage(args []string) {
	for i, a := range args {
		if a == "-lang" && i+1 < len(args) {
			currentLanguage = ParseLanguage(args[i+1])
			return
		}
		// Also accept -lang=ru, which is what people type out of habit.
		if strings.HasPrefix(a, "-lang=") {
			currentLanguage = ParseLanguage(strings.TrimPrefix(a, "-lang="))
			return
		}
	}
	if v := os.Getenv(LanguageEnvVar); strings.TrimSpace(v) != "" {
		currentLanguage = ParseLanguage(v)
		return
	}
	currentLanguage = StoredLanguage()
}

// ParseLanguage maps a user-supplied name onto a language, defaulting to
// English for anything unrecognised.
func ParseLanguage(value string) Language {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ru", "rus", "russian", "русский":
		return LanguageRussian
	case "zh", "cn", "zh-cn", "chinese", "中文":
		return LanguageChinese
	default:
		return LanguageEnglish
	}
}

// StoredLanguage reads the persisted selection, falling back to English when
// the key is absent or holds a value outside the known range.
func StoredLanguage() Language {
	v := ConfigInt(LanguageValueName, int(LanguageEnglish))
	if v < int(LanguageEnglish) || v > int(LanguageChinese) {
		return LanguageEnglish
	}
	return Language(v)
}

// PersistLanguage stores the selection so the checker and the uninstaller
// start in the same language. Writing under HKLM needs administrator rights;
// the tools that call this already run elevated, and a failure here is not
// worth interrupting an install for, so the error is returned for logging
// rather than being treated as fatal.
func PersistLanguage(l Language) error {
	return SetConfigInt(LanguageValueName, int(l))
}

// CurrentLanguage reports the active language.
func CurrentLanguage() Language { return currentLanguage }

// LanguageIndex reports the active language as a combo-box index.
func LanguageIndex() int { return int(currentLanguage) }

// SetLanguage switches the active language, ignoring out-of-range indices.
// It reports whether the language actually changed, which callers use to avoid
// acting on the repeated change notifications Windows combo boxes emit.
func SetLanguage(l Language) bool {
	if l < LanguageEnglish || l > LanguageChinese || l == currentLanguage {
		return false
	}
	currentLanguage = l
	return true
}

// LanguageLabel is the native name of a language, used for the selector.
func LanguageLabel(l Language) string {
	switch l {
	case LanguageRussian:
		return "Русский"
	case LanguageChinese:
		return "中文"
	default:
		return "English"
	}
}

// LanguageTag is the short form used for -lang arguments and log headers.
func LanguageTag(l Language) string {
	switch l {
	case LanguageRussian:
		return "ru"
	case LanguageChinese:
		return "zh"
	default:
		return "en"
	}
}

// T returns the translation for the active language.
//
// All three strings stay at the call site on purpose: it keeps the source
// readable, makes it obvious which text is user-facing, and avoids a key
// catalogue that can drift out of sync with the code. Identifiers that other
// code parses — registry names, task names, service names, log markers such as
// [GSP] or SS0= — must stay outside T.
func T(en, ru, zh string) string {
	switch currentLanguage {
	case LanguageRussian:
		return ru
	case LanguageChinese:
		return zh
	default:
		return en
	}
}

// languageKeyReadable reports whether the config key can be read at all. Used
// by the tools to decide whether to warn that a language choice will not stick.
func languageKeyReadable() bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, ConfigKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

// LanguagePersistAvailable reports whether a language choice can be stored.
func LanguagePersistAvailable() bool { return languageKeyReadable() }
