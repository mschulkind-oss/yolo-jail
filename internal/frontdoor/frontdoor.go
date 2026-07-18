// Package frontdoor is the Go port of the `yolo` console-script entry
// (src/cli/__init__.py:main) — seam #1 of the go-port (docs/plans/go-port-plan.md
// Stage 12). The Go binary handles ported subcommands natively; for everything
// else it execs `python -m src.cli` with YOLO_GO_DELEGATED=1 set (the loop
// breaker) so the Python prelude doesn't bounce straight back to Go.
//
// Frozen contracts:
//   - argv `--`→`run` rewrite over the _SUBCOMMANDS set.
//   - YOLO_INVOCATION_CWD pop + chdir (the jail shim chdirs to the repo root).
//   - tmux/kitten jail indicators + restore batching — but ONLY when NOT
//     delegating (the plan: the Go front door must not touch indicators when
//     delegating, or Python saves the already-branded state as its restore
//     target and the terminal stays branded after exit).
//   - startup banner platform naming: x86_64/aarch64 (platform.machine()),
//     NOT Go's amd64/arm64.
//
// Source of truth: src/cli/__init__.py + src/cli/terminal.py.
package frontdoor

import (
	"os"
	"strings"
)

// Subcommands is the frozen _SUBCOMMANDS set (kept in lockstep with the Python
// registrations; cross-asserted by the drift/parity tests).
var Subcommands = map[string]struct{}{
	"init": {}, "init-user-config": {}, "config-ref": {}, "prune": {},
	"check": {}, "run": {}, "ps": {}, "doctor": {}, "loopholes": {},
	"broker": {}, "builder": {}, "macos-setup": {}, "macos-teardown": {},
	"macos-unshare": {}, "macos-fix-permissions": {},
}

// nativeSubcommands are the subcommands the Go binary handles itself (no
// delegation). Grows as slices land; everything else delegates to Python.
var nativeSubcommands = map[string]struct{}{
	// Stage 12 native slices are pure-output; wired in native.go. Start empty
	// so behavior is unchanged until each slice is byte-goldened.
}

// RewriteArgv applies the `yolo <args> -- cmd` → `yolo run <args> -- cmd`
// rewrite: if `--` is present and nothing before it names a subcommand, insert
// `run` before the `--`. Mirrors main()'s argv rewrite. args is argv[1:];
// returns the (possibly) rewritten argv[1:].
func RewriteArgv(args []string) []string {
	dashIdx := indexOf(args, "--")
	if dashIdx < 0 {
		return args
	}
	preDash := args[:dashIdx]
	for _, a := range preDash {
		if _, ok := Subcommands[a]; ok {
			return args // already names a subcommand
		}
	}
	// Insert "run" at the `--` position.
	out := make([]string, 0, len(args)+1)
	out = append(out, args[:dashIdx]...)
	out = append(out, "run")
	out = append(out, args[dashIdx:]...)
	return out
}

// Subcommand returns the leading subcommand: the FIRST positional (non-flag)
// argument, iff it names a subcommand; else "" (bare `yolo`, only flags, or an
// unrecognized first positional — which the delegated typer app would error
// on). Used to decide native vs delegate.
func Subcommand(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue // a global flag before the subcommand (e.g. --version)
		}
		if _, ok := Subcommands[a]; ok {
			return a
		}
		return "" // first positional isn't a subcommand
	}
	return ""
}

// IsNative reports whether the Go binary handles sub natively (no delegation).
func IsNative(sub string) bool {
	_, ok := nativeSubcommands[sub]
	return ok
}

// InvocationCWD pops YOLO_INVOCATION_CWD and returns it (the jail shim sets it
// after chdir'ing to the repo root; main chdirs back so downstream sees the
// user's real dir). Empty when unset.
func InvocationCWD() string {
	v := os.Getenv("YOLO_INVOCATION_CWD")
	if v != "" {
		os.Unsetenv("YOLO_INVOCATION_CWD")
	}
	return v
}

func indexOf(s []string, x string) int {
	for i, v := range s {
		if v == x {
			return i
		}
	}
	return -1
}
