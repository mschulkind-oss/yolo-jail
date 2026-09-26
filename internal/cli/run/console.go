package run

import (
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
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

// pr builds a color-aware printer for w, through the one gate (tty.Color).
// Color is emitted only when the run requested it (o.Color) AND stdout is a
// real terminal — never to a pipe/file, so redirected output stays clean — AND
// the launch environment's NO_COLOR is unset or empty. That environment is
// o.Getenv, the one every other launch decision reads, so a test's fake
// environment governs color too.
func (o *Options) pr(w io.Writer) printer {
	// Some staging-only callers have no presentation stream: their job is to
	// collect derived paths, not render launch disclosure. A dependency closure
	// may still have a truthful line to emit, and a nil writer must mean silent
	// rather than a nil-interface panic.
	if w == nil {
		w = io.Discard
	}
	// o.Color is tested FIRST and alone: IsTTYStdout is consulted only when color was
	// requested, as it always was, so a staging-only Options that never set the seam
	// (and never asks for color) does not have to.
	return printer{rt: richtext.Printer{W: w, Color: o.Color && tty.Color(o.Getenv, true, o.IsTTYStdout())}}
}
