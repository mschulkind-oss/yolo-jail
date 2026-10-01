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

// An opening Escape's string does not close would run on into the markup written after it: the
// renderer reads `[docs[/dim]` as one unknown token, so the closing tag printed as text in both
// modes and, on a terminal, the dim was never reset. Every opening left unclosed at the end of the
// string is escaped too, so the markup around it renders, and the text is unchanged once the
// joiners are removed.
func TestEscapeLeavesNoTagOpenAtItsEnd(t *testing.T) {
	for _, in := range []string{"see [docs for more", "a [/b", "a [b] then [c", "two [x [y", "ends [", "ends [/"} {
		line := "[dim]" + Escape(in) + "[/dim]"
		if got := strings.ReplaceAll(Render(line, false), wordJoiner, ""); got != in {
			t.Errorf("plain render of %q = %q, want the text unchanged", line, got)
		}
		if got := Render(line, true); got != ansiDim+Escape(in)+ansiReset {
			t.Errorf("color render of %q = %q, want the escaped text between dim and a reset", line, got)
		}
		// Two escaped strings side by side make no tag either.
		if got := Render(Escape(in)+Escape("bold]x"), true); strings.Contains(got, "\x1b[") {
			t.Errorf("%q followed by an escaped %q rendered a style: %q", in, "bold]x", got)
		}
	}
	// A closed literal is still left alone, and a bracket that cannot open a tag is not touched.
	for _, in := range []string{"[path] [y/N]", "list[0", "a [ b", "x]"} {
		if Escape(in) != in {
			t.Errorf("Escape(%q) = %q, want it unchanged", in, Escape(in))
		}
	}
}
