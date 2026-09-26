package tty

import "os"

// NoColorVar is the environment variable of the NO_COLOR convention
// (https://no-color.org): when it is set to any non-empty value, a program that
// adds color by default must not. An EMPTY value counts as unset — the
// convention's own rule — so `NO_COLOR= yolo check` still colors.
const NoColorVar = "NO_COLOR"

// NoColor reports whether the environment getenv reads asks for no color: its
// NO_COLOR is non-empty. A nil getenv reads this process's environment.
//
// THIS IS THE ONE PLACE YOLO READS NO_COLOR. Color below is built on it, and the
// few surfaces that add color without a terminal gate of their own — the bash
// the launcher generates for the container, the entrypoint's hand-over line, the
// provisioning stage's failure lines — call it directly, because they print from
// a process whose stream this one cannot probe.
func NoColor(getenv func(string) string) bool {
	if getenv == nil {
		getenv = os.Getenv
	}
	return getenv(NoColorVar) != ""
}

// Color is THE color gate: every yolo surface that may emit ANSI decides it
// here. Color is on only when all three hold —
//
//   - requested: the command asked for color (its Options.Color / Deps.Color);
//   - terminal: the stream it writes to is a real terminal (IsTerminal /
//     IsTerminalFile, or the caller's injectable seam over them), so a pipe or a
//     redirect never receives an escape;
//   - NoColor(getenv) is false: the NO_COLOR convention has not vetoed it.
//
// getenv is the environment the command already reads its other inputs from —
// the run pipeline's and check's injected Options.Getenv, so one invocation reads
// one environment and a test stays hermetic — or nil for this process's own.
func Color(getenv func(string) string, requested, terminal bool) bool {
	return requested && terminal && !NoColor(getenv)
}
