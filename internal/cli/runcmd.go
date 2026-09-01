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
	"errors"
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
                     -p always takes the name as its own argument, and refuses an argv
                     where the next token cannot be one; a name spelling a flag or the
                     word "run" is written -p=<name>. Only --profile with no argument
                     reports timings — a bare -p is a parse error.
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
var runFlags = []string{"--new", "--profile", "--dry-run", "--network", "--accept-config-changes", "--pack-profile"}

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
// `yolo run --network -h` reads `-h` as the network mode, not as help. `-p` applies
// profileValueAt's guard instead of eating whatever follows, so `yolo -p -h` is
// answered as the mistyped help request it almost certainly is rather than left to
// die as errProfileNameMissing with the -h it was never offered.
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
		case a == "--network" || a == "--pack-profile":
			i++ // its value, whatever it looks like
		case a == "-p":
			if _, ok := profileValueAt(args, i); ok {
				i++
			}
		case len(a) > 1 && a[0] == '-':
			// Another flag (a run flag, or a stray one runRun ignores). Keep scanning:
			// a flag never starts the implicit command.
		default:
			return false // an implicit command started here
		}
	}
	return false
}

// errProfileNameMissing is what parseRunArgs returns when `-p` is followed by no
// token that can be a profile name. FATAL at the parse, not a silent fallback,
// because both things -p could otherwise do are lies: taking the next token selects a
// profile the user never named (see profileValueAt), and --profile's timing-report
// fallback answers a question nobody asked.
var errProfileNameMissing = errors.New("-p needs a profile name: 'yolo -p <name> [-- <command>]' " +
	"or '-p=<name>' when the name would read as a flag or as the word 'run'")

// profileValueAt reads the profile NAME that follows args[i] — the shared guard for
// the two spellings that take a name as its own token (`--profile <name>`,
// `-p <name>`).
//
// A token is NOT a name when it is:
//
//   - anything starting with `-`: the next flag, whose meaning the name must not
//     swallow (`yolo -p -- claude` would otherwise be a profile called "--");
//   - `run`: the token RewriteArgv inserts at the `--` position. It lands directly
//     after a value-taking flag that had no value of its own, so `-p` would read it
//     as a name and silently select a profile literally called "run" — a name
//     profiles-as-pack-variants.md OQ-3 rules free-form, so nothing downstream could
//     call it a typo;
//   - `--`: the separator itself.
//
// The last is subsumed by the `-` prefix; it is spelled out because the separator is
// what this guard exists to protect, not just another flag. A name that genuinely
// reads as one of these is spelled `--profile=<name>` / `-p=<name>`.
func profileValueAt(args []string, i int) (string, bool) {
	if i+1 >= len(args) {
		return "", false
	}
	v := args[i+1]
	if strings.HasPrefix(v, "-") || v == "run" || v == "--" {
		return "", false
	}
	return v, true
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
// The error return is the ONE refusal the fold makes: `-p` followed by nothing that
// can be a profile name (errProfileNameMissing). It is fatal at the parse rather
// than a launch pre-flight because both alternatives — taking the token anyway, or
// --profile's timing-report fallback — are answers to a question nobody asked, and
// because the launch pipeline is a long way past this point (config load, pack
// staging) for a mistake that is visible in the argv alone. --network and
// --pack-profile keep their older silent swallow of a missing value: their values
// are arbitrary user text, so nothing downstream can mistake a swallowed one for a
// selection. Reported as a deliberate asymmetry, not an oversight to copy.
func parseRunArgs(args []string, opts *run.Options) error {
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
			if v, ok := profileValueAt(args, i); ok {
				i++
				opts.ProfileName = v
			} else {
				opts.Profile = true
			}
		case len(a) > len("--profile=") && strings.HasPrefix(a, "--profile="):
			opts.ProfileName = strings.TrimPrefix(a, "--profile=")
		case a == "-p":
			v, ok := profileValueAt(args, i)
			if !ok {
				return errProfileNameMissing
			}
			i++
			opts.ProfileName = v
		case len(a) > len("-p=") && strings.HasPrefix(a, "-p="):
			opts.ProfileName = strings.TrimPrefix(a, "-p=")
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
	return nil
}
