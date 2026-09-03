package cli

// runcmd.go owns `run`'s OWN argument handling for `--help`.
//
// Why it lives here and not in the top-level help dispatch: wantsTopLevelHelp
// (cli.go) counts only the FIRST token on purpose, so that `yolo -- cmd --help`
// reaches the inner command. Widening it would trade one bug for a worse one.
// `run` therefore has to recognise `--help`/`-h` as its own flag, and it has to
// do so BEFORE it treats the remainder as a command to execute — otherwise
// `--help` is handed to the jail as an argv and comes back
// `bash: line 1: --help: command not found` (exit 127).
//
// The second half of the papercut is that help was unavailable exactly when it
// was most needed: `run` reaches config load before it would notice a help
// request, so a workspace with a broken yolo-jail.jsonc had no way to read
// run's usage at all. runHelp is pure and is called at the very top of runRun,
// which is what makes help answerable "without touching config" — the same
// property cli.go's top-level help branch documents for itself.

import (
	"io"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// runUsage is what `yolo run --help` prints. Plain text (no rich markup),
// matching its sibling subcommand usages (configUsage, packUsage, applyUsage),
// so the output is byte-stable off a TTY and in tests.
//
// The flag list is exactly the flags runRun parses. TestRunUsageListsEveryRunFlag
// pins that correspondence, so a new flag cannot land undocumented.
const runUsage = `Usage: yolo run [flags] [-- <command> [args...]]
       yolo [flags] -- <command> [args...]

Run <command> inside the jail. With no command it opens an interactive shell,
which is also what a bare 'yolo' does.

Everything AFTER '--' belongs to the inner command, including that command's own
flags: 'yolo -- claude --help' prints claude's help, not this one.

Flags:
  --new              Launch a fresh container instead of attaching to the jail
                     already running for this workspace.
  --network <mode>   Override the network mode for this launch
                     (also --network=<mode>).
  --profile <sel>   Select the active profile for this launch (also -p <sel>,
                     --profile=<sel>, -p=<sel>). Two spellings of the value: a bare
                     NAME selects it for every pack this launch selects;
                     <cli>=<name> (e.g. claude=zai, comma-separated, repeatable)
                     selects it for the named CLI only. A --profile or -p with no
                     token after it selects nothing.
  --timing           Report startup performance timings: the host-side total, plus the
                     in-container breakdown (entrypoint config generation, mise, the
                     command itself).
  --dry-run          macos-user runtime only: print the plan without launching.
  --accept-config-changes
                     Approve a changed jail config on a launch with no terminal
                     to prompt on (CI, scripts), recording it as approved just
                     as answering 'y' would. Without it such a launch is
                     refused. Per-launch by design: it is a flag rather than an
                     environment variable so an approval cannot be inherited by
                     child processes or outlive the launch it was given for.
  --help, -h         Show this help. Answered before any config is loaded, so it
                     still works when yolo-jail.jsonc does not parse.

Global options are listed by 'yolo --help'; the full config reference is
'yolo config-ref'.`

// runFlags is every flag runRun itself consumes. It exists so the usage text and
// the parser cannot drift apart silently (TestRunUsageListsEveryRunFlag), and so
// runHelpRequested's "keep scanning past a run flag" branch has one definition.
var runFlags = []string{"--new", "--profile", "--timing", "--dry-run", "--network", "--accept-config-changes"}

// applyProfileValue reads one -p/--profile value: "cli=name" (comma-separated,
// repeatable) merges into the per-CLI selection table, anything else is a bare
// profile name. Names refuse "=" at declaration (config profiles + the pack
// manifest), so a value containing "=" is unambiguously the pair grammar and a
// name can never collide with it.
func applyProfileValue(v string, opts *run.Options) {
	if !strings.Contains(v, "=") {
		opts.ProfileName = v
		return
	}
	if opts.UseProfiles == nil {
		opts.UseProfiles = make(map[string]string)
	}
	for _, pair := range strings.Split(v, ",") {
		if parts := strings.SplitN(pair, "=", 2); len(parts) == 2 {
			opts.UseProfiles[parts[0]] = parts[1]
		}
	}
}

// runHelpRequested reports whether args (the rewritten argv[1:], so it may carry
// the injected "run" token anywhere before `--`) asks for RUN's help rather than
// the inner command's.
//
// It mirrors runRun's own parse rather than scanning for the token, because two
// forms must NOT be claimed:
//
//   - anything after `--` is the inner command's argv — `yolo -- cmd --help` is
//     the invariant the first-token-only top-level rule exists to protect;
//   - anything after an implicit command start — runRun treats the first
//     unrecognized bare token as the command, so `yolo run foo --help` means
//     "run `foo --help` in the jail" and must keep meaning that.
//
// Every one of run's value flags consumes its value the same way parseRunArgs does,
// so `yolo run --network -h` reads `-h` as the network mode and `yolo -p -h` as a
// profile named "-h" — neither is the mistyped help request it might look like. -p
// and --profile used to be gentler here: a token that could not be a name left -h
// reachable, so the mistyped help got answered. OQ-PT5 (docs/reference/providers.md
// §5.2) took away their other meaning, and with it the reason to second-guess the
// next token; they are value flags now, indistinguishable from --network.
func runHelpRequested(args []string) bool {
	sawRun := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return false // the rest is the inner command's argv
		case a == "--help" || a == "-h":
			return true
		case a == "run" && !sawRun:
			sawRun = true // the injected/leading subcommand token
		case a == "--network" || a == "--profile" || a == "-p":
			i++ // its value, whatever it looks like
		case len(a) > 1 && a[0] == '-':
			// Another flag (a run flag, or a stray one runRun ignores). Keep scanning:
			// a flag never starts the implicit command.
		default:
			return false // an implicit command started here
		}
	}
	return false
}

