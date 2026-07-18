// Package pytext reproduces Python's repr() for strings, which validation
// error messages embed via f-string "{x!r}" (e.g. "must be a relative path
// (got 'x')"). Go's %q differs from Python repr in quote choice and escaping,
// so error-string byte-parity needs this.
//
// Source of truth: CPython unicode_repr (Objects/unicodeobject.c):
//   - quote = '\” unless the string contains '\” and not '"', then '"'
//   - escape: '\\' and the quote char -> backslash-prefixed; \t \n \r ->
//     short escapes; other C0 controls and 0x7f -> \xXX; printable ASCII ->
//     literal; >= 0x80 -> literal iff Unicode-printable, else \xXX / \uXXXX /
//     \UXXXXXXXX.
package pytext

import (
	"fmt"
	"strings"
	"unicode"
)

// Repr returns Python's repr(s) for a string.
func Repr(s string) string {
	quote := byte('\'')
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}

	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote) || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x7f:
			b.WriteRune(r)
		case isPrintable(r):
			b.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x10000:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteByte(quote)
	return b.String()
}

// isPrintable approximates Python's str.isprintable() for a single rune:
// "not in Unicode category Other (C) or Separator (Z), except ASCII space".
// Go's unicode.IsPrint is the near-complementary definition (L, M, N, P, S,
// or ASCII space), which agrees with Python for the characters that appear in
// yolo-jail config values. The ASCII-space case never reaches here (r < 0x7f
// is handled above), so no special-casing is needed.
func isPrintable(r rune) bool {
	return unicode.IsPrint(r)
}
