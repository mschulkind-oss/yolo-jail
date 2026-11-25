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

// verbatim prints text between head and tail, which are markup, byte for byte. A config value
// is shown so a reader can compare it, and copy it, exactly: through the markup printer a
// bracketed style word in it (`[bold]`) would be read as markup and dropped, and escaped it would
// carry an invisible U+2060 into anything pasted from the screen.
func (p printer) verbatim(head, text, tail string) {
	_, _ = io.WriteString(p.rt.W, richtext.Render(head, p.rt.Color)+text+richtext.Render(tail, p.rt.Color)+"\n")
}

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

// pr builds a color-aware printer for w, a writer on the LAUNCH STREAM: o.Stderr, or a
// writer derived from it. Every line the launcher itself prints during a launch is written
// through it to stderr, because the launcher's stdout is the jailed command's stdout
// (launchlog.go states the rule and lists the few outputs that are a verb's payload, which
// take prOut instead).
//
// Color goes through the one gate (tty.Color, docs/reference/cli-color.md): emitted only
// when the run requested it (o.Color) AND the launch stream is a real terminal — never to a
// pipe/file, so redirected output stays clean — AND the launch environment's NO_COLOR is
// unset or empty. That environment is o.Getenv, the one every other launch decision reads,
// so a test's fake environment governs color too.
func (o *Options) pr(w io.Writer) printer {
	return printer{rt: richtext.Printer{W: orDiscard(w), Color: o.streamColor()}}
}

// prOut is pr for a writer on STDOUT, for output that IS a verb's payload rather than a
// launch line (`yolo config apply`'s result report); its color asks whether stdout is a
// terminal. A launch line never takes it.
func (o *Options) prOut(w io.Writer) printer {
	// o.Color is tested FIRST and alone: IsTTYStdout is consulted only when color was
	// requested, so an Options that never set the seam (and never asks for color) need not.
	return printer{rt: richtext.Printer{W: orDiscard(w), Color: o.Color && tty.Color(o.Getenv, true, o.IsTTYStdout())}}
}

// streamColor is whether this launch renders color on its launch stream, which is what pr asks.
// The keeper's lines reach the launcher's terminal through the relay, so the launcher hands this
// answer to its keeper (keeperPlan.Color), whose own stderr is a pipe; the keeper answers with it
// (streamTTY).
func (o *Options) streamColor() bool {
	// o.Color is tested FIRST and alone, as in prOut: a staging-only Options that never set the
	// seams (and never asks for color) need not.
	if !o.Color {
		return false
	}
	terminal := false
	switch {
	case o.streamTTY != nil:
		terminal = o.streamTTY()
	case o.IsTTYStderr != nil:
		terminal = o.IsTTYStderr()
	}
	return tty.Color(o.Getenv, true, terminal)
}

// orDiscard is w, or io.Discard for nil. Some staging-only callers have no presentation stream:
// their job is to collect derived paths, not render launch disclosure. A dependency closure may
// still have a truthful line to emit, and a nil writer must mean silent rather than a
// nil-interface panic.
func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}
