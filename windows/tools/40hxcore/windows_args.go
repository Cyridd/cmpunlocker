package hxcore

import "strings"

// QuoteWindowsArg encodes one argv element for the command-line parser used by
// ShellExecute/CreateProcess.  strings.Join is not sufficient here: an exe
// path or -log value containing spaces would be split after UAC elevation,
// while embedded quotes and trailing backslashes need special handling.
func QuoteWindowsArg(arg string) string {
	if arg != "" && !strings.ContainsAny(arg, " \t\"") {
		return arg
	}

	var b strings.Builder
	b.Grow(len(arg) + 2)
	b.WriteByte('"')
	backslashes := 0
	for _, r := range arg {
		switch r {
		case '\\':
			backslashes++
		case '"':
			// Escape every preceding backslash and the quote itself.
			b.WriteString(strings.Repeat("\\", backslashes*2+1))
			b.WriteByte('"')
			backslashes = 0
		default:
			if backslashes > 0 {
				b.WriteString(strings.Repeat("\\", backslashes))
				backslashes = 0
			}
			b.WriteRune(r)
		}
	}
	// Backslashes immediately before the closing quote must be doubled.
	if backslashes > 0 {
		b.WriteString(strings.Repeat("\\", backslashes*2))
	}
	b.WriteByte('"')
	return b.String()
}

// JoinWindowsArgs returns a ShellExecute-compatible parameter string.
func JoinWindowsArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = QuoteWindowsArg(arg)
	}
	return strings.Join(quoted, " ")
}
