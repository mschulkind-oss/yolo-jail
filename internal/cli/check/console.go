package check

// The status badges and note indentation that `yolo check` emits. The plain
// (ANSI-stripped) text is the contract; the styled forms carry the equivalent
// colors for a real terminal.

import "strings"

// Plain badge lines (ANSI-stripped). Two leading spaces, a one-column glyph and
// a space, a 6-column "[BADGE]" tag, then a space and the message.
const (
	badgePass = "[PASS]"
	badgeFail = "[FAIL]"
	badgeWarn = "[WARN]"
	badgeSkip = "[SKIP]"

	// The scannable glyphs before the badges (docs/plans/cli-visual-polish.md, Group C): the
	// eye finds a ✗ in a column of ✓ faster than it reads [FAIL] among [PASS]. They are literal
	// text, so the plain report carries them too; on a terminal each takes its badge's hue.
	// [SKIP] has no glyph, only the blank that keeps its badge in the column: it reports an
	// absence, and a mark would compete with the three that report a finding.
	glyphPass = "✓"
	glyphFail = "✗"
	glyphWarn = "!"
	glyphSkip = " "

	// Note indent: the first line is prefixed with an ASCII "-> " arrow (not a
	// Unicode arrow — legible on a plain terminal); continuation lines align
	// under it. The arrow sits under the badge's closing bracket, as it did
	// before the glyph column moved the badge two columns right.
	noteFirstPrefix = "         -> "
	noteContPrefix  = "            "
)

// PassLine returns the plain-text PASS line for msg.
func PassLine(msg string) string { return "  " + glyphPass + " " + badgePass + " " + msg }

// FailLine returns the plain-text FAIL line for msg.
func FailLine(msg string) string { return "  " + glyphFail + " " + badgeFail + " " + msg }

// WarnLine returns the plain-text WARN line for msg.
func WarnLine(msg string) string { return "  " + glyphWarn + " " + badgeWarn + " " + msg }

// SkipLine returns the plain-text SKIP line for msg.
func SkipLine(msg string) string { return "  " + glyphSkip + " " + badgeSkip + " " + msg }

// NoteLines renders a (possibly multi-line) note:
// the first line gets the "-> " arrow, the rest align under it. Returns one
// string per output line (no trailing newline). An empty note yields a single
// line.
func NoteLines(note string) []string {
	lines := splitlines(note)
	if len(note) == 0 {
		lines = []string{note}
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if i == 0 {
			out[i] = noteFirstPrefix + l
		} else {
			out[i] = noteContPrefix + l
		}
	}
	return out
}

// splitlines splits s on the line boundaries realistic in
// check notes (subprocess stderr surfaced in a note): \n, \r\n, \r, \v, \f,
// \x1c-\x1e, \x85, U+2028, U+2029. A single trailing terminator does NOT
// produce a trailing empty element.
func splitlines(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	var cur strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if isLineBoundary(r) {
			// \r\n counts as ONE boundary.
			if r == '\r' && i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	// A non-empty trailing segment (no terminator at EOF) is its own line;
	// a trailing terminator already flushed and leaves nothing to add.
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func isLineBoundary(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		return true
	}
	return false
}
