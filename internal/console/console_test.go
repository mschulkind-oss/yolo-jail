package console

import (
	"reflect"
	"testing"
)

func TestBadgeLines(t *testing.T) {
	// ANSI-stripped forms from check_cmd.py: "  [PASS] msg" etc.
	if got := PassLine("ok thing"); got != "  [PASS] ok thing" {
		t.Errorf("PassLine = %q", got)
	}
	if got := FailLine("bad thing"); got != "  [FAIL] bad thing" {
		t.Errorf("FailLine = %q", got)
	}
	if got := WarnLine("meh"); got != "  [WARN] meh" {
		t.Errorf("WarnLine = %q", got)
	}
}

func TestNoteLines(t *testing.T) {
	// Single line: arrow prefix only.
	if got := NoteLines("run yolo once"); !reflect.DeepEqual(got, []string{"       -> run yolo once"}) {
		t.Errorf("single NoteLines = %q", got)
	}
	// Multi-line: first arrow, rest aligned under it.
	got := NoteLines("line one\nline two\nline three")
	want := []string{
		"       -> line one",
		"          line two",
		"          line three",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("multi NoteLines =\n%q\nwant\n%q", got, want)
	}
	// Empty note: one arrow line with empty content (Python `or [note]`).
	if got := NoteLines(""); !reflect.DeepEqual(got, []string{"       -> "}) {
		t.Errorf("empty NoteLines = %q", got)
	}
	// Trailing newline dropped like str.splitlines().
	if got := NoteLines("only\n"); !reflect.DeepEqual(got, []string{"       -> only"}) {
		t.Errorf("trailing-nl NoteLines = %q", got)
	}
	// \r\n (realistic tool stderr) splits like Python's splitlines(), not into
	// one line with a trailing \r.
	gotCRLF := NoteLines("a\r\nb\r\nc")
	wantCRLF := []string{"       -> a", "          b", "          c"}
	if !reflect.DeepEqual(gotCRLF, wantCRLF) {
		t.Errorf("CRLF NoteLines =\n%q\nwant\n%q", gotCRLF, wantCRLF)
	}
	// bare \r also splits.
	if got := NoteLines("x\ry"); !reflect.DeepEqual(got, []string{"       -> x", "          y"}) {
		t.Errorf("CR NoteLines = %q", got)
	}
}
