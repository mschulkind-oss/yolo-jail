package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostwrap"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// hostApply is `yolo host apply` — the ergonomic spelling of `yolo apply --at host`.
// Both remain. This one used to also own --shell-init, which is removed and now refuses
// (refuseShellInit).
func hostApply(args []string, out, errw io.Writer, color bool, stdin io.Reader) int {
	// The format family is read FIRST, off the same argv, for the reason `ps` reads it
	// before its probes: a rejected value is misuse, and a run that renders first and
	// refuses afterwards spends the work on an answer nobody gets. See outputformat.go.
	format, ok := parseOutputFormat("host apply", args, errw)
	if !ok {
		return 2
	}
	assert, dryRun, revert, timing := false, false, false, false
	for i := 0; i < len(args); i++ {
		a := args[i]
		// Tokens the format parse above already consumed. This parser REFUSES an
		// unrecognized argument, so without the skip the VALUE of `--format json` arrives
		// below as one.
		if n := outputFormatTokens(args, i); n > 0 {
			i += n - 1
			continue
		}
		switch {
		case isHelpToken(a):
			fmt.Fprintln(out, hostUsage)
			return 0
		case a == "--assert":
			assert = true
		case a == "--dry-run":
			dryRun = true
		case a == "--shell-init" || strings.HasPrefix(a, "--shell-init="):
			// Refused HERE, in the parse, so nothing below it runs: no pack refresh, no
			// render, no wrapper generation, whatever else the argv asked for.
			return refuseShellInit(errw)
		case a == "--revert":
			revert = true
		case a == hostTimingFlag:
			timing = true
		default:
			fmt.Fprintf(errw, "yolo host apply: unexpected argument %q\n\n%s\n", a, hostUsage)
			return 2
		}
	}
	write := assert && !dryRun
	// IN A JAIL, NOTHING: a host apply renders into the home of whoever runs it, and in here that
	// is the jail's own, which its launch already rendered (hostapplyinjail.go). After the parse,
	// so --help and the --shell-init refusal still answer; before every stage, so a refused run
	// writes nothing at all.
	if rc, refused := refuseHostApplyInJail("yolo host apply", revert, errw); refused {
		return rc
	}
	// ABOVE EVERY STAGE, not just above the render. [OQ-RO4]'s refusal is misuse decided
	// from argv, so it ends the command here; asking applyHostFormatted to make it once left
	// a later stage (the since-removed --shell-init) running behind it, with `write` still
	// true, and the run exited 2 with an empty stdout while appending the PATH line to the
	// user's rc file (jsonRefusedForPosture).
	if jsonRefusedForPosture(format, write) {
		return refuseJSONForActingApply(errw)
	}
	// --revert is a DIFFERENT OPERATION, not a modifier of the render, so it takes the whole
	// command: it consumes the provenance record instead of writing one, has no document to
	// emit and no wrappers to generate. The flag it cannot share is refused by name rather
	// than silently ignored (hostrevert.go).
	if revert && outfmt.IsJSON(format) {
		fmt.Fprintf(errw, "yolo host apply: --revert has no document to emit — it is a "+
			"dry run by default, and its report IS the thing you read before asserting "+
			"it.\n")
		return 2
	}
	// THE TIMING SURFACE (perf-logging.md D18), past every refusal decided from argv: each
	// stage below is spanned, and the table goes to stderr so a `--format json` stdout is still
	// one document.
	finish := startHostApplyTiming(timing, errw)
	var rc int
	if revert {
		rc = hostApplyRevert(out, errw, color, write)
	} else {
		// THE DECLARED OWNERSHIP CONTRACT, above every stage for the same reason the refusal
		// above it is: a refusal that only stopped the render would leave any later stage
		// running inside a command that wrote nothing (hostmanagementgate.go).
		var refused bool
		if rc, refused = refuseHostManagement(errw); !refused {
			rc = hostApplyRefreshAndRender(out, errw, color, write, stdin, format)
		}
	}
	finish(rc)
	return rc
}

// hostApplyRevert is --revert's whole command at both spellings: the ownership gate it shares
// with nothing else (refuseHostRevert), then the withdrawal, spanned.
func hostApplyRevert(out, errw io.Writer, color, write bool) int {
	if rc, refused := refuseHostRevert(errw); refused {
		return rc
	}
	defer hostApplySpan("host_apply.revert").End()
	return hostRevert(out, errw, color, write)
}

// hostApplyRefreshAndRender is the apply proper at both spellings (OQ-7: one operation): the
// fetch-before-resolve a launch does (hostpackrefresh.go), to stderr so a `--format json`
// stdout still carries one document and nothing else, then the render, each spanned.
func hostApplyRefreshAndRender(out, errw io.Writer, color, write bool, stdin io.Reader, format string) int {
	sp := hostApplySpan("host_apply.pack_refresh")
	refreshHostPacks(errw)
	sp.End()
	defer hostApplySpan("host_apply.render").End()
	return applyHostFormatted(out, errw, color, write, stdin, format)
}

