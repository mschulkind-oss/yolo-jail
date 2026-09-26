package tty

import (
	"bytes"
	"strings"
	"testing"
)

// TestConfirmAnswers pins the one yes/no grammar every prompt shares: y/yes and n/no in any
// case, Enter takes the default, anything else is no, and end of input is no even when the
// default is yes — a closed stdin is not consent.
func TestConfirmAnswers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		input      string
		defaultYes bool
		want       bool
	}{
		{"y", "y\n", false, true},
		{"YES", "YES\n", false, true},
		{"n on default-yes", "n\n", true, false},
		{"no on default-yes", " No \n", true, false},
		{"enter takes default yes", "\n", true, true},
		{"enter takes default no", "\n", false, false},
		{"typo is no", "yse\n", true, false},
		{"EOF is no on default-yes", "", true, false},
		{"EOF is no on default-no", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if got := Confirm(&out, strings.NewReader(tc.input), "Go? [Y/n] ", tc.defaultYes); got != tc.want {
				t.Errorf("Confirm(%q, defaultYes=%v) = %v, want %v", tc.input, tc.defaultYes, got, tc.want)
			}
			if out.String() != "Go? [Y/n] " {
				t.Errorf("the prompt must be written as given, got %q", out.String())
			}
		})
	}
}

// TestConfirmNilReaderIsNo: no reader means no one can answer, so a default-yes prompt must
// not act on it.
func TestConfirmNilReaderIsNo(t *testing.T) {
	var out bytes.Buffer
	if Confirm(&out, nil, "Go? [Y/n] ", true) {
		t.Error("Confirm with a nil reader answered yes")
	}
}
