// Package richtext renders rich console markup ([bold], [red], [dim], …) either
// to ANSI escapes (color, on a TTY) or to plain text (piped / color off). It is
// the single shared renderer for every yolo command's human output — extracted
// from internal/cli/run's console.go so no command carries its own
// near-duplicate strip-always printer (the lost-color bug).
//
// The contract: only KNOWN style tags are touched; a stray literal bracket like
// [path] or [y/N] is left verbatim in both modes, so it can't be mangled.
//
// Architecture and invariants: docs/reference/cli-color.md
package richtext

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

// tagRe matches rich console markup: [bold red], [/dim], etc. A leading letter
// after the optional "/" keeps it from matching [y/N] or [123].
var tagRe = regexp.MustCompile(`\[/?[a-zA-Z][^\]]*\]`)

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiBlue    = "\x1b[34m"
	ansiMagenta = "\x1b[35m"
	ansiCyan    = "\x1b[36m"
)

// ansiForTag maps a rich open-tag word (lowercased) to its ANSI code. Compound
// styles ("bold red") concatenate; closing tags all reset. blue+magenta round
// out the foreground palette to six hues, enough to give each composition layer
// (defaults/host/workspace/overlay/managed) a distinct color in
// `yolo config render --explain` (docs/plans/cli-visual-polish.md).
var ansiForTag = map[string]string{
	"bold": ansiBold, "dim": ansiDim, "red": ansiRed, "green": ansiGreen,
	"yellow": ansiYellow, "blue": ansiBlue, "magenta": ansiMagenta, "cyan": ansiCyan,
}

// isStyleTag reports whether the bracketed token is a known STYLE tag — every
// space-separated word (after an optional leading "/") must be a known style
// word. Literals like [path], [y/N], [rR] fail and are left untouched.
func isStyleTag(tag string) bool {
	inner := strings.TrimSuffix(strings.TrimPrefix(tag, "["), "]")
	inner = strings.TrimPrefix(inner, "/")
	if inner == "" {
		return false
	}
	for _, w := range strings.Fields(inner) {
		if _, ok := ansiForTag[w]; !ok {
			return false
		}
	}
	return true
}

// ToANSI renders known style tags to ANSI escapes (closing tags reset); unknown
// or literal bracketed tokens are preserved verbatim.
func ToANSI(s string) string {
	return tagRe.ReplaceAllStringFunc(s, func(tag string) string {
		if !isStyleTag(tag) {
			return tag
		}
		if strings.HasPrefix(tag, "[/") {
			return ansiReset
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(tag, "["), "]")
		var b strings.Builder
		for _, w := range strings.Fields(inner) {
			b.WriteString(ansiForTag[w])
		}
		return b.String()
	})
}

// Strip removes only known STYLE tags, leaving plain text; literal bracketed
// tokens are preserved.
func Strip(s string) string {
	return tagRe.ReplaceAllStringFunc(s, func(tag string) string {
		if isStyleTag(tag) {
			return ""
		}
		return tag
	})
}

// wordJoiner is U+2060, a zero-width character that is not a letter, so a style tag with one
// after its `[` no longer matches tagRe and prints as the text it was.
const wordJoiner = "\u2060"

// openRe matches what can begin a tag: a `[`, an optional `/`, then a letter or the end of the
// string.
var openRe = regexp.MustCompile(`\[(/?(?:[a-zA-Z]|$))`)

// Escape makes s safe to embed in markup: every known style tag in it (the only tokens
// Render touches) gets a zero-width word joiner after its `[`, so it renders as literal text
// in both modes instead of restyling the line. For a string yolo did not write, such as a
// directory name a jail chose.
//
// So does every opening that no `]` in s closes. Left alone, it would run on into the markup
// written after s: `[dim]see [docs[/dim]` holds one unknown token, `[docs[/dim]`, so the
// closing tag would print as text in both modes and, on a terminal, the dim would never be
// reset. A bracket that closes inside s, and one that cannot begin a tag, are untouched.
func Escape(s string) string {
	s = tagRe.ReplaceAllStringFunc(s, func(tag string) string {
		if !isStyleTag(tag) {
			return tag
		}
		return "[" + wordJoiner + tag[1:]
	})
	// Every opening after the last `]` is unclosed; every one before it closes inside s.
	cut := strings.LastIndexByte(s, ']') + 1
	return s[:cut] + openRe.ReplaceAllString(s[cut:], "["+wordJoiner+"${1}")
}

// Render applies ToANSI when color is set, else Strip.
func Render(s string, color bool) string {
	if color {
		return ToANSI(s)
	}
	return Strip(s)
}

// Printer writes color-aware console lines to W. Construct with color already
// resolved to (requested && on a TTY) — this package does not probe the
// terminal, so redirected output stays clean by the caller's choice.
type Printer struct {
	W     io.Writer
	Color bool
}

// Print renders one line + newline.
func (p Printer) Print(msg string) { fmt.Fprintln(p.W, Render(msg, p.Color)) }

// Printf is Print with a format string.
func (p Printer) Printf(format string, args ...any) {
	fmt.Fprintln(p.W, Render(fmt.Sprintf(format, args...), p.Color))
}
