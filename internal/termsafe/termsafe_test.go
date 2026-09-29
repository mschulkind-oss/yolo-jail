package termsafe

import "testing"

func TestVisible(t *testing.T) {
	for in, want := range map[string]string{
		"plain /path/to/x":          "plain /path/to/x",
		"x\x1b[2K\x1b[1A":           `x\x1b[2K\x1b[1A`,
		"\x1b]0;owned\a":            `\x1b]0;owned\a`,
		"a\rb\tc\nd":                `a\rb\tc\nd`,
		"del\x7f":                   `del\x7f`,
		"c1\u009b31m":               `c1\u009b31m`,
		"rtl\u202eevil":             `rtl\u202eevil`,
		"bad\x9bbyte":               `bad\x9bbyte`,
		"ünïcödé stays":             "ünïcödé stays",
		"\x1b]52;c;cGF3bmVk\x07 ok": `\x1b]52;c;cGF3bmVk\a ok`,
	} {
		if got := Visible(in); got != want {
			t.Errorf("Visible(%q) = %q, want %q", in, got, want)
		}
		if HasUnsafe(Visible(in)) {
			t.Errorf("Visible(%q) still holds an unsafe rune", in)
		}
	}
}
