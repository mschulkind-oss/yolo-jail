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

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
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

A workspace whose jail is already running is ATTACHED to, not replaced — the
running session is never yanked out from under you. Ending it is deliberate and
two-stepped: 'yolo stop' (from the workspace), then an ordinary launch.

Flags:
  --at <notch>       Run at this confinement notch (jail|guest|host), overriding
                     the config's 'confinement' key for this launch (also
                     --at=<notch>). 'jail' launches the jail. 'host' runs the
                     command at the host notch, exactly as 'yolo host -- <cmd>'
                     does, wherever the flag sits ('yolo run --at host -- <cmd>'
                     included). 'guest' is not built (env-manager plan Phase 7),
                     so it is refused rather than silently downgraded to a jail.
  --network <mode>   Override the network mode for this launch, whatever the
                     config's 'network.mode' says (also --network=<mode>).
  --profile <sel>   Select the active profile for this launch (also -p <sel>,
                     --profile=<sel>, -p=<sel>). Two spellings of the value: a bare
                     NAME selects it for every agent CLI the selected packs install
                     that no pair names, and never the command after ` + "`--`" + `, as
                     at the host; <cli>=<name> (e.g. claude=zai, comma-separated,
                     repeatable) selects it for the named CLI only. A -p beats the
                     config's 'profile' key, the persistent spelling that takes the
                     same forms ('yolo config-ref'), for each CLI it selects for;
                     every CLI no -p selects for keeps the key's selection. A command
                     no profile reaches (bash, curl) gets a provider's key from
                     --with-credentials, below, at every notch.
  --with-credentials <provider[,provider...]|all>
                     GRANT this jail the named providers' claimed env_sources
                     credentials, exactly as 'yolo host --with-credentials' grants
                     its command: KEYS ONLY, no profile is selected and nothing is
                     re-pointed (no base URL, no model). 'all' is every composed
                     provider that claims a value in env_sources. Repeatable; also
                     --with-credentials=<list>. It combines with -p: an agent keeps
                     its profile and also holds the granted keys. THE GRANT IS FIXED
                     WHEN THE JAIL IS LAUNCHED: every process in the jail holds it,
                     so this session, every session attached to it later and
                     everything each one starts inherit it, and an attach asking
                     for a provider the running jail was not launched with is
                     refused ('yolo stop', then launch again with the flag); one
                     naming the jail's set, part of it, or nothing enters. On
                     macos-user each invocation is its own session and holds its
                     own grant. Every entry names what is granted, by name, never
                     by value. An unknown provider refuses, naming the known ones;
                     a named provider env_sources holds no value for is reported.
                     Nothing else implies it: not -p, not the profile key, not any
                     YOLO_ALLOW_* variable, and no config key.
  A value flag with no value (a trailing -p, '-p --', '--profile=') is refused,
  exit 2, as 'yolo host' refuses it.
  --timing           Report this launch's performance timings, start to shell return:
                     the host-side span table (probes, image load, teardown), the
                     child window (including podman's own post-exit cleanup), and
                     the in-container breakdown (entrypoint config generation, mise,
                     the command itself). Spans append to
                     <workspace>/.yolo/host-perf.log as they happen, and any span
                     over a second names itself on stderr. The global --verbose/-v
                     (before the subcommand) prints the same report.
                     ASKING IS WHAT PRINTS: the always-on spellings — YOLO_TIMING=1
                     or YOLO_VERBOSE=1 in the environment, "perf_logging": true in
                     the user config — record the same spans to the same file and
                     print nothing but one dim line naming it.
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

Examples:
  yolo                                # an interactive shell in this workspace's jail
  yolo -- claude                      # run claude in it
  yolo -- bash -lc 'just test-fast'   # one command, then exit
  yolo -p zai -- claude               # ... on the zai profile, this launch only
  yolo --with-credentials zai -- bash # a jail holding zai's key, keys only
  yolo --timing -- true               # what did this launch spend its time on?

Global options are listed by 'yolo --help'; the full config reference is
'yolo config-ref'.`

// runFlags is every flag runRun itself consumes. It exists so the usage text and
// the parser cannot drift apart silently (TestRunUsageListsEveryRunFlag), and so
// runHelpRequested's "keep scanning past a run flag" branch has one definition.
//
// ⚠ THERE IS NO QUIET FLAG FOR A LAUNCH, AND THERE IS NOT GOING TO BE ONE. This is the
// list someone reaches for to add `--quiet`, so the rule is written here rather than
// argued again in the docstring of whichever line the flag would have hidden.
//
// P4, docs/reference/report-tiers.md's principles: DISCLOSURES ARE NEVER SUPPRESSIBLE. Progress
// and provenance may be COMPRESSED to a line — that is what the boot catalog's one-liner is
// (the launch stream), and it is the whole density control a launch gets — but the decision a
// disclosure reports stays visible on every launch. A flag that could hide the host-access
// banner would delete the one thing trust-paths.md's OQ-TP9 kept when it deleted the approval
// gate; a flag that could hide only progress would save four lines.
//
// It is a RULE now rather than three independent conclusions: the version banner, the
// flake-source line and the jail line each carried their own docstring arguing they must
// stay unconditional, which is what a policy made one line at a time looks like just
// before the fourth author makes it differently. Ruled by OQ-RO3, 2026-09-11; pinned by
// TestTheLaunchHasNoQuietFlag. `YOLO_NO_BANNER` is the one pre-existing hatch and it is
// deliberately narrow — it silences the version line and nothing else.
var runFlags = []string{"--profile", "--timing", "--dry-run", "--network", "--accept-config-changes", "--at",
	"--with-credentials"}

// runKnownFlags is runFlags plus the spellings that are not policy: the short forms, help, and the
// global --verbose the front door strips before subcommand resolution (included so a launch that
// somehow still carries it is not refused for it).
//
// DERIVED from runFlags rather than retyped, so a flag added there cannot be refused by the very
// scan that is supposed to accept it — and TestTheLaunchHasNoQuietFlag keeps guarding what may go
// in that list.
func runKnownFlags() []string {
	return append(append([]string(nil), runFlags...),
		"-p", "--help", "-h", "--verbose", "-v", "run")
}

// applyProfileValue reads one -p/--profile value: "cli=name" (comma-separated,
// repeatable) merges into the per-CLI selection table, anything else is a bare
// profile name. Names refuse "=" and "," at declaration (config profiles + the pack
// manifest), so a value containing "=" is unambiguously the pair grammar and a
// name can never collide with it.
//
// A LIST (docs/design/active-provider-sets.md OQ-AP1) is carried into run.Options as the
// comma-joined entries the parser checked — the value's own spelling, split back by
// packload.SplitProfileList, the one reading a name without commas allows. A later pair for
// the same CLI replaces its whole list, as a later pair always replaced its one name.
//
// The grammar itself is parseProfileValue, which `yolo host` and `yolo host env` read too
// (ES-D27), so the two notches cannot disagree about what a -p value says. The fold is
// config.ProfileSelection's, the one the config `profile` key lowers to (PP-D10), so a flag
// and the key's equivalent form set the same two fields. A value the grammar refuses is
// returned, for the launch to refuse as misuse (exit 2) before anything starts.
func applyProfileValue(v string, opts *run.Options) error {
	sel := opts.ProfileFlags()
	if err := sel.ApplyFlag(v); err != nil {
		return err
	}
	opts.SetProfileFlags(sel)
	return nil
}

// parseProfileValue is the -p grammar every notch reads (config.ParseProfileFlag, OQ-AP1): a
// value with no "=" is a BARE list — one profile name or several separated by commas — and one
// with "=" is comma-separated cli=name pairs, a bare name after a pair continuing that CLI's
// list. What the grammar cannot place (a name before any pair, an empty entry) is refused,
// never dropped.
func parseProfileValue(v string) (bare []string, pairs map[string][]string, err error) {
	return config.ParseProfileFlag(v)
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
		case valueTakingFlags[a]:
			// Its value, whatever it looks like — read by the one value-flag reader, so a
			// value flag parseRunArgs learns is skipped here too. A flag followed by `--`
			// has no value, and the `--` is still the separator.
			if f, ok := readAnyLaunchValueFlag(args, i); ok {
				i = f.last
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

// runArgv is what parseRunArgs read from a launch's argv beyond the Options it filled.
type runArgv struct {
	// boundary is the number of LEADING tokens that are yolo's own; see parseRunArgs.
	boundary int
	// misuse is the first value flag given no value ("<flag> needs a value",
	// readValueFlag), nil when none was. runRun refuses it with exit 2, the host's code for
	// the same typo.
	misuse error
}

// parseRunArgs folds run's flags and its post-`--` command out of args (the
// rewritten argv[1:]) into opts. Extracted from runRun as a PURE function so the
// inner-command argv is directly assertable — `yolo -- cmd --help` must put
// `--help` in opts.Args, and that half of the R2 invariant is otherwise only
// observable by launching a container.
//
// The front door puts "run" FIRST (RewriteArgv: `yolo --timing -- true` →
// [run, --timing, --, true]), the same shape an explicit `yolo run …` has. It still
// skips the first "run" wherever it appears, because an explicit subcommand may follow
// global-looking flags (`yolo -p zai run -- claude`).
//
// THE FOLD REFUSES ONE THING: a value flag given no value. Every value flag is read by
// readValueFlag (valueflags.go), the one reader `yolo host` reads the same flags with,
// so `-p`, `--profile`, `--at`, `--network` and `--with-credentials` take the next token
// or a glued `=value`, and a missing or empty one is returned as runArgv.misuse ("-p
// needs a value"). It used to be swallowed, and the glued empty spellings (`--profile=`,
// `--network=`) fell to the default arm and STARTED THE COMMAND: `yolo --profile= --
// claude` ran `--profile=` inside the jail (rc 127) while the host refused nothing
// (notch-convergence.md row A2). The parse continues past a misuse, so every other field
// is still read; the caller decides.
//
// The profile value's GRAMMAR dispatches on itself (2026-09-03 ruling; parseProfileValue,
// shared with the host by ES-D27): a token containing "=" is a <cli>=<name> pair list
// (comma-separated, repeatable); anything else is a bare profile name. Profile names refuse
// "=" at declaration, so the two grammars cannot be ambiguous. The bare name never keys on
// the command after "--".
//
// # The returned boundary, and why the parser owns it
//
// runArgv.boundary is the number of LEADING tokens that are yolo's own — everything before the
// `--` separator or, when there is none, before the implicit command start. That is the only argv
// slice an unknown-flag refusal may scan (runRun), and it is returned from HERE rather than
// re-derived there because the boundary is defined by this switch: a caller recomputing it needs a
// second copy of which flags take a value, and the copy that drifts refuses a launch. `yolo run
// claude --resume` returns 1 — `--resume` is the inner command's, exactly as `yolo -- claude
// --resume`'s is.
//
// A flag-shaped token this switch does not recognise counts as yolo's and lands INSIDE the
// boundary, because a mistyped flag is the case the refusal exists for; see the default arm.
func parseRunArgs(args []string, opts *run.Options) runArgv {
	var parsed runArgv
	afterDashDash := false
	sawRun := false
	parsed.boundary = len(args)
	var cmdArgs []string
	// value reads one value flag through the shared reader, keeping the first misuse.
	value := func(f valueFlag) (string, bool) {
		if f.err != nil {
			if parsed.misuse == nil {
				parsed.misuse = f.err
			}
			return "", false
		}
		return f.value, true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if afterDashDash {
			cmdArgs = append(cmdArgs, a)
			continue
		}
		if f, ok := readValueFlag(args, i, "--profile", "-p"); ok {
			i = f.last
			if v, ok := value(f); ok {
				// A value the -p grammar refuses (OQ-AP1: a list element naming no agent, an empty
				// entry) is misuse, refused like a value flag given no value, before anything runs.
				if err := applyProfileValue(v, opts); err != nil && parsed.misuse == nil {
					parsed.misuse = err
				}
			}
			continue
		}
		// THE NOTCH, CONSUMED RATHER THAN SWALLOWED. Without it `--at` fell to the default
		// arm below, which treats the first unrecognized bare token as the start of the
		// command — so `yolo --at guest -- claude` handed the jail its own flag as argv and
		// died inside it with `--at: command not found` (DP-B22).
		//
		// The VALUE is not validated here: run.refuseUnbuiltNotch is the one place a notch
		// is judged, so `--at` and the config's `confinement` key cannot disagree about what
		// is launchable (docs/design/declaration-parity.md DP-B16/DP-B22).
		if f, ok := readValueFlag(args, i, "--at"); ok {
			i = f.last
			if v, ok := value(f); ok {
				opts.Notch = v
			}
			continue
		}
		if f, ok := readValueFlag(args, i, "--network"); ok {
			i = f.last
			if v, ok := value(f); ok {
				opts.Network = v
			}
			continue
		}
		// THE GRANT (docs/design/credential-sources-separation.md OQ-ES5's jail half, ruled
		// 2026-10-05): the host's flag, read in the host's grammar (addGrantValue: comma lists,
		// repeatable, an empty element refused as misuse) into the one field the launch reads it
		// from. Spelled as a LITERAL for TestRunUsageListsEveryRunFlag's reason, beside
		// --accept-config-changes below; TestTheJailGrantFlagIsTheHostsSpelling pins it to
		// withCredentialsFlag, the host's spelling.
		if f, ok := readValueFlag(args, i, "--with-credentials"); ok {
			i = f.last
			if v, ok := value(f); ok {
				var req hostGrantRequest
				if err := addGrantValue(&req, v); err != nil {
					if parsed.misuse == nil {
						parsed.misuse = err
					}
				} else {
					opts.WithCredentials = append(opts.WithCredentials, req.names...)
				}
			}
			continue
		}
		switch {
		case a == "--":
			afterDashDash = true
			parsed.boundary = i
		case a == "run" && !sawRun:
			sawRun = true // the injected/leading subcommand token
		case a == "--timing":
			opts.Timing = true
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
		default:
			// An unrecognized bare token before `--` starts the command (typer
			// would error, but the front door already classified this as run).
			cmdArgs = append(cmdArgs, a)
			afterDashDash = true
			// THE BOUNDARY SPLITS THIS ARM IN TWO, because the arm itself does not: it fires
			// for a MISTYPED FLAG as readily as for a command name, and those are opposite
			// answers to "whose token is this?". A flag-shaped token is yolo's — it is the
			// `--dry-runn` the refusal exists to catch — so it goes INSIDE the boundary; a
			// bare one is the command, so the boundary ends before it and everything from
			// there on is the inner program's argv.
			//
			// The fold's own behaviour is unchanged either way: both still start the command
			// here, which is why `--dry-runn` reached a real launch as a command name before
			// there was a refusal to stop it.
			if len(a) > 1 && a[0] == '-' {
				parsed.boundary = i + 1
			} else {
				parsed.boundary = i
			}
		}
	}
	opts.Args = cmdArgs
	return parsed
}
