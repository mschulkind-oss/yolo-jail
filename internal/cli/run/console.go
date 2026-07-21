package run

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

// richTagRe matches rich console markup ([bold red]…[/bold red], [dim]…[/dim]).
// Human chatter is NOT under the byte-parity contract (only the ordered argv,
// yolo-user-env.sh bytes, shell-quoting, and frozen host-state contracts are);
// we either RENDER the markup to ANSI (color on a TTY, so a config diff shows
// green/red) or strip it (piped output / color off).
var richTagRe = regexp.MustCompile(`\[/?[a-zA-Z][^\]]*\]`)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiCyan   = "\x1b[36m"
)

// ansiForTag maps a rich OPEN tag (lowercased inner text) to its ANSI code.
// Compound styles ("bold red") concatenate their parts. Closing tags all reset.
// An unknown style word contributes nothing (rendered as no-op), so a stray
// literal like "[path]" or "[y/N]" — which is NOT in this set — is left as
// plain text (see richToANSI's guard).
var ansiForTag = map[string]string{
	"bold": ansiBold, "dim": ansiDim, "red": ansiRed, "green": ansiGreen,
	"yellow": ansiYellow, "cyan": ansiCyan,
}

// isStyleTag reports whether the bracketed token `tag` (e.g. "[bold red]" or
// "[/dim]") is a known rich STYLE tag — every space-separated word (after the
// optional leading "/") must be a known style word. Literal brackets such as
// "[path]", "[y/N]", "[rR]" fail this and are left untouched.
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

// richToANSI renders known style tags to ANSI escapes; closing tags reset.
// Unknown/literal bracketed tokens are preserved verbatim.
func richToANSI(s string) string {
	return richTagRe.ReplaceAllStringFunc(s, func(tag string) string {
		if !isStyleTag(tag) {
			return tag // literal content, e.g. [path]
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

// stripRich removes only known STYLE tags, leaving plain text; literal
// bracketed tokens ([path], [y/N]) are preserved.
func stripRich(s string) string {
	return richTagRe.ReplaceAllStringFunc(s, func(tag string) string {
		if isStyleTag(tag) {
			return ""
		}
		return tag
	})
}

// printer writes run()'s console lines. When color is set the rich markup is
// rendered to ANSI; otherwise it is stripped to plain text. Writes to w.
type printer struct {
	w     io.Writer
	color bool
}

// render applies the color-or-strip transform to one message.
func (p printer) render(msg string) string {
	if p.color {
		return richToANSI(msg)
	}
	return stripRich(msg)
}

// print renders one console.print line + newline.
func (p printer) print(msg string) {
	fmt.Fprintln(p.w, p.render(msg))
}

// printf is print with a format string.
func (p printer) printf(format string, args ...any) {
	fmt.Fprintln(p.w, p.render(fmt.Sprintf(format, args...)))
}

// pr builds a color-aware printer for w. Color is emitted only when the run
// requested it (o.Color) AND stdout is a real terminal — never to a pipe/file,
// so redirected output stays clean.
func (o *Options) pr(w io.Writer) printer {
	return printer{w: w, color: o.Color && o.IsTTYStdout()}
}
