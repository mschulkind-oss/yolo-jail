package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
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
	"surface":          runSurface,
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
	"claude-auth": runClaudeAuth,
	"prune":       runPrune,
	"stores":      runStores,
	"nix-roots":   runNixRoots,
	"programs":    runPrograms,
	// The yolo account's verbs (commands.go). The four macos-* verbs they replaced are not
	// keys: the front door refuses them naming these (renamedVerbs).
	"yolo-account": runYoloAccount,
	// The named setups: list, show and copy one (setupcmd.go; docs/design/named-setups.md §6).
	"setup":  runSetup,
	"update": runUpdate,
	// The github-broker's two ends: `gh` is the jail side (Main routes it before the
	// front door, gh.go), `audit` the host's record of every brokered call (audit.go).
	"gh":     runGHVerb,
	"access": runAccessVerb,
	// The git-broker's jail side: git's remote helper (gitremote.go), routed early too.
	"git-remote": runGitRemoteVerb,
	"audit":      runAudit,
	"approve":    runApprove,
	// A host command a human runs, delivered to the jail (hostrun.go); Main routes it early too.
	"host-run": runHostRunVerb,
	"wait":     runWaitVerb,
	// The host clipboard's image, saved into the workspace for the agent (paste.go;
	// docs/design/image-paste.md §6.4).
	"paste": runPaste,
	// What happened to this workspace's requests, in a jail and on the host (history.go).
	"history": runHistoryVerb,
	// The named verbs for a broker ceiling and a project's request (allow.go, request.go;
	// docs/reference/config/editing.md#the-commands).
	"allow":   runAllow,
	"request": runRequest,
	// The named verb for the readable config's active_provider table (activeprovider.go).
	"active-provider": runActiveProvider,
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
// THE HOST ROUTE IS DECIDED HERE, ONCE, WHEREVER `--setup` SITS (docs/plans/notch-convergence.md
// item 10, row A3; named-setups.md §8). `--setup host-self` on a launch is the systematic
// spelling of the host exec verb, so every launch spelling carrying a host setup routes to
// `yolo host`: `yolo --setup host-self -- c`, `yolo run --setup host-self -- c`, `yolo --setup
// host-self run -- c`, `yolo run --setup host-self c` and a bare `yolo --setup host-self`. A host
// setup selected by YOLO_SETUP or the config is NOT a flag spelling and keeps the launch's
// refusal naming `yolo host` (named-setups.md §8).
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

// hostNotchArgv reports whether a launch argv (it carries the "run" token) names a setup with
// no walls through `--setup` (`host-self`, `host-yolo-account`, or a user setup whose
// confinement is `host`), and if so the `yolo host` argv that launch means: yolo's own tokens
// with the run token and every `--setup` pair consumed, then `--setup <name>` again unless the
// name is host-self (the host verb's own default), then `--` and the command. The launch is read
// by parseRunArgs, the parser that runs it, so the setup and the command boundary are the ones a
// jail launch of the same argv would have seen: `--setup` is the last one typed, and
// `yolo run --setup host-self claude --resume` hands `claude --resume` to the host verb whole.
//
// A host setup is never selected by config or YOLO_SETUP (named-setups.md §8): only the flag
// reroutes, and the launch's setup gate refuses the others naming `yolo host`.
//
// Every other token is carried over as typed, so the host parser judges it: a run flag with
// no host meaning is refused there by name (parseHostExecFlags), never dropped here.
func hostNotchArgv(args []string) ([]string, bool) {
	var opts run.Options
	parsed := parseRunArgs(args, &opts)
	// A misuse elsewhere in the argv does not keep a host setup a launch: the host parser
	// judges every token again and refuses it there, by name (parseHostExecFlags).
	if opts.SetupName == "" {
		return nil, false
	}
	_, user, _ := config.SelectedSetupFromConfig()
	s, err := setup.Lookup(opts.SetupName, user)
	if err != nil || s.Kind() != setup.KindHost {
		return nil, false
	}
	out := []string{"host"}
	if s.Name != "host-self" {
		out = append(out, "--setup", s.Name)
	}
	sawRun := false
	for i := 0; i < parsed.boundary; i++ {
		if f, ok := readAnyLaunchValueFlag(args, i); ok {
			// EVERY `--setup` is consumed: parseRunArgs already read the last one, so an earlier
			// one is overruled, as `--setup host-self --setup podman-self` is a jail launch.
			if f.name != "--setup" {
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
