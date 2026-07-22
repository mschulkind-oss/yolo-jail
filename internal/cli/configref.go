// configref.go implements `yolo config-ref` — the full configuration reference
// document. The content is an embedded static template and the closed set of
// markup tags is rendered to ANSI. The ansi* constants it uses are shared with
// markup.go (see the const block there).
package cli

import (
	_ "embed"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

//go:embed config_ref.txt
var configRefContent string

// tagReplacer maps the closed set of rich tags in the reference to ANSI. rich
// nests styles, but this document never nests beyond one level, so a flat
// open→code / close→reset mapping renders identically to the eye. Order is
// irrelevant (tags are distinct literals). `[5432]` in the text is literal
// content, not a tag, so it is left untouched.
var tagReplacer = strings.NewReplacer(
	"[bold cyan]", ansiBold+ansiCyan,
	"[/bold cyan]", ansiReset,
	"[bold yellow]", ansiBold+ansiYellow,
	"[/bold yellow]", ansiReset,
	"[bold]", ansiBold,
	"[/bold]", ansiReset,
	"[cyan]", ansiCyan,
	"[/cyan]", ansiReset,
	"[yellow]", ansiYellow,
	"[/yellow]", ansiReset,
)

// Render returns the reference document with rich tags rendered to ANSI when
// color is true, or with the tags stripped to plain text when false.
func Render(color bool) string {
	if color {
		return tagReplacer.Replace(configRefContent)
	}
	return stripTags(configRefContent)
}

// configRefRun prints the reference to w (color-on when w is a terminal, per
// the caller) and returns the exit code (always 0).
func configRefRun(w io.Writer, color bool) int {
	io.WriteString(w, Render(color))
	return 0
}

// RunStdout is the front-door entry: prints to stdout with color when stdout is
// a TTY.
func RunStdout() int {
	return configRefRun(os.Stdout, isTTY(os.Stdout))
}

// stripTags removes every rich tag, leaving plain text (for non-TTY output).
func stripTags(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '[' {
			if end := strings.IndexByte(s[i:], ']'); end >= 0 {
				tag := s[i : i+end+1]
				if isRichTag(tag) {
					i += end + 1
					continue
				}
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// isRichTag reports whether tok is one of the document's known rich tags (so
// literal bracketed text like `[5432]` is preserved).
func isRichTag(tok string) bool {
	switch tok {
	case "[bold cyan]", "[/bold cyan]", "[bold yellow]", "[/bold yellow]",
		"[bold]", "[/bold]", "[cyan]", "[/cyan]", "[yellow]", "[/yellow]":
		return true
	}
	return false
}

// isTTY reports whether f is a real terminal, via the shared ioctl probe
// (internal/tty) — not an os.ModeCharDevice stat, which false-positives on the
// container `-t` flag and /dev/null.
func isTTY(f *os.File) bool {
	return tty.IsTerminalFile(f)
}
