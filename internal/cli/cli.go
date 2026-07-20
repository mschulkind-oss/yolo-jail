// Package cli is the yolo-jail CLI root. It owns the `yolo` entry point: the
// YOLO_INVOCATION_CWD pop+chdir, the hidden `internal` namespace interception,
// the `--`->run argv rewrite, subcommand resolution, and dispatch to the thin
// command handlers. cmd/yolo/main.go is a shim that calls Main.
//
// Contracts:
//   - argv `--`→`run` rewrite over the registry's subcommand set.
//   - YOLO_INVOCATION_CWD pop + chdir (the jail shim chdirs to the repo root).
//   - the hidden `internal` namespace is intercepted before RewriteArgv, so it
//     never participates in `--`->run rewrite semantics.
package cli

import (
	"fmt"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// Main is the `yolo` entry point. argv is the full os.Args (argv[0] is the
// program name). It returns the process exit code.
func Main(argv []string) int {
	args := argv[1:]

	if cwd := InvocationCWD(); cwd != "" {
		_ = os.Chdir(cwd)
	}

	if len(args) >= 1 && args[0] == "internal" {
		return runInternal(args[1:])
	}

	if len(args) >= 1 && args[0] == "--version" {
		fmt.Println("yolo-jail " + version.Get(os.Getenv("YOLO_REPO_ROOT")))
		return 0
	}

	// Top-level help. Handled before RewriteArgv so a bare `yolo`, `yolo help`,
	// `yolo --help`, or `yolo -h` prints usage and exits 0 rather than falling
	// through to the "unknown command" error. `--`->run rewrite still owns the
	// `yolo <flags> -- cmd` form, so a `--help` that follows a `--` is the inner
	// command's flag, not ours.
	if len(args) == 0 || wantsTopLevelHelp(args) {
		fmt.Print(usageText())
		return 0
	}

	args = RewriteArgv(args)
	sub := Subcommand(args)

	if !IsNative(sub) {
		fmt.Fprintf(os.Stderr, "yolo: unknown command %q\n", sub)
		return 1
	}
	return dispatchNative(sub, args)
}

// wantsTopLevelHelp reports whether the leading token is a top-level help
// request. Only the FIRST token counts, so `yolo run --help` still dispatches to
// run and `yolo -- cmd --help` passes --help through to the inner command.
func wantsTopLevelHelp(args []string) bool {
	switch args[0] {
	case "--help", "-h", "help":
		return true
	}
	return false
}
