// Package termsafe renders text another party chose so that printing it sends a terminal
// nothing but text. Host yolo prints strings a jail's agent can shape, such as a path
// under the workspace or an argv the jail sent, and a terminal acts on an ESC, CSI or OSC
// sequence in one: it moves the cursor, clears lines, retitles the window, or writes the
// clipboard (OSC 52).
//
// It ESCAPES rather than strips, so the reader sees that something odd was there: an ESC
// becomes the four characters `\x1b`, and a right-to-left override the six characters
// `\u202e`. The escapes are Go's, as strconv.Quote writes them.
package termsafe

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Unsafe reports whether a terminal may act on r: a C0 or C1 control character, DEL, or a
// bidi-format character, which reorders what the reader sees.
func Unsafe(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)
}

// HasUnsafe reports whether s holds an unsafe rune or a byte that is not UTF-8.
func HasUnsafe(s string) bool {
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if (r == utf8.RuneError && n == 1) || Unsafe(r) {
			return true
		}
		i += n
	}
	return false
}

// Visible returns s with every unsafe rune, and every byte that is not UTF-8, written as a
// visible escape. A string with none is returned unchanged.
func Visible(s string) string {
	if !HasUnsafe(s) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			b.WriteString(`\x` + strconv.FormatUint(uint64(s[i])|0x100, 16)[1:])
		case Unsafe(r):
			q := strconv.QuoteRuneToASCII(r)
			b.WriteString(q[1 : len(q)-1])
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

// VisibleLines is Visible applied to each line of s: a newline stays a newline, for a message yolo
// wrote on several lines around text another party chose. Every other unsafe rune is escaped, so
// a name that may hold a newline goes through Visible before it joins such a message.
func VisibleLines(s string) string {
	if !HasUnsafe(s) {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = Visible(l)
	}
	return strings.Join(lines, "\n")
}
