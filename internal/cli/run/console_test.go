package run

import (
	"strings"
	"testing"
)

// ANSI escapes the shared internal/richtext renderer emits — mirrored here so
// these run-package wiring tests can assert on rendered output without reaching
// into richtext's unexported constants. The renderer itself (tag → ANSI, literal
// preservation) is unit-tested in internal/richtext; these tests only cover the
// run package's color-gating and diff-coloring wiring.
const (
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
	ansiCyan  = "\x1b[36m"
)

// TestPrinterColorGating verifies o.pr() emits ANSI only when Color AND a TTY,
// and strips otherwise — so redirected output never carries escape codes.
func TestPrinterColorGating(t *testing.T) {
	mk := func(color, tty bool) string {
		var b strings.Builder
		o := &Options{Color: color, IsTTYStdout: func() bool { return tty }, Stdout: &b}
		o.pr(&b).print("[green]hi[/green]")
		return b.String()
	}
	if got := mk(true, true); !strings.Contains(got, ansiGreen) {
		t.Errorf("color+tty: expected ANSI, got %q", got)
	}
	for _, tc := range []struct {
		name       string
		color, tty bool
	}{{"no-color", false, true}, {"no-tty", true, false}} {
		if got := mk(tc.color, tc.tty); strings.Contains(got, "\x1b[") {
			t.Errorf("%s: expected NO ANSI, got %q", tc.name, got)
		} else if !strings.Contains(got, "hi") {
			t.Errorf("%s: text dropped: %q", tc.name, got)
		}
	}
}

// TestConfigDiffColored drives the changePrompter's diff rendering on a TTY and
// asserts +/- / hunk lines carry the expected ANSI colors (the reported bug: the
// config diff printed with no colors because the printer always stripped markup).
func TestConfigDiffColored(t *testing.T) {
	var out strings.Builder
	o := &Options{
		Color:       true,
		IsTTYStdout: func() bool { return true },
		IsTTYStdin:  func() bool { return true },
		Stdout:      &out,
		Stdin:       strings.NewReader("y\n"),
	}
	p := &changePrompter{o: o}
	ok := p.Prompt([]string{
		"--- last",
		"+++ current",
		"@@ -1 +1 @@",
		"-old: 1",
		"+new: 2",
	})
	if !ok {
		t.Fatal("Prompt should return true on 'y'")
	}
	s := out.String()
	if !strings.Contains(s, ansiGreen+"+new: 2") {
		t.Errorf("added line not green: %q", s)
	}
	if !strings.Contains(s, ansiRed+"-old: 1") {
		t.Errorf("removed line not red: %q", s)
	}
	if !strings.Contains(s, ansiCyan+"@@ -1 +1 @@") {
		t.Errorf("hunk header not cyan: %q", s)
	}
}
