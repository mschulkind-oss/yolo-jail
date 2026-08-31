// Package cli is the yolo-jail CLI root. It owns the `yolo` entry point: the
// YOLO_INVOCATION_CWD pop+chdir, the hidden `internal` namespace interception,
// the `--`->run argv rewrite, subcommand resolution, and dispatch to the thin
// command handlers. cmd/yolo/main.go is a shim that calls Main.
//
// Contracts:
//   - argv `--`→`run` rewrite over the registry's subcommand set.
//   - a bare `yolo` (or `yolo <flags>` with no subcommand) routes to `run`,
//     which opens an interactive jail shell (Python's invoke_without_command);
//     only an explicit `help`/`--help`/`-h` prints usage.
//   - YOLO_INVOCATION_CWD pop + chdir (the jail shim chdirs to the repo root).
//   - the hidden `internal` namespace is intercepted before RewriteArgv, so it
//     never participates in `--`->run rewrite semantics.
package cli

import (
	"fmt"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/crossaudit"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// Main is the `yolo` entry point. argv is the full os.Args (argv[0] is the
// program name). It returns the process exit code.
func Main(argv []string) int {
	args := argv[1:]

	// ONE install for every host process that can carry a jail↔host crossing:
	// `yolo run` hosts the fronts for `publishes: "socket"` daemons and each
	// `yolo internal daemon <name>` hosts its own listener, and both are this
	// binary. Here rather than at each listener so a new daemon is audited by
	// existing and no call site can forget.
	//
	// Costs nothing on an invocation that crosses nothing: the sink opens its
	// file on the FIRST crossing, so `yolo --version` and every command below
	// create no log and no directory. Audit-only throughout — see
	// internal/crossaudit.
	crossaudit.Install()

	if cwd := InvocationCWD(); cwd != "" {
		_ = os.Chdir(cwd)
	}

	if len(args) >= 1 && args[0] == "internal" {
		return runInternal(args[1:])
	}

	if len(args) >= 1 && args[0] == "--version" {
		// Resolve the repo root the SAME way run/check do (the shared method), so
		// an unstamped binary describes the yolo-jail repo, never the cwd's repo.
		repoRes, _ := reporoot.Resolve(os.Getenv)
		fmt.Println("yolo-jail " + version.Get(repoRes.Root))
		return 0
	}

	// Top-level help ONLY for an explicit `yolo help` / `yolo --help` / `yolo -h`.
	// A BARE `yolo` (no args) must NOT print help — it drops into an interactive
	// jail shell (the run path defaults an empty command to bash; see
	// run.runContainer). This mirrors the Python CLI's invoke_without_command=True
	// + no_args_is_help=False. `--`->run rewrite still owns `yolo <flags> -- cmd`,
	// so a `--help` after `--` is the inner command's flag, not ours.
	if wantsTopLevelHelp(args) {
		fmt.Print(richtext.Render(usageText(), isTTY(os.Stdout)))
		return 0
	}

	// `--user-layer <file>` is GLOBAL and is consumed here, before subcommand resolution,
	// so it reaches run/check/loopholes/pack alike (see userlayer.go for why one place
	// rather than four flag parsers). It must run AFTER the help branch — `yolo --help`
	// answers without touching config — and BEFORE RewriteArgv, so the flag never looks
	// like a leading positional that would make `yolo --user-layer x.jsonc -- bash`
	// resolve as an unknown command.
	args, ok := applyUserLayerFlag(args, os.Stderr)
	if !ok {
		return 1
	}

	args = RewriteArgv(args)
	sub := Subcommand(args)

	// No recognized subcommand. Two sub-cases:
	//   - bare `yolo` or `yolo <flags>` (no leading positional token) → open an
	//     interactive jail shell via `run` (Python's invoke_without_command).
	//   - a leading positional that isn't a subcommand (e.g. a typo'd `yolo
	//     chekc`) → error, so typos don't silently run in the jail. The intended
	//     way to run an arbitrary command is `yolo -- <cmd>`.
	if sub == "" {
		if hasLeadingPositional(args) {
			fmt.Fprintf(os.Stderr, "yolo: unknown command %q\n", firstPositional(args))
			return 1
		}
		return dispatchNative("run", append([]string{"run"}, args...))
	}

	if !IsNative(sub) {
		fmt.Fprintf(os.Stderr, "yolo: unknown command %q\n", sub)
		return 1
	}
	return dispatchNative(sub, args)
}

// firstPositional returns the first non-flag token before any `--`, or "".
func firstPositional(args []string) string {
	for _, a := range args {
		if a == "--" {
			return ""
		}
		if len(a) == 0 || a[0] != '-' {
			return a
		}
	}
	return ""
}

// hasLeadingPositional reports whether args has a non-flag token before any
// `--` — i.e. the user typed something that looks like a subcommand name.
func hasLeadingPositional(args []string) bool { return firstPositional(args) != "" }

// routeDecision reports how Main routes args (after the internal/--version
// interceptions), without executing anything. Pure, for tests. One of:
// "help", "run", "dispatch:<sub>", or "unknown".
func routeDecision(args []string) string {
	if wantsTopLevelHelp(args) {
		return "help"
	}
	rewritten := RewriteArgv(args)
	sub := Subcommand(rewritten)
	if sub == "" {
		if hasLeadingPositional(rewritten) {
			return "unknown"
		}
		return "run"
	}
	if !IsNative(sub) {
		return "unknown"
	}
	return "dispatch:" + sub
}

// wantsTopLevelHelp reports whether the leading token is an explicit top-level
// help request. A bare `yolo` (no args) is NOT help — it opens a jail shell.
// Only the FIRST token counts, so `yolo run --help` still dispatches to run and
// `yolo -- cmd --help` passes --help through to the inner command.
func wantsTopLevelHelp(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "--help", "-h", "help":
		return true
	}
	return false
}
