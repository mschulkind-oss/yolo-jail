// Package console renders the status badges and note indentation that
// `yolo check` emits, matching src/cli/check_cmd.py. Parity is defined on the
// ANSI-STRIPPED text (§3 internal/console), so the plain forms are the
// contract; the styled forms carry the equivalent colors for a real terminal.
//
// Source of truth: src/cli/check_cmd.py (ok/fail/warn/_print_note).
package console

import "strings"

// Plain badge lines (ANSI-stripped). Two leading spaces, a 6-column "[BADGE]"
// tag, then a space and the message — the alignment the Python code documents.
const (
	badgePass = "[PASS]"
	badgeFail = "[FAIL]"
	badgeWarn = "[WARN]"

	// Note indent: the first line is prefixed with an ASCII "-> " arrow (not a
	// Unicode arrow — legible on a plain terminal); continuation lines align
	// under it.
	noteFirstPrefix = "       -> "
	noteContPrefix  = "          "
)

// PassLine returns the plain-text PASS line for msg.
func PassLine(msg string) string { return "  " + badgePass + " " + msg }

// FailLine returns the plain-text FAIL line for msg.
func FailLine(msg string) string { return "  " + badgeFail + " " + msg }

// WarnLine returns the plain-text WARN line for msg.
func WarnLine(msg string) string { return "  " + badgeWarn + " " + msg }

// NoteLines renders a (possibly multi-line) note the way _print_note does:
// the first line gets the "-> " arrow, the rest align under it. Returns one
// string per output line (no trailing newline). An empty note yields a single
// line (mirrors Python's `note.splitlines() or [note]`).
func NoteLines(note string) []string {
	lines := strings.Split(note, "\n")
	// Python's str.splitlines() drops a single trailing newline; emulate by
	// trimming one trailing empty element from a trailing "\n".
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
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
