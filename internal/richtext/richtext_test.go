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