// hostApplyPerf is the collector of the host apply this process is running, nil outside one and
// whenever nothing asked to record (a nil *perf.Log spans nothing). Package state, set and
// restored by startHostApplyTiming, for one reason: two of the stages it times (the wrappers and
// the floor) sit inside applyHostSurveyed, which the launch gate's observe pass runs too, and a
// collector threaded through that render's signatures would reach every one of its callers for
// two spans. The gate's pass runs with this nil, so it records nothing of its own.
var hostApplyPerf *perf.Log

// hostApplySpan starts one of the running host apply's spans.
func hostApplySpan(name string) *perf.Span { return hostApplyPerf.Span(name) }

// startHostApplyTiming opens a host apply's timing surface (run.HostNotchTimingLog: the jail
// launch's two gates, the machine-wide file) and returns the function that ends it with the
// apply's exit code: the table when this invocation typed --timing or --verbose, one line naming
// the file when a persistent opt-in recorded, nothing otherwise.
func startHostApplyTiming(typed bool, errw io.Writer) func(rc int) {
	t := run.HostNotchTimingLog(typed, explicitVerbose(), os.Getenv, errw)
	prev := hostApplyPerf
	hostApplyPerf = t.Log
	return func(rc int) {
		hostApplyPerf = prev
		t.Report(fmt.Sprintf("yolo host apply timing (rc %d):", rc))
	}
}

// refuseShellInit is all that is left of `yolo host apply --shell-init`: a refusal that
// writes nothing and hands over the line the flag used to append.
//
// REMOVED 2026-09-27 by the maintainer's ruling (docs/reference/host-agent-environment.md,
// HE-D1): "this shell init command apperas to do nothing, and I don't th8ink it's ever safe
// so we shoud reove it." It appended the PATH line to an rc file it GUESSED from $SHELL
// (~/.zshrc for zsh, ~/.bashrc for anything else), and only under --assert, so the bare
// spelling every remedy printed was a dry run that wrote nothing. Under --assert it also
// appended after the apply it rode on had refused and said "Nothing was written."
//
// IT REFUSES rather than falling through to "unexpected argument", the way `yolo host
// wrappers enable` does: the people who type it are the ones a shipped message told to, and
// what they need is the line, not a usage dump.
func refuseShellInit(errw io.Writer) int {
	fmt.Fprintf(errw, "yolo host apply: --shell-init was removed — yolo does not edit your "+
		"shell rc.\nAdd this line to it yourself, below any line that puts ~/.local/bin on "+
		"PATH, then open a new shell:\n  %s\n`yolo check` says whether it took effect.\n",
		hostwrap.PathLine(paths.WrapDir()))
	return 2
}

// hostWrapperYolo is the yolo the generated wrappers exec, by absolute path: the running one,
// spelled as hostwrap.Running spells it from this process's PATH. A variable so a test can point
// the stage at a stub and see the stage hand it to hostwrap (hostwrapperspath_test.go).
var hostWrapperYolo = func() string { return hostwrap.Running(os.Getenv("PATH")) }

