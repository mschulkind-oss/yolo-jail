package shquote

import (
	"testing"
)

// goldenQuote pins shlex.quote's observed output over adversarial argv.
var goldenQuote = map[string]string{
	"":                  "''",
	"plain":             "plain",
	"with space":        "'with space'",
	"a'b":               `'a'"'"'b'`,
	"$var":              `'$var'`,
	"a=b,c.d/e-f:g@h%i": "a=b,c.d/e-f:g@h%i", // all in the safe set
	"semi;colon":        "'semi;colon'",
	"back\\slash":       `'back\slash'`,
	"tab\there":         "'tab\there'",
	"newline\nhere":     "'newline\nhere'",
	"quote\"dq":         `'quote"dq'`,
	"glob*":             "'glob*'",
	"café":              "'café'", // non-ASCII: \w is ASCII-only in shlex
	"~home":             "'~home'",
	"(paren)":           "'(paren)'",
}

func TestQuoteGolden(t *testing.T) {
	for in, want := range goldenQuote {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinGolden(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b c"}, "a 'b c'"},
		{[]string{"echo", "$HOME", "a'b"}, `echo '$HOME' 'a'"'"'b'`},
		{[]string{"podman", "run", "--rm", "-e", "X=y z"}, "podman run --rm -e 'X=y z'"},
	}
	for _, tc := range cases {
		if got := Join(tc.in); got != tc.want {
			t.Errorf("Join(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
