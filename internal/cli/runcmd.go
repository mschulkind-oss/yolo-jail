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
  --profile <name>   Select active profile or provider preset for this launch (also -p <name>,
                     --profile=<name>, -p=<name>). The name is whatever token follows —
                     there is nothing else the flag can mean. With a command, keys the
                     profile to that command's binary; with no command, applies it to
                     every pack this launch selects. Like --network and --pack-profile, a
                     --profile or -p with no token after it selects nothing.
  --timing           Report startup performance timings: the host-side total, plus the
                     in-container breakdown (entrypoint config generation, mise, the
                     command itself).
  --pack-profile <cli>=<name>
                     Select the profile for one CLI (e.g. pi=glm,claude=bedrock). The key is
                     the binary a pack installs; an unknown one is refused at launch.
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
var runFlags = []string{"--new", "--profile", "--timing", "--dry-run", "--network", "--accept-config-changes", "--pack-profile"}

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
		case a == "--network" || a == "--pack-profile" || a == "--profile" || a == "-p":
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
// when there is one and silently takes none when there is not: --network and
// --pack-profile have always worked that way, and since OQ-PT5
// (docs/reference/providers.md) --profile and -p do too. The asymmetry the old
// note here reported is gone rather than copied — it existed only because a bare
// --profile meant something else (the startup-timing report, now --timing), which
// gave the parser a second reading to protect and a heuristic (profileValueAt) to
// guess which one the user meant. That heuristic cost two fix commits (bd2186d1,
// 8868326a) and is deleted rather than made more careful: with one meaning left,
// the next token is a name, and a missing one selects nothing.
//
// KNOWN TRANSIENT, deliberately not coded around: `yolo -p -- claude` reaches this
// as [-p, run, --, claude], so -p reads the injected "run" as a profile name. The
// guard that used to catch it is superseded later in this same cycle by mandatory
// profile declaration (provider-catalog-and-selection-plan.md step 6, OQ-CS6), which
// makes an undeclared name a reportable error at launch.
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
		// An ordinary value flag, in both spellings: the next token is the name, glued
		// or not, exactly as --network and --pack-profile read theirs. No look-ahead
		// and no fallback — see the function comment for what that replaced.
		case a == "--profile" || a == "-p":
			if i+1 < len(args) {
				i++
				opts.ProfileName = args[i]
			}
		case len(a) > len("--profile=") && strings.HasPrefix(a, "--profile="):
			opts.ProfileName = strings.TrimPrefix(a, "--profile=")
		case len(a) > len("-p=") && strings.HasPrefix(a, "-p="):
			opts.ProfileName = strings.TrimPrefix(a, "-p=")
		case a == "--pack-profile":
			if i+1 < len(args) {
				i++
				if opts.UseProfiles == nil {
					opts.UseProfiles = make(map[string]string)
				}
				for _, pair := range strings.Split(args[i], ",") {
					if parts := strings.SplitN(pair, "=", 2); len(parts) == 2 {
						opts.UseProfiles[parts[0]] = parts[1]
					}
				}
			}
		case len(a) > len("--pack-profile=") && strings.HasPrefix(a, "--pack-profile="):
			if opts.UseProfiles == nil {
				opts.UseProfiles = make(map[string]string)
			}
			val := strings.TrimPrefix(a, "--pack-profile=")
			for _, pair := range strings.Split(val, ",") {
				if parts := strings.SplitN(pair, "=", 2); len(parts) == 2 {
					opts.UseProfiles[parts[0]] = parts[1]
				}
			}
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
