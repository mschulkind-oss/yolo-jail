package cli

import (
	"fmt"
	"os"
	"strings"
)

// registry is the single source of truth for the `yolo` CLI surface: it maps
// every recognized subcommand name to its handler. Membership drives argv
// rewriting and resolution (RewriteArgv/Subcommand/IsNative); the handler is
// invoked by dispatchNative. The hidden `internal` namespace is deliberately
// NOT registered here — Main intercepts it before RewriteArgv, so it never
// participates in `--`->run rewrite semantics.
var registry = map[string]func(args []string) int{
	"check":                 runCheck,
	"doctor":                runCheck, // doctor is an alias for check (same body + flag).
	"run":                   runRun,
	"ps":                    runPs,
	"loopholes":             runLoopholes,
	"config":                runConfig,
	"config-ref":            runConfigRef,
	"init":                  runInit,
	"init-user-config":      runInitUserConfig,
	"broker":                runBroker,
	"prune":                 runPrune,
	"builder":               runBuilder,
	"macos-setup":           runMacosSetup,
	"macos-teardown":        runMacosTeardown,
	"macos-unshare":         runMacosUnshare,
	"macos-fix-permissions": runMacosFixPermissions,
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
		if _, ok := registry[a]; ok {
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
		if _, ok := registry[a]; ok {
			return a
		}
		return ""
	}
	return ""
}

// IsNative reports whether the Go binary handles sub natively. All recognized
// subcommands are native.
func IsNative(sub string) bool {
	_, ok := registry[sub]
	return ok
}

// dispatchNative invokes the handler registered for sub. Callers gate on
// IsNative first, so the not-found branch is defensive only.
func dispatchNative(sub string, args []string) int {
	if fn, ok := registry[sub]; ok {
		return fn(args)
	}
	fmt.Fprintf(os.Stderr, "yolo: unimplemented command %q\n", sub)
	return 1
}

// InvocationCWD pops YOLO_INVOCATION_CWD and returns it (the jail shim sets it
// after chdir'ing to the repo root; Main chdirs back so downstream sees the
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
