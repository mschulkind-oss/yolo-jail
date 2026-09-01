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
  --profile <name>   Select active profile or provider preset for this launch (also -p <name>).
                     With a command, keys the profile to that command's binary; with no
                     command, applies it to every pack this launch selects. Without an
                     argument at all, reports startup timings.
  --auth <mode>      Select Claude auth mode / provider preset (also --claude-auth=<mode>).
  --claude-auth <mode>
                     Select Claude auth mode / provider preset (e.g. bedrock, teams).
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
var runFlags = []string{"--new", "--profile", "--dry-run", "--network", "--accept-config-changes", "--auth", "--claude-auth", "--pack-profile"}

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
// `--network`'s value is consumed the same way runRun consumes it, so
// `yolo run --network -h` reads `-h` as the network mode, not as help.
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
		case a == "--network" || a == "--claude-auth" || a == "--auth" || a == "--pack-profile" || a == "-p":
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
		case a == "--profile":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && args[i+1] != "run" && args[i+1] != "--" {
				i++
				opts.ProfileName = args[i]
			} else {
				opts.Profile = true
			}
		case len(a) > len("--profile=") && strings.HasPrefix(a, "--profile="):
			opts.ProfileName = strings.TrimPrefix(a, "--profile=")
		case a == "-p":
			if i+1 < len(args) {
				i++
				opts.ProfileName = args[i]
			}
		case len(a) > len("-p=") && strings.HasPrefix(a, "-p="):
			opts.ProfileName = strings.TrimPrefix(a, "-p=")
		case a == "--claude-auth" || a == "--auth":
			if i+1 < len(args) {
				i++
				opts.ClaudeAuth = args[i]
			}
		case len(a) > len("--claude-auth=") && strings.HasPrefix(a, "--claude-auth="):
			opts.ClaudeAuth = strings.TrimPrefix(a, "--claude-auth=")
		case len(a) > len("--auth=") && strings.HasPrefix(a, "--auth="):
			opts.ClaudeAuth = strings.TrimPrefix(a, "--auth=")
		case a == "--pack-profile":
			if i+1 < len(args) {
				i++
				if opts.PackProfiles == nil {
					opts.PackProfiles = make(map[string]string)
				}
				for _, pair := range strings.Split(args[i], ",") {
					if parts := strings.SplitN(pair, "=", 2); len(parts) == 2 {
						opts.PackProfiles[parts[0]] = parts[1]
					}
				}
			}
		case len(a) > len("--pack-profile=") && strings.HasPrefix(a, "--pack-profile="):
			if opts.PackProfiles == nil {
				opts.PackProfiles = make(map[string]string)
			}
			val := strings.TrimPrefix(a, "--pack-profile=")
			for _, pair := range strings.Split(val, ",") {
				if parts := strings.SplitN(pair, "=", 2); len(parts) == 2 {
					opts.PackProfiles[parts[0]] = parts[1]
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
