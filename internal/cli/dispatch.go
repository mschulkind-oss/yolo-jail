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
	"describe":              runDescribe,
	"apply":                 runApply,
	"host":                  runHost,
	"check-deps":            runCheckDeps,
	"pack":                  runPack,
	"config-ref":            runConfigRef,
	"init":                  runInit,
	"init-user-config":      runInitUserConfig,
	"broker":                runBroker,
	"prune":                 runPrune,
	"macos-setup":           runMacosSetup,
	"macos-teardown":        runMacosTeardown,
	"macos-unshare":         runMacosUnshare,
	"macos-fix-permissions": runMacosFixPermissions,
}

// valueTakingFlags are the flags whose value is the NEXT argv token rather than being
// glued on with `=`. Any scan that looks for a SUBCOMMAND has to skip those values, or a
// value that happens to spell a subcommand name is read as one.
//
// This is not hypothetical and it is not only about new commands: `--network host` makes
// "host" a flag value that is also a registry key, so without this skip
// `yolo --network host -- bash` stops meaning "run bash in a host-networked jail" and
// starts meaning "run bash at the host notch" — a silent change of meaning, in the
// direction of running the command OUTSIDE the sandbox. Every value here is
// user-supplied text (`-p pack`, `--auth check`), so the collision is open-ended and
// listing the flags is the only closed way to describe it.
//
// runHelpRequested (runcmd.go) carries the same skip for its own scan; the two lists are
// pinned together by TestValueTakingFlagsCoverRunHelpSkips.
var valueTakingFlags = map[string]bool{
	"--at":            true,
	"--network":       true,
	"--profile":       true,
	"-p":              true,
	"--auth":          true,
	"--claude-auth":   true,
	"--agent-profile": true,
	// Stripped by StripUserLayer before RewriteArgv sees argv, so this entry is
	// defensive — it costs nothing and removes an ordering dependency.
	"--user-layer": true,
}

// namesSubcommand reports whether any token in args is a subcommand NAME, skipping the
// values of value-taking flags. `--flag=value` forms need no skip: the value cannot be a
// bare token.
func namesSubcommand(args []string) bool {
	for i := 0; i < len(args); i++ {
		if valueTakingFlags[args[i]] {
			i++ // its value, whatever it looks like
			continue
		}
		if _, ok := registry[args[i]]; ok {
			return true
		}
	}
	return false
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
	if namesSubcommand(args[:dashIdx]) {
		return args
	}
	out := make([]string, 0, len(args)+1)
	out = append(out, args[:dashIdx]...)
	out = append(out, "run")
	out = append(out, args[dashIdx:]...)
	return out
}

// Subcommand returns the leading subcommand: the FIRST positional (non-flag)
// argument, iff it names a subcommand; else "".
//
// The value of a value-taking flag is skipped rather than treated as that positional —
// see valueTakingFlags for why `yolo --network host` must not resolve to the `host`
// subcommand.
func Subcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if valueTakingFlags[a] {
			i++ // its value, whatever it looks like
			continue
		}
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
