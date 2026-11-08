package cli

import (
	"strings"
	"testing"
)

func TestNoTagsLeakInColorOrPlain(t *testing.T) {
	// Plain output must contain none of the known rich tags.
	plain := Render(false)
	for _, tag := range []string{"[bold]", "[/bold]", "[bold cyan]", "[cyan]", "[yellow]", "[bold yellow]"} {
		if strings.Contains(plain, tag) {
			t.Errorf("plain output still contains tag %q", tag)
		}
	}
	// Literal bracketed text (not a tag) is preserved: the TOML examples' table headers.
	for _, literal := range []string{"[machine.gpu]", "[allow.mounts]", "[install]"} {
		if !strings.Contains(plain, literal) {
			t.Errorf("literal %s should be preserved in plain output", literal)
		}
	}
	// Color output has ANSI and no leftover rich tags.
	color := Render(true)
	if !strings.Contains(color, "\x1b[1m") {
		t.Error("color output should contain ANSI bold")
	}
	for _, tag := range []string{"[bold]", "[/bold]", "[bold cyan]"} {
		if strings.Contains(color, tag) {
			t.Errorf("color output still contains rich tag %q", tag)
		}
	}
}
