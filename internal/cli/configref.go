// configref.go implements `yolo config-ref` — the full configuration reference
// document. The content is an embedded static template and the closed set of
// markup tags is rendered to ANSI. The ansi* constants it uses are shared with
// markup.go (see the const block there).
package cli

import (
	_ "embed"
	"io"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

//go:embed config_ref.txt
var configRefContent string

// Render returns the reference document with rich tags rendered to ANSI when
// color is true, or with the tags stripped to plain text when false.
func Render(color bool) string {
	return richtext.Render(configRefContent, color)
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
	return configRefRun(os.Stdout, colorForWriter(os.Stdout))
}

// isTTY reports whether f is a real terminal, via the shared ioctl probe
// (internal/tty) — not an os.ModeCharDevice stat, which false-positives on the
// container `-t` flag and /dev/null. It answers INTERACTION questions (may this
// prompt?); a color decision goes through colorForWriter, which adds NO_COLOR.
func isTTY(f *os.File) bool {
	return tty.IsTerminalFile(f)
}
