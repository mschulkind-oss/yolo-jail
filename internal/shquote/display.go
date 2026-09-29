package shquote

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// QuoteDisplay is Quote for a word shown to a person rather than run. A word holding a
// control character, a bidi-format character or a byte that is not UTF-8 is written in
// bash's ANSI-C form, $'…', with each of their bytes as a \xNN escape, so printing it
// sends the terminal nothing but text and pasting it into bash still gives back the word.
// Every other word is exactly Quote's.
//
// Quote itself stays shlex.quote, byte for byte: single quotes keep an ESC as an ESC, which
// is right for `bash -c` and wrong for a terminal.
func QuoteDisplay(s string) string {
	if !termsafe.HasUnsafe(s) {
		return Quote(s)
	}
	var b strings.Builder
	b.WriteString("$'")
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\'':
			b.WriteString(`\'`)
		case termsafe.Unsafe(r):
			// Byte by byte, never \u: bash decodes \u only in a UTF-8 locale.
			for _, c := range []byte(s[i : i+n]) {
				fmt.Fprintf(&b, `\x%02x`, c)
			}
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	b.WriteString("'")
	return b.String()
}

// JoinDisplay is Join with QuoteDisplay: a command line for a person to read.
func JoinDisplay(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = QuoteDisplay(a)
	}
	return strings.Join(parts, " ")
}
