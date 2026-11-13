package check

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBadgeLines(t *testing.T) {
	// ANSI-stripped forms: a glyph column before the badge (docs/plans/cli-visual-polish.md,
	// Group C), blank for [SKIP] so its badge stays in the column.
	if got := PassLine("ok thing"); got != "  ✓ [PASS] ok thing" {
		t.Errorf("PassLine = %q", got)
	}
	if got := FailLine("bad thing"); got != "  ✗ [FAIL] bad thing" {
		t.Errorf("FailLine = %q", got)
	}
	if got := WarnLine("meh"); got != "  ! [WARN] meh" {
		t.Errorf("WarnLine = %q", got)
	}
	if got := SkipLine("not here"); got != "    [SKIP] not here" {
		t.Errorf("SkipLine = %q", got)
	}
}

// TestTheReporterPrintsAGlyphBeforeEachBadge drives the reporter's four graded paths, not the
// console.go helpers alone: each plain line is its helper's line, each glyph takes its badge's
// hue on a terminal, and the colored report without its escapes is the plain one byte for byte.
func TestTheReporterPrintsAGlyphBeforeEachBadge(t *testing.T) {
	emit := func(r *reporter) {
		r.ok("fine")
		r.okPath("path: ", "/p", "")
		r.fail("broken", "")
		r.warn("odd", "")
		r.skip("elsewhere", "")
	}
	var plain, colored bytes.Buffer
	emit(newReporter(&plain, false))
	emit(newReporter(&colored, true))

	want := strings.Join([]string{PassLine("fine"), PassLine("path: /p"), FailLine("broken"),
		WarnLine("odd"), SkipLine("elsewhere")}, "\n") + "\n"
	if plain.String() != want {
		t.Errorf("plain report =\n%q\nwant\n%q", plain.String(), want)
	}
	for _, glyph := range []string{ansiGreen + "✓" + ansiReset, ansiRed + "✗" + ansiReset,
		ansiYellow + "!" + ansiReset} {
		if !strings.Contains(colored.String(), "  "+glyph+" ") {
			t.Errorf("colored report lacks the glyph %q:\n%q", glyph, colored.String())
		}
	}
	if got := stripANSI(colored.String()); got != plain.String() {
		t.Errorf("stripped colored report = %q, want the plain %q", got, plain.String())
	}
}

func TestNoteLines(t *testing.T) {
	// Single line: arrow prefix only, under the badge's closing bracket.
	if got := NoteLines("run yolo once"); !reflect.DeepEqual(got, []string{"         -> run yolo once"}) {
		t.Errorf("single NoteLines = %q", got)
	}
	column := func(s, sub string) int { return utf8.RuneCountInString(s[:strings.Index(s, sub)]) }
	if i := column(FailLine("x"), "]"); column(NoteLines("y")[0], "->") != i {
		t.Errorf("the note arrow is not under the badge's closing bracket (column %d)", i)
	}
	// Multi-line: first arrow, rest aligned under it.
	got := NoteLines("line one\nline two\nline three")
	want := []string{
		"         -> line one",
		"            line two",
		"            line three",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("multi NoteLines =\n%q\nwant\n%q", got, want)
	}
	// Empty note: one arrow line with empty content.
	if got := NoteLines(""); !reflect.DeepEqual(got, []string{"         -> "}) {
		t.Errorf("empty NoteLines = %q", got)
	}
	// A single trailing newline is dropped.
	if got := NoteLines("only\n"); !reflect.DeepEqual(got, []string{"         -> only"}) {
		t.Errorf("trailing-nl NoteLines = %q", got)
	}
	// \r\n (realistic tool stderr) splits on the boundary, not into
	// one line with a trailing \r.
	gotCRLF := NoteLines("a\r\nb\r\nc")
	wantCRLF := []string{"         -> a", "            b", "            c"}
	if !reflect.DeepEqual(gotCRLF, wantCRLF) {
		t.Errorf("CRLF NoteLines =\n%q\nwant\n%q", gotCRLF, wantCRLF)
	}
	// bare \r also splits.
	if got := NoteLines("x\ry"); !reflect.DeepEqual(got, []string{"         -> x", "            y"}) {
		t.Errorf("CR NoteLines = %q", got)
	}
}