// runHelp answers a help request for `run`: it writes run's usage to out and
// reports true, so the caller can return 0 without loading config or launching
// anything. False means args was not a help request and the caller proceeds.
func runHelp(args []string, out io.Writer) bool {
	if !runHelpRequested(args) {
		return false
	}
	io.WriteString(out, runUsage+"\n")
	return true
}

// parseRunArgs folds run's flags and its post-`--` command out of args (the
// rewritten argv[1:]) into opts. Extracted from runRun as a PURE function so the
// inner-command argv is directly assertable — `yolo -- cmd --help` must put
// `--help` in opts.Args, and that half of the R2 invariant is otherwise only
// observable by launching a container.
//
// The front-door RewriteArgv inserts "run" at the `--` position, so flags that
// preceded `--` end up BEFORE the "run" token (e.g. `yolo --new -- true` →
// [--new, run, --, true]). So it scans the WHOLE argv: skip the "run" token
// wherever it appears, parse flags until `--`, and take everything after `--` as
// the command.
//
// The fold makes NO refusal. Every value flag takes its value from the next token
// when there is one and silently takes none when there is not, --network and the
// profile flags alike. The value's GRAMMAR dispatches on itself (2026-09-03 ruling):
// a token containing "=" is <cli>=<name> pair list (comma-separated, repeatable —
// the old --pack-profile spelling, deleted 2026-09-03: it never shipped in a
// release and -p/--profile carry both grammars); anything else is a bare profile
// name. Profile names refuse "=" at declaration, so the two grammars cannot be
// ambiguous. The bare name never keys on the command after "--" — a short option
// whose meaning depends on a token further down the argv is the confusion the
// ruling removed; name the CLI explicitly when the distinction matters.
//
// `yolo -p -- claude` reaches this as [-p, run, --, claude], so -p reads the
// injected "run" as a profile name; mandatory declaration (OQ-CS6) refuses it at
// launch, naming what IS declared.
func parseRunArgs(args []string, opts *run.Options) {
	afterDashDash := false
	sawRun := false
	var cmdArgs []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if afterDashDash {
			cmdArgs = append(cmdArgs, a)
			continue
		}
		switch {
		case a == "--":
			afterDashDash = true
		case a == "run" && !sawRun:
			sawRun = true // the injected/leading subcommand token
		case a == "--new":
			opts.New = true
		case a == "--timing":
			opts.Timing = true
		// An ordinary value flag, in both spellings, glued or not. The value's
		// grammar dispatches on itself: "cli=name" pairs (comma-separated) merge into
		// the per-CLI table; a bare name is the selection for every selected pack.
		// See the function comment for the ruling and the ambiguity guard.
		case a == "--profile" || a == "-p":
			if i+1 < len(args) {
				i++
				applyProfileValue(args[i], opts)
			}
		case len(a) > len("--profile=") && strings.HasPrefix(a, "--profile="):
			applyProfileValue(strings.TrimPrefix(a, "--profile="), opts)
		case len(a) > len("-p=") && strings.HasPrefix(a, "-p="):
			applyProfileValue(strings.TrimPrefix(a, "-p="), opts)
		case a == "--dry-run":
			opts.DryRun = true
		// Spelled as a LITERAL, not as config.AcceptConfigChangesFlag, even though
		// that constant is the flag's owner and the refusal message's source. The
		// usage/parser drift guard (TestRunUsageListsEveryRunFlag) reads this
		// function's SOURCE for long-flag literals, so a constant here would make
		// the flag invisible to the one check that keeps `run --help` honest.
		// TestAcceptConfigChangesFlagMatchesTheRefusalMessage pins the two
		// spellings together instead, so the flag a refused launch is told to pass
		// is the flag this parser accepts.
		case a == "--accept-config-changes":
			opts.AcceptConfigChanges = true
		case a == "--network":
			if i+1 < len(args) {
				i++
				opts.Network = args[i]
			}
		// A NON-EMPTY value only, deliberately: a bare `--network=` falls through to
		// the default branch and starts the command, which is what it did before the
		// extraction. Preserved rather than "fixed" — this move is behavior-neutral.
		case len(a) > len("--network=") && strings.HasPrefix(a, "--network="):
			opts.Network = strings.TrimPrefix(a, "--network=")
		default:
			// An unrecognized bare token before `--` starts the command (typer
			// would error, but the front door already classified this as run).
			cmdArgs = append(cmdArgs, a)
			afterDashDash = true
		}
	}
	opts.Args = cmdArgs
}
