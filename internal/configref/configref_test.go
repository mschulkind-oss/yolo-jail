package configref

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
	// Literal bracketed text (not a tag) is preserved.
	if !strings.Contains(plain, "[5432]") {
		t.Error("literal [5432] should be preserved in plain output")
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
