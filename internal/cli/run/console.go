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

// noColorEnvArgs is the `-e NO_COLOR=<value>` pair that carries the launch
// environment's NO_COLOR into the container, or nothing when it is unset or empty
// (the convention's definition of "not set", https://no-color.org).
//
// A user who asked the host for no color asked it of the jail too: the in-jail
// `yolo`, the entrypoint's hand-over line, the shell prompt, and any agent CLI
// that honors the convention all read it there. Carried like TERM and COLORTERM,
// and like them only when present — an empty `-e NO_COLOR=` would reach a program
// that tests for presence rather than value as a request for no color.
//
// ONE builder for BOTH argvs that start a process in the jail: assembleRunCmd's
// fresh launch and attachExisting's `exec` (through attachNoColorEnvArgs). The exec
// needs its own copy because a running container's environment is the one it was
// launched with, so a NO_COLOR set only at attach would otherwise never reach the
// command attached to.
func (o *Options) noColorEnvArgs() []string {
	if !tty.NoColor(o.Getenv) {
		return nil
	}
	return []string{"-e", tty.NoColorVar + "=" + o.Getenv(tty.NoColorVar)}
}

// attachNoColorEnvArgs is the attach exec's NO_COLOR, and it answers for BOTH ways a
// running container's frozen launch environment (envLines, from inspect) can disagree
// with this invocation. Set here: noColorEnvArgs carries it. Unset here but frozen
// non-empty — the jail was launched with NO_COLOR — and every exec would inherit it, so
// the entrypoint, the prompt and the agents would stay colorless while the host half of
// this same attach colors. That pair is `-e NO_COLOR=`: EMPTY, which the convention
// counts as unset, because `podman exec` cannot remove a variable outright.
//
// The empty pair goes only to a container whose inspect SHOWED a non-empty NO_COLOR.
// A jail launched without it never gains one (noColorEnvArgs says why an empty value
// is otherwise avoided), and an inspect that answered nothing proves nothing, so it
// changes nothing.
func (o *Options) attachNoColorEnvArgs(envLines []string) []string {
	if args := o.noColorEnvArgs(); args != nil {
		return args
	}
	if envLineValue(envLines, tty.NoColorVar) != "" {
		return []string{"-e", tty.NoColorVar + "="}
	}
	return nil
}

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
