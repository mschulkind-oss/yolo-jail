package cli

import (
	"strings"
	"testing"
)

func TestNoTagsLeakInColorOrPlain(t *testing.T) {
	// Plain output must contain none of the known rich tags.
	plain := Render(false)
	for _, tag := range []string{"[bold]", "[/bold]", "[bold cyan]", "[cyan]", "[yellow]", "[bold yellow]", "[green]", "[/green]"} {
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

// TestConfigRefValueLabelsAreGreen pins the Group C polish of docs/plans/cli-visual-polish.md:
// each `Default:` and `Values:` label is green in color output and plain text otherwise.
func TestConfigRefValueLabelsAreGreen(t *testing.T) {
	color, plain := Render(true), Render(false)
	for _, label := range []string{"Default:", "Values:"} {
		if !strings.Contains(color, "\x1b[32m"+label+"\x1b[0m") {
			t.Errorf("color output does not color %q green", label)
		}
		if !strings.Contains(plain, "    "+label+" ") {
			t.Errorf("plain output lost the %q label", label)
		}
	}
	for _, tag := range []string{"[green]", "[/green]"} {
		if strings.Contains(color, tag) {
			t.Errorf("color output still contains rich tag %q", tag)
		}
	}
}

// TestConfigRefResourcePathsAreCyan pins the Cross-cutting resource-path polish of
// docs/plans/cli-visual-polish.md: config file paths and endpoint paths on label: path lines
// are cyan in color output and plain text otherwise.
func TestConfigRefResourcePathsAreCyan(t *testing.T) {
	color, plain := Render(true), Render(false)
	for _, p := range []string{
		"yolo-jail.toml",
		"yolo-jail.local.toml",
		"~/.config/yolo-jail/config.toml",
		"~/.config/yolo-jail/config.d/*.toml",
		"yolo-jail.jsonc",
		"yolo-jail.local.jsonc",
		"~/.config/yolo-jail/config.jsonc",
		"/run/yolo-services/journal.endpoint",
	} {
		if !strings.Contains(color, "\x1b[36m"+p+"\x1b[0m") {
			t.Errorf("color output does not color resource path %q cyan", p)
		}
		if !strings.Contains(plain, p) {
			t.Errorf("plain output lost resource path %q", p)
		}
	}
}
