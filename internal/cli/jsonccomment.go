package cli

import "strings"

// jsoncCommentSpans returns the [start, end) byte spans of every comment in a JSONC
// text — line comments (`// …` to end of line, newline excluded) and block comments
// (`/* … */`, to end of text when unterminated) — with string literals tracked so a
// `//` inside a quoted value (`"https://example"`) is not a comment.
//
// It exists because a textual editor of JSONC (setJSONCBool) and the JSONC parser
// disagree about comments at the cost of real correctness: the parser ignores a
// commented-out key while strings.Index finds it, so the writer edited the comment,
// reported success, and left the live key untouched — `wrappers enable` exiting 0 over
// a byte-identical file. Every find-the-key decision the editor makes must go through
// these spans; anything else re-derives a worse parser by accident.
//
// The scanner is deliberately byte-oriented: JSONC's structural characters (`"`, `/`,
// `*`, `\`, newline) are all ASCII, so multi-byte UTF-8 content inside strings and
// comments passes through untouched and cannot be misread as structure.
func jsoncCommentSpans(text string) [][2]int {
	var spans [][2]int
	inString := false
	escaped := false
	blockStart := -1
	for i := 0; i < len(text); i++ {
		c := text[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if blockStart >= 0 {
			if c == '*' && i+1 < len(text) && text[i+1] == '/' {
				spans = append(spans, [2]int{blockStart, i + 2})
				blockStart = -1
				i++ // consume the closing '/'
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			nl := strings.IndexByte(text[i:], '\n')
			if nl < 0 {
				return append(spans, [2]int{i, len(text)})
			}
			spans = append(spans, [2]int{i, i + nl})
			i += nl - 1 // the loop's i++ then lands on the newline itself
		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			blockStart = i
			i++
		}
	}
	if blockStart >= 0 {
		// Unterminated block comment: treat the rest of the text as comment, which is
		// what a parser reporting an error will refuse to read anyway.
		spans = append(spans, [2]int{blockStart, len(text)})
	}
	return spans
}

// coveredBy reports whether byte position i falls inside any of spans. A position at a
// span's END boundary is outside it: the span is half-open, so a key starting exactly
// where a line comment ends (impossible for `//` comments, possible after a block
// comment's closing `/`) is editable.
func coveredBy(spans [][2]int, i int) bool {
	for _, s := range spans {
		if i >= s[0] && i < s[1] {
			return true
		}
	}
	return false
}
