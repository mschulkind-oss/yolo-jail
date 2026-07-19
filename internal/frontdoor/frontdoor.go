// Package frontdoor handles the `yolo` CLI entry — argv rewriting, subcommand
// resolution, terminal indicators, and startup banner.
//
// Contracts:
//   - argv `--`→`run` rewrite over the Subcommands set.
//   - YOLO_INVOCATION_CWD pop + chdir (the jail shim chdirs to the repo root).
//   - tmux/kitten jail indicators + restore batching.
//   - startup banner platform naming: x86_64/aarch64 (platform.machine()),
//     NOT Go's amd64/arm64.
package frontdoor

import (
	"os"
	"strings"
)

// Subcommands is the set of recognized subcommand names.
var Subcommands = map[string]struct{}{
	"init": {}, "init-user-config": {}, "config-ref": {}, "prune": {},
	"check": {}, "run": {}, "ps": {}, "doctor": {}, "loopholes": {},
	"broker": {}, "builder": {}, "macos-setup": {}, "macos-teardown": {},
	"macos-unshare": {}, "macos-fix-permissions": {},
}

// RewriteArgv applies the `yolo <args> -- cmd` → `yolo run <args> -- cmd`
// rewrite: if `--` is present and nothing before it names a subcommand, insert
// `run` before the `--`. args is argv[1:]; returns the (possibly) rewritten
// argv[1:].
func RewriteArgv(args []string) []string {
	dashIdx := indexOf(args, "--")
	if dashIdx < 0 {
		return args
	}
	preDash := args[:dashIdx]
	for _, a := range preDash {
		if _, ok := Subcommands[a]; ok {
			return args
		}
	}
	out := make([]string, 0, len(args)+1)
	out = append(out, args[:dashIdx]...)
	out = append(out, "run")
	out = append(out, args[dashIdx:]...)
	return out
}

// Subcommand returns the leading subcommand: the FIRST positional (non-flag)
// argument, iff it names a subcommand; else "".
func Subcommand(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if _, ok := Subcommands[a]; ok {
			return a
		}
		return ""
	}
	return ""
}

// IsNative reports whether the Go binary handles sub natively. All recognized
// subcommands are native.
func IsNative(sub string) bool {
	_, ok := Subcommands[sub]
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
