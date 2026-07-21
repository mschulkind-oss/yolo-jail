package richtext

import "testing"

func TestToANSIStyleTags(t *testing.T) {
	got := ToANSI("[bold red]ERR[/bold red] ok")
	want := ansiBold + ansiRed + "ERR" + ansiReset + " ok"
	if got != want {
		t.Errorf("ToANSI = %q, want %q", got, want)
	}
}

func TestToANSILeavesLiteralsAlone(t *testing.T) {
	// [path] and [y/N] are NOT style tags — preserved verbatim in both modes.
	for _, s := range []string{"see [path]", "continue? [y/N]", "pick [rR]"} {
		if ToANSI(s) != s {
			t.Errorf("ToANSI mangled literal: %q -> %q", s, ToANSI(s))
		}
		if Strip(s) != s {
			t.Errorf("Strip mangled literal: %q -> %q", s, Strip(s))
		}
	}
}

func TestStripRemovesStyleTagsOnly(t *testing.T) {
	if got := Strip("[bold]hi[/bold] [dim]x[/dim]"); got != "hi x" {
		t.Errorf("Strip = %q, want %q", got, "hi x")
	}
}

func TestRenderColorGate(t *testing.T) {
	if got := Render("[green]ok[/green]", false); got != "ok" {
		t.Errorf("Render(color=false) = %q, want plain", got)
	}
	if got := Render("[green]ok[/green]", true); got != ansiGreen+"ok"+ansiReset {
		t.Errorf("Render(color=true) = %q, want ANSI", got)
	}
}

// TestFullPalette covers all six foreground hues plus bold/dim — the palette the
// visual-polish plan relies on (one hue per composition layer in --explain).
func TestFullPalette(t *testing.T) {
	cases := map[string]string{
		"bold":    ansiBold,
		"dim":     ansiDim,
		"red":     ansiRed,
		"green":   ansiGreen,
		"yellow":  ansiYellow,
		"blue":    ansiBlue,
		"magenta": ansiMagenta,
		"cyan":    ansiCyan,
	}
	for tag, code := range cases {
		in := "[" + tag + "]x[/" + tag + "]"
		if got := ToANSI(in); got != code+"x"+ansiReset {
			t.Errorf("ToANSI(%q) = %q, want %q", in, got, code+"x"+ansiReset)
		}
		if got := Strip(in); got != "x" {
			t.Errorf("Strip(%q) = %q, want %q", in, got, "x")
		}
	}
}
