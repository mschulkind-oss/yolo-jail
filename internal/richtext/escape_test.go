package richtext

import (
	"strings"
	"testing"
)

// Escape turns every known style tag in a string yolo did not write into text: it renders
// no ANSI in color mode and is not stripped in plain mode, while literals are untouched.
func TestEscapeKeepsStyleTagsAsText(t *testing.T) {
	in := "a [bold red]b[/bold red] [path] [y/N]"
	esc := Escape(in)
	if got := Render(esc, true); strings.Contains(got, "\x1b[") {
		t.Errorf("an escaped tag still styled the line: %q", got)
	}
	plain := strings.ReplaceAll(Render(esc, false), wordJoiner, "")
	if plain != in {
		t.Errorf("plain render of the escaped string = %q, want %q", plain, in)
	}
	if Escape("[path] [y/N]") != "[path] [y/N]" {
		t.Errorf("Escape touched literals: %q", Escape("[path] [y/N]"))
	}
}
