package main

import (
	"os"

	hxcore "40hxcore"
)

// Thin wrappers over the shared language state in 40hxcore. The installer owns
// the language selector, so it is also the tool that persists the choice; the
// checker and the uninstaller only read it.
//
// localizeLogText used to live here. It rewrote Chinese log lines word by word
// on their way to the log sink, which produced broken grammar and could corrupt
// a multi-byte character whenever a 4 KiB pipe read landed mid-rune. Log text is
// now translated at the call site like everything else, so the post-processing
// pass is gone.

type Language = hxcore.Language

const (
	LanguageEnglish = hxcore.LanguageEnglish
	LanguageRussian = hxcore.LanguageRussian
	LanguageChinese = hxcore.LanguageChinese
)

func initLanguage() { hxcore.InitLanguage(os.Args) }

func languageIndex() int { return hxcore.LanguageIndex() }

// setLanguage switches language and persists the choice so 40HXCheck.exe and
// 40HXUninstaller.exe start in the same language. It reports whether anything
// actually changed — a Windows combo box raises CurrentIndexChanged more than
// once for a single pick, and callers use this to stay idempotent.
func setLanguage(index int) bool {
	if !hxcore.SetLanguage(hxcore.Language(index)) {
		return false
	}
	// Best effort: the tools that change language run elevated, but a failure
	// here only means the choice will not carry over to the other two tools.
	_ = hxcore.PersistLanguage(hxcore.Language(index))
	return true
}

func languageLabel(index int) string { return hxcore.LanguageLabel(hxcore.Language(index)) }

// tr returns the translation for the active language. Keeping all three values
// at the call site makes it explicit which text is user-facing and avoids
// touching protocol, registry, task, or log-parsing identifiers.
func tr(en, ru, zh string) string { return hxcore.T(en, ru, zh) }
