package run

import (
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// printer wraps the shared richtext renderer: color mode emits ANSI, otherwise
// known style tags are stripped (literal brackets like [path] or [y/N] survive
// verbatim in both modes). The lowercase method names keep the many
// `o.pr(w).print(...)` call sites unchanged.
//
// Human chatter is NOT under the byte-parity contract (only the ordered argv,
// yolo-user-env.sh bytes, shell-quoting, and frozen host-state contracts are);
// we either RENDER the markup to ANSI (color on a TTY, so a config diff shows
// green/red) or strip it (piped output / color off).
type printer struct{ rt richtext.Printer }

// print renders one console line + newline.
func (p printer) print(msg string) { p.rt.Print(msg) }

// printf is print with a format string.
func (p printer) printf(format string, args ...any) { p.rt.Printf(format, args...) }

// pr builds a color-aware printer for w. Color is emitted only when the run
// requested it (o.Color) AND stdout is a real terminal — never to a pipe/file,
// so redirected output stays clean.
func (o *Options) pr(w io.Writer) printer {
	return printer{rt: richtext.Printer{W: w, Color: o.Color && o.IsTTYStdout()}}
}