// applyHostWrappers is the wrapper-generation stage of an apply.
//
// It runs at BOTH spellings of the host apply because it lives inside applyHost, not
// inside `yolo host apply` — the two are one operation and only differ in how they are
// typed (OQ-7).
//
// Reporting follows §5.5's ruling: apply announces its OWN ACTION, and only that. It
// prints the PATH line when it created or changed the directory, and stays silent
// otherwise. It never inspects PATH, because the PATH apply can see is a fact about the
// shell that happened to invoke it rather than about the user's rc file — an observation
// that is wrong in both directions (it nags after an rc edit made in another shell, and
// says nothing after a one-off `export`). `yolo check` carries that observation instead,
// where it is both decidable and actionable.
func applyHostWrappers(pr richtext.Printer, errw io.Writer, home string, packs []*packload.Pack,
	write bool, survey *hostApplySurvey) int {
	dir := paths.WrapDirUnder(home)
	enabled := config.HostWrappersEnabled()

	if !enabled {
		// Not opted in means NO directory and no messages at all — that is what keeps
		// any of this from being a nag. The one exception is cleaning up after the key
		// is turned back OFF: leaving live wrappers on a user's PATH after they said no
		// would be the worst of both.
		plan, err := hostwrap.PlanFor(dir, "", nil)
		if err != nil {
			// Cannot determine, so nothing is noted: an unreadable wrapper dir is not a change
			// the launch gate may stop on (§4.4).
			return 0
		}
		noteWrapperPlan(survey, dir, plan)
		if !plan.Changed() {
			return 0
		}
		if !write {
			pr.Printf("  [cyan]%-20s[/cyan] would remove %d wrapper(s)  [dim]%s[/dim]",
				"host_wrappers", len(plan.Removed), dir)
			return 0
		}
		if _, err := hostwrap.Clear(dir); err != nil {
			reportWrappersFailure(errw, home, dir, "clearing them", err, false)
			return 1
		}
		pr.Printf("  [cyan]%-20s[/cyan] removed %d wrapper(s)  [dim]%s[/dim]",
			"host_wrappers", len(plan.Removed), dir)
		return 0
	}

	bins := hostwrap.Bins(packs)
	yolo := hostWrapperYolo()
	if !write {
		plan, err := hostwrap.PlanFor(dir, yolo, bins)
		if err != nil {
			reportWrappersFailure(errw, home, dir, "planning them", err, true)
			return 1
		}
		noteWrapperPlan(survey, dir, plan)
		reportDestination(pr, tierRun, plan.Changed(), "  [cyan]%-20s[/cyan] %s  [dim]%s[/dim]",
			"host_wrappers", describeWrapperPlan(plan, false), dir)
		return 0
	}
	plan, err := hostwrap.Generate(dir, yolo, bins)
	if err != nil {
		reportWrappersFailure(errw, home, dir, "generating them", err, true)
		return 1
	}
	noteWrapperPlan(survey, dir, plan)
	// Unchanged wrappers are detail, as any settled destination is (report-tiers.md, tier 2).
	reportDestination(pr, tierRun, plan.Changed(), "  [cyan]%-20s[/cyan] %s  [dim]%s[/dim]",
		"host_wrappers", describeWrapperPlan(plan, true), dir)
	if plan.Changed() {
		// The completion notice: "I just wrote these; here is what makes them take
		// effect." Conditioned on this apply's own action, never on an observation.
		pr.Printf("    [dim]add this to your shell rc to use them:[/dim]")
		pr.Printf("    [bold]%s[/bold]", hostwrap.PathLine(dir))
	}
	return 0
}

// reportWrappersFailure is the wrappers stage's failure: the error, led by the word the verdict
// names the stage by ("the wrappers stage failed"), then its next step on the lines after it
// (docs/reference/happy-path-principle.md, rule 1). It used to print the error alone.
//
// The directory is yolo's and holds nothing but the wrappers it generates, so making it a directory
// the user can write is the fix whatever the error says about it: a file where the directory goes,
// or permissions. The second step turns wrappers off, which an apply honors by leaving the
// directory alone; it is not offered when they are already off (enabled false), the stage then
// clearing only what an earlier apply wrote.
func reportWrappersFailure(errw io.Writer, home, dir, what string, err error, enabled bool) {
	fmt.Fprintf(errw, "yolo host apply: wrappers failed — %s: %v\n", what, err)
	fmt.Fprintf(errw, "  fix: make %s a directory you can write (yolo keeps only its wrappers "+
		"there), then: yolo host apply --assert\n", prettyHomePath(home, dir))
	if enabled {
		fmt.Fprintf(errw, "  or stop yolo writing wrappers: set \"host_wrappers\": false in %s\n",
			paths.UserConfigPath())
	}
}

// noteWrapperPlan records the wrapper directory's change predicate in the apply's roll-up.
//
// THE WRAPPER DIR IS A FIFTH DESTINATION, beyond the four written kinds §3.4 enumerates, and it
// is included on purpose: `applyHost` writes it, `hostwrap.Plan.Changed` is already an exact
// content predicate for it, and a launch reaching this gate arrived THROUGH one of these
// wrappers. Leaving it out would mean "the host is up to date whenever an agent launches" was
// false for the one mechanism that made the launch observable at all — a pack added since the
// last apply has no wrapper, and nothing else in the survey would say so.
func noteWrapperPlan(survey *hostApplySurvey, dir string, plan hostwrap.Plan) {
	// tierRun: the wrapper dir is generated whole and holds nothing the user wrote.
	survey.note(tierRun, "host_wrappers", "host_wrappers", dir, plan.Changed())
}

func describeWrapperPlan(plan hostwrap.Plan, wrote bool) string {
	if !plan.Changed() {
		return fmt.Sprintf("%d wrapper(s), unchanged", len(plan.Wrappers))
	}
	var parts []string
	if n := len(plan.Added); n > 0 {
		parts = append(parts, fmt.Sprintf("+%d", n))
	}
	if n := len(plan.Rewritten); n > 0 {
		parts = append(parts, fmt.Sprintf("~%d", n))
	}
	if n := len(plan.Removed); n > 0 {
		parts = append(parts, fmt.Sprintf("-%d", n))
	}
	verb := "would write"
	if wrote {
		verb = "wrote"
	}
	return fmt.Sprintf("%s %d wrapper(s) (%s)", verb, len(plan.Wrappers), strings.Join(parts, " "))
}
