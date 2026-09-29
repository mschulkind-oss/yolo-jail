package shquote

import (
	"os/exec"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

func TestQuoteDisplay(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                 "plain",
		"with space":            "'with space'",
		"a'b":                   `'a'"'"'b'`,
		"\x1b[2J\x1b[H":         `$'\x1b[2J\x1b[H'`,
		"x\x1b]0;pwned\afake\r": `$'x\x1b]0;pwned\x07fake\x0d'`,
		"it's\x1b":              `$'it\'s\x1b'`,
		`back\slash` + "\x7f":   `$'back\\slash\x7f'`,
		"rtl\u202eevil":         `$'rtl\xe2\x80\xaeevil'`,
		"bad\x9bbyte":           `$'bad\x9bbyte'`,
		"c1\u009b31m":           `$'c1\xc2\x9b31m'`,
		"ünïcödé":               "'ünïcödé'",
	} {
		got := QuoteDisplay(in)
		if got != want {
			t.Errorf("QuoteDisplay(%q) = %q, want %q", in, got, want)
		}
		if termsafe.HasUnsafe(got) {
			t.Errorf("QuoteDisplay(%q) = %q still holds a control character", in, got)
		}
	}
}

// The display form is still the word: bash reads it back byte for byte.
func TestQuoteDisplayRoundTripsThroughBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	for _, in := range []string{"\x1b[2J", "it's\x1b\\x", "rtl\u202eevil", "bad\x9bbyte", "plain word"} {
		out, err := exec.Command("bash", "-c", "printf %s "+QuoteDisplay(in)).Output()
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != in {
			t.Errorf("bash read %q back as %q", in, out)
		}
	}
}
