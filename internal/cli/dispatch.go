package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// registry is the single source of truth for the `yolo` CLI surface: it maps
// every recognized subcommand name to its handler. Membership drives argv
// rewriting and resolution (RewriteArgv/Subcommand/IsNative); the handler is
// invoked by dispatchNative. The hidden `internal` namespace is deliberately
// NOT registered here — Main intercepts it before RewriteArgv, so it never
// participates in `--`->run rewrite semantics.
var registry = map[string]func(args []string) int{
	"check":            runCheck,
	"doctor":           runCheck, // doctor is an alias for check (same body + flag).
	"run":              runRun,
	"stop":             runStop,
	"ps":               runPs,
	"loopholes":        runLoopholes,
	"config":           runConfig,
	"describe":         runDescribe,
	"apply":            runApply,
	"host":             runHost,
	"check-deps":       runCheckDeps,
	"pack":             runPack,
	"capture":          runCapture,
	"config-ref":       runConfigRef,
	"features":         runFeatures,
	"init":             runInit,
	"init-user-config": runInitUserConfig,
	// The host-daemon management surface and its retained alias. `broker` is
	// `host-daemon <verb> claude-oauth-broker` and nothing else, so every existing
	// invocation and doc reference still resolves (OQ-HD2).
	"host-daemon": runHostDaemon,
	"broker":      runBroker,
	// The host operator's verbs for the machine-wide OpenAI grant. `yolo internal
	// openai-auth` is retained as an alias into the SAME handler (internal.go), for
	// the reason runOpenAIAuth gives — the registry row is what makes this address
	// exist at all, and without it the handler is unreachable and four of this
	// package's tests cannot see it.
	"openai-auth": runOpenAIAuth,
	// The host operator's verbs for the machine's Claude login, openai-auth's twin
	// (claudeauth.go; docs/design/claude-login-without-interception.md, OQ-CL2).
	"claude-auth":           runClaudeAuth,
	"prune":                 runPrune,
	"stores":                runStores,
	"programs":              runPrograms,
	"macos-setup":           runMacosSetup,
	"macos-teardown":        runMacosTeardown,
	"macos-unshare":         runMacosUnshare,
	"macos-fix-permissions": runMacosFixPermissions,
	"update":                runUpdate,
	// The github-broker's two ends: `gh` is the jail side (Main routes it before the
	// front door, gh.go), `audit` the host's record of every brokered call (audit.go).
	"gh":    runGHVerb,
	"audit": runAudit,
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
// user-supplied text (`-p pack`, `--profile check`), so the collision is open-ended and
// listing the flags is the only closed way to describe it.
//
// DERIVED from launchValueFlags (valueflags.go), the one list the launch parsers read their
// value flags from, so a value flag those parsers learn is skipped here too without a second
// edit. runHelpRequested derives its skip from the same list.
var valueTakingFlags = func() map[string]bool {
	m := map[string]bool{
		// Stripped by StripUserLayer before RewriteArgv sees argv, so this entry is
		// defensive — it costs nothing and removes an ordering dependency.
		"--user-layer": true,
	}
	// `--with-credentials` is among them: a jail launch takes it (OQ-ES5's jail half), and this
	// skip is what lets it get there. Without it `yolo --with-credentials zai -- bash` read
	// "zai" as a command name and answered `unknown command "zai"`.
	for _, name := range launchValueFlagNames() {
		m[name] = true
	}
	return m
}()

// RewriteArgv applies the `yolo <args> -- cmd` → `yolo run <args> -- cmd`
// rewrite: if `--` is present and no positional precedes it, prepend `run`. args is
// argv[1:]; returns the (possibly) rewritten argv[1:].
//
// ANY POSITIONAL before `--` leaves argv alone, not only a subcommand name: a subcommand
// runs as itself, and anything else is a command yolo does not have, which Main refuses
// by name (`yolo chekc -- x`: unknown command "chekc"). The values of value-taking flags
// are not positionals (firstPositional skips them), so `yolo --network host -- bash` is
// still a jail launch and never the host verb.
func RewriteArgv(args []string) []string {
	dashIdx := indexOf(args, "--")
	if dashIdx < 0 {
		return args
	}
	if firstPositional(args[:dashIdx]) != "" {
		return args
	}
	// "run" goes FIRST, never at the `--`. Inserted at the separator, it sat where a value
	// flag's value sits: `yolo -p -- claude` became [-p run -- claude], which reads exactly like
	// `yolo run -p run -- claude`, so the run parser took the injected token as a profile named
	// "run" and could not tell the two apart (notch-convergence.md row A2). Leading, it is the
	// same shape as an explicit `yolo run …`, which is what the rewrite means.
	return append([]string{"run"}, args...)
}

// routeArgv is THE FRONT DOOR'S ONE DECISION: which subcommand runs args (argv[1:], after the
// global flags are consumed) and with what argv. sub is "" for an unknown command, which Main
// refuses naming firstPositional; implicit reports a launch no token asked for by name (a bare
// `yolo`, or `yolo <flags>`), which routeDecision tells apart.
//
// THE NOTCH IS DECIDED HERE, ONCE, WHEREVER `--at` SITS (docs/plans/notch-convergence.md item
// 10, row A3). `--at host` on a launch is the systematic spelling of the host exec verb
// (host-agent-environment.md OQ-2), so every launch spelling carrying it routes to `yolo host`:
// `yolo --at host -- c`, `yolo run --at host -- c`, `yolo --at host run -- c`, `yolo run --at
// host c` and a bare `yolo --at host`. Only the first used to; the rest reached the jail
// launcher and were refused there as "the host notch … a launch cannot honor it". The config
// key `confinement: host` is NOT an `--at` spelling and keeps that refusal (OQ-DP3).
func routeArgv(args []string) (sub string, routed []string, implicit bool) {
	args = RewriteArgv(args)
	sub = Subcommand(args)
	if sub == "" {
		if hasLeadingPositional(args) {
			return "", args, false
		}
		sub, args, implicit = "run", append([]string{"run"}, args...), true
	}
	if sub == "run" {
		if host, ok := hostNotchArgv(args); ok {
			return "host", host, false
		}
	}
	return sub, args, implicit
}

// hostNotchArgv reports whether a launch argv (it carries the "run" token) asks for the host
// notch with `--at host`, and if so the `yolo host` argv that launch means: yolo's own tokens
// with the run token and every `--at` pair consumed, then `--` and the command. The launch
// is read by parseRunArgs, the parser that runs it, so the notch and the command boundary are
// the ones a jail launch of the same argv would have seen: `--at` is the last one typed, and
// `yolo run --at host claude --resume` hands `claude --resume` to the host verb whole.
//
// Every other token is carried over as typed, so the host parser judges it: a run flag with
// no host meaning is refused there by name (parseHostExecFlags), never dropped here.
func hostNotchArgv(args []string) ([]string, bool) {
	var opts run.Options
	parsed := parseRunArgs(args, &opts)
	if opts.Notch != string(config.ConfinementHost) {
		return nil, false
	}
	out := []string{"host"}
	sawRun := false
	for i := 0; i < parsed.boundary; i++ {
		if f, ok := readAnyLaunchValueFlag(args, i); ok {
			// EVERY `--at` is consumed, not only `--at host`: parseRunArgs already read the last
			// one as the notch, so an earlier `--at jail` is overruled, as `--at host --at jail`
			// is a jail launch. Carried over, the host verb refused the launch it was chosen for.
			if f.name != "--at" {
				out = append(out, args[i:f.last+1]...)
			}
			i = f.last
			continue
		}
		if args[i] == "run" && !sawRun {
			sawRun = true
			continue
		}
		out = append(out, args[i])
	}
	rest := args[parsed.boundary:]
	if len(rest) > 0 && rest[0] != "--" {
		out = append(out, "--")
	}
	return append(out, rest...), true
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
//
// It is also where the startup banner is written, for every command in the
// registry and before the handler runs — one insertion instead of twenty-one,
// and it lands even when the handler goes on to hang or panic. See
// startupbanner.go for what is deliberately excluded and why the line is on
// stderr.
func dispatchNative(sub string, args []string) int {
	emitStartupBanner(os.Stderr, os.Getenv)
	// Right under the banner, for the banner's reason: every registered command
	// gets the update notice with no per-command edit (update.go).
	if rc, stop := updateHook(sub, args); stop {
		return rc
	}
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
