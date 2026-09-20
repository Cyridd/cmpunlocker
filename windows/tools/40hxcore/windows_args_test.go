package hxcore

import "testing"

func TestQuoteWindowsArg(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"simple", "simple"},
		{"", `""`},
		{`C:\Program Files\x`, `"C:\Program Files\x"`},
		{`ends with slash\`, `"ends with slash\\"`},
		{`a"b`, `"a\"b"`},
	}
	for _, tc := range cases {
		if got := QuoteWindowsArg(tc.in); got != tc.want {
			t.Fatalf("QuoteWindowsArg(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestJoinWindowsArgs(t *testing.T) {
	got := JoinWindowsArgs([]string{"-log", `C:\Program Files\40HX\run.log`, "-elevated"})
	want := `-log "C:\Program Files\40HX\run.log" -elevated`
	if got != want {
		t.Fatalf("JoinWindowsArgs() = %q, want %q", got, want)
	}
}
