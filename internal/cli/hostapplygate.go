package cli

// hostapplygate.go is THE LAUNCH HOOK: at `yolo host -- <bin>`, behave like a jail launch and
// then exec (docs/reference/host-apply-staleness.md §4.1, §4.3, §4.4).
//
// # Why the launch is the only moment
//
// `yolo host apply` renders pack surfaces into the real $HOME and nothing ever re-checks them,
// so the rendered and would-be-rendered states drift apart silently. Agents read their config
// at startup and do not reload it, so a render that is stale while nothing reads it is not a
// problem and the same render stale at the instant an agent starts is the whole problem (§1
// P1). Every generated wrapper already execs through here, which makes this the one place the
// question is worth asking — and the reason the design adds no per-command check, no
// fingerprint and no standalone notice.
//
// # What it compares
//
// The RENDER, not the config (OQ-HS9). A host approval snapshot mirroring the jail's
// `approvals/<name>.json` would be cheaper and needs no predicate, and it is structurally blind
// to a hand-edited `~/.claude/settings.json`: the config never moved, so nothing would prompt.
// The comparison runs through applyHostSurveyed — the apply itself, in observe posture, with
// its output captured — so the gate cannot describe a render the apply would not perform.
//
// # What it never does
//
//   - It does not add a second confirmation, and it answers none of the apply's. The one-way
//     doors stay `confirmHostLosses` and the skills/briefing adoption, retire and dependency
//     gates (§7). The one this hook reaches is the first-apply entry loss, shown on a TTY
//     (hostApplyGateApplyInteractive). Every other question means the hook renders nothing
//     and names `yolo host apply --assert`. Its unattended apply is buffered, so a question
//     there is one nobody can see.
//   - It never applies an incomplete pack set. A configured pack that does not resolve means
//     nothing is rendered, the same no-half-states rule `yolo host apply --assert` refuses by.
//   - It does not run in a jail, at all. `render.Host` targets the invoking user's real home
//     and paths.Home() in a jail is /home/agent, so there is no host home in here to be stale.
//   - It does not touch `yolo run` or `yolo host apply`, and it takes no approval of its own:
//     no flag and no environment variable answers the first-apply question off a terminal
//     (OQ-NC10).

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// THERE IS NO HOST LAUNCH APPROVAL (docs/plans/notch-convergence.md OQ-NC10, ruled 2026-09-28).
// OQ-HS10 designed one, an environment variable for the wrapper path, whose fixed body
// (`exec yolo host -- claude "$@"`) has no slot for a flag. Its last reader went with the
// zero-prompt auto-apply, which re-renders a stale home without asking, and the one question
// this gate still asks — a first apply dropping MCP servers yolo does not declare — is a one-way
// door kept behind a terminal: off one the launch refuses and names `yolo host apply --assert`,
// which asks it where it can be answered. So the variable was retired rather than given that
// door, and `--accept-config-changes` stays refused at the host by name (jailOnlyRunFlags).

// hostApplyGateBudget bounds the observe pass (§4.4).
//
// A STUCK-DETECTOR, NOT A TUNING KNOB, which is why there is no config key and no environment
// variable for it: the observe pass measured 11.4 ms warm (§5), so a second is three orders of
// magnitude of headroom and anything past it means a cold or network-mounted $HOME rather than
// a value someone should be adjusting. On expiry the gate reports cannot-determine and execs —
// a launch must never hang on a check it can decline to make.
//
// A package var only so a test can shrink it, following flockSyscall's convention. Nothing but
// a test reassigns it.
var hostApplyGateBudget = time.Second

// hostApplyGateSurvey is the observe pass, behind a seam, and the seam exists for ONE reason:
// a budget overrun cannot be provoked deterministically by shrinking the budget.
//
// THE RACE, measured on CI 2026-09-12. The select below has two ready-able cases, and Go picks
// UNIFORMLY AT RANDOM among the ready ones. A test that set the budget to a nanosecond was
// betting that the survey is slower than a timer that is ready almost immediately — true on a
// cold developer machine, false on a CI runner whose page cache makes the whole observe pass
// finish first. It refused instead of execing, and `TestHostApplyGateExecsWhenTheBudgetExpires`
// failed on main for a race that had been latent since the test was written.
//
// Shrinking the budget further cannot fix it: the bet is on a comparison between two durations
// the test controls only one of. The fix is to control the OTHER one — a test substitutes a
// survey that does not return, so `done` is never ready and the timeout branch is the only one
// that can be selected. Production is untouched; this var is only ever reassigned by a test.
var hostApplyGateSurvey = applyHostSurveyed

// hostGateCanPrompt reports whether this process can put a question to a human.
//
// STDIN, not stdout, and the difference is a real case: `claude --print foo > out.txt` has a
// redirected stdout and a perfectly good terminal on stdin, and refusing that launch as
// "nobody to ask" would be false. It is the same probe the jail's own approval gate uses
// (run.Options.IsTTYStdin, read by config.CheckConfigChanges), so the two notches agree about
// what "no TTY" means.
//
// A package var for hostApplyGateBudget's reason.
var hostGateCanPrompt = func() bool { return isTTY(os.Stdin) }

// hostApplyGate implements §4.3's table. It returns true to proceed with the exec and false to
// abort the launch; every false path has already explained itself and named a remedy (§1 P5).
//
// errw, not out: everything printed here lands on stderr, including the prompt. A wrapped
// launch is one exec away from being the agent, and an agent's stdout is routinely parsed
// (`claude --print`), so a gate that wrote to stdout would corrupt the very launches it is
// least entitled to disturb.
func hostApplyGate(errw io.Writer, stdin io.Reader, bin string) bool {
	// IN-JAIL IS A HARD NO-OP, checked first and before the key: the config an in-jail process
	// reads is the generated snapshot, and the inherit census strips this key from it — but a
	// hand-written config or an older snapshot could still carry it, and there is no host home
	// in here for it to be about. The discriminator is config.InJail(), the same one every
	// other in-jail behaviour reads.
	if config.InJail() {
		return true
	}
	// NOT OPTED IN IS TOTAL SILENCE. The default, and what makes "no launch and no command
	// mentions any of this" true for everyone who never asked (§11).
	if !config.HostApplyOnLaunchEnabled() {
		return true
	}
	// NOTHING TO CHECK WITHOUT A RENDER. Under `host_management: none` yolo writes no host
	// surface, so there is no staleness for this gate to find and the check is a NO-OP rather
	// than a nag (config-ownership-and-promotion.md §4.1). The two keys are orthogonal and both
	// are read — this one says HOW the host renders, host_apply_on_launch says WHEN a re-render
	// is checked (§4.4).
	//
	// ⚠ `own` USED TO SHARE THIS EXIT and no longer does. While whole-file composition was
	// unbuilt the apply this gate offers to run refused, so prompting would have stopped
	// launches over a question with no yes; now it renders, so an owned home goes stale exactly
	// the way an asserted one does — and MORE consequentially, since under `own` the file is
	// derived output and a stale render is a file that disagrees with its own definition.
	// Whatever renders is what this checks.
	//
	// Ordered after the opt-in, not before it, so the overwhelmingly common case (the key off)
	// still reads the user config exactly once.
	if config.HostManagementMode() == config.HostManagementNone {
		return true
	}
	// A CONFIG THE LAUNCH REFUSES IS NOT RENDERED FIRST. The composition after this gate runs
	// the provider and profile section of validation and refuses on it, so the gate asks the same
	// question before its observe pass rather than auto-applying a render of that config and
	// only then refusing: measured with the retired `use_profiles`, the launch printed
	// "synchronized" for an apply that had just deselected the home's profile, and then refused.
	// The render composition refuses it too (composeHostInputs), which alone would make this a
	// cannot-determine followed by the refusal; asked here, the launch says one thing.
	if err := hostProviderSectionRefusal(config.UserScopeConfigOrEmpty(), nil); err != nil {
		fmt.Fprintf(errw, "yolo host: refusing to launch %s: %v\n", bin, err)
		return false
	}

	// ONE WRITER PER HOME (§4.6, hostapplylock.go), taken around the WHOLE observe-then-write
	// sequence rather than around the write alone. Locking only the apply would leave the
	// window that matters open: this launch's survey could read a home another process is
	// halfway through applying, conclude "out of date", and then prompt about drift that no
	// longer exists by the time it asks.
	//
	// Resolved the same way applyHost resolves it — os.UserHomeDir, not paths.Home() — so the
	// lock and the render cannot end up keyed to two different homes. A home yolo cannot even
	// resolve is cannot-determine on its own terms; applyHost would fail on it too.
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(errw, "yolo host: could not check your host render (cannot resolve your "+
			"home: %v) — launching %s anyway.\n", err, bin)
		return true
	}
	lock := tryHostApplyLock(home)
	if lock == nil {
		// Another process holds it (or the state dir is unwritable). Not a state this launch
		// can resolve or describe — §4.4's cannot-determine class, so exec and let the other
		// writer finish.
		fmt.Fprintf(errw, "yolo host: another `yolo host apply` is running for %s — launching "+
			"%s without re-checking the render.\n", home, bin)
		return true
	}
	// Held for exactly this function, which ends before the exec. Go opens with O_CLOEXEC so
	// the kernel would drop the fd at exec anyway, but a lock whose lifetime is "until this
	// function returns" is one a reader can reason about without knowing that.
	//
	// THE LOCK IS THE LAUNCH PATH'S, NOT THE COMMAND'S, and that boundary is §4.6's own: the
	// concurrent writer this design introduces is the wrapped launch, while `yolo host apply`
	// *"has no lock today because its caller was always a human running one command."* So an
	// explicit apply run alongside a gated launch is still unserialised. Closing that would
	// mean either making the command WAIT on a launch that may be sitting at a [y/N] — an
	// unbounded pause on someone else's terminal — or making it refuse, which is a new failure
	// mode for a shipping command that §7 asks this design not to touch.
	defer lock.Close()

	survey, why := surveyHostApplyWithinBudget()
	// AN INCOMPLETE PACK SET RENDERS NOTHING (no half states — the same rule `yolo host apply
	// --assert` refuses by). Checked before cannot-determine, because the observe pass DID
	// answer: it named the packs it could not resolve, and that is the loud line the user needs.
	// A pack whose manifest has problems is one of them (resolveConfiguredPack, NS-D14), named
	// with its problems, rather than applied from whatever part of its manifest decoded.
	//
	// THE LAUNCH REFUSES (docs/plans/notch-convergence.md NC-D5): a launch-shaped verb refuses a
	// pack set it cannot complete at every notch, and a jail launch refuses this config too. The
	// hook used to report the set and launch anyway, on §4.4's rule that a pack-set fault is never
	// a reason to stop a launch; the composition behind this hook now refuses the same set, so
	// "launching against your last apply" would have been followed by a refusal.
	if survey != nil && len(survey.UnresolvedPacks()) > 0 {
		// A pack whose only fault is a contribution this yolo cannot read is not one the launch
		// refuses: the composition reads it as this yolo can and says what it skips
		// (docs/design/patched-forks.md PF-D68). So with nothing else wrong the hook renders
		// nothing, as for any incomplete set, and launches on the last apply (PF-D70).
		if skewOnly(survey.UnresolvedPacks()) {
			reportHostApplyGateSkewedSet(errw, bin, survey.UnresolvedPacks())
			return true
		}
		reportHostApplyGateIncompleteSet(errw, bin, survey.UnresolvedPacks())
		return false
	}
	if survey == nil {
		// CANNOT DETERMINE (§4.4): an observe pass that failed on its own terms (an inert pack,
		// an unresolvable config-overlay, a doubly-owned surface), an unreadable home, a budget
		// overrun. An unresolvable or malformed PACK is not here: the branch above names it.
		// The predicate has no answer, so there is no change to refuse over — exec, with at
		// most one line. Per internal/version's srcskew house rule, a gate that cannot prove
		// its condition does not fire.
		fmt.Fprintf(errw, "yolo host: could not check whether your host render is up to date "+
			"(%s) — launching %s anyway.\n", why, bin)
		return true
	}
	// WHAT CANNOT BE WRITTEN, split by whether this program reads it ([OQ-HS17]). The observe
	// pass sees a broken link (a render ERROR is a write-time failure, and a dry run that meets
	// one returns cannot-determine above). One in the launched program's own configuration
	// refuses: launching it against a file yolo could not write is the half-applied home
	// OQ-HS14 refuses. One anywhere else is reported and the program launches.
	related, unrelated, _ := splitLaunchFailures(survey, bin)
	if len(related) > 0 {
		refuseLaunchOverFailures(errw, home, bin, related)
		return false
	}
	if !survey.Changes() {
		// The common case, and the one R3 is about: silence. A freshly-applied home must
		// prompt not at all, ever, until something actually changes. A standing failure in
		// another program's configuration is the one thing said here, because nothing else
		// will say it until the next explicit apply — and so is a declared dependency this
		// launch's PATH lacks, the miss line being a disclosure rather than a prompt
		// (host-agent-environment.md's miss line: it prints on every miss).
		reportGateMisses(errw, survey, bin)
		reportUnrelatedLaunchFailures(errw, home, bin, unrelated)
		return true
	}

	// AN APPLY THAT WOULD ASK SOMETHING IS NOT RUN FROM HERE. The auto-apply below runs with its
	// report buffered, so any question it asked would be one the user cannot see: measured, that
	// was a launch hanging after the banner on a TTY (Enter meant no, a blind `y` moved a skill),
	// a silent "no" off one followed by a "synchronized" line for work that did not happen, and a
	// declined install of ANOTHER pack's missing binary refusing this program's launch. So the
	// observe pass lists every question an --assert would ask (PendingDecisions), and if there is
	// any, NOTHING is rendered, the questions are printed where the user can see them, and the
	// program launches against the render already in the home.
	//
	// Ahead of the first-apply MCP branch below on purpose: that branch runs a VISIBLE apply,
	// and a visible apply would still put these questions — including an install offer for a
	// program this launch is not — between the user and the program they asked for.
	if decisions := survey.PendingDecisions(); len(decisions) > 0 && !surveyOnlyNeedsLossPrompt(survey) {
		reportHostApplyGateDecisions(errw, bin, decisions)
		reportGateMisses(errw, survey, bin)
		return true
	}

	canPrompt := hostGateCanPrompt()

	// ONE-WAY DOOR EXCEPTION (confirmHostLosses):
	// On a FIRST apply into an unmanaged home where undeclared servers exist,
	// preserve the legacy adoption prompt if promptable, or refuse if non-interactive.
	if surveyNeedsPrompt(survey) {
		if canPrompt {
			return hostApplyGateApplyInteractive(errw, stdin, bin)
		}
		fmt.Fprintf(errw, "yolo host: refusing to launch %s — first apply into this home would "+
			"lose undeclared MCP servers and there is no terminal to confirm adoption.\n"+
			"  Run `yolo host apply --assert` to review and adopt configuration interactively.\n",
			bin)
		return false
	}

	// ZERO-PROMPT AUTO-APPLY (docs/reference/host-apply-staleness.md OQ-2).
	// For all routine synchronizations under assert and own, apply changes automatically
	// without prompting, emit a single stderr notice, and launch immediately. The user's stdin
	// is NOT handed down: nothing above left a question for this apply to ask.
	return hostApplyGateApply(errw, bin, home)
}

// refuseLaunchOverFailures is the refusal for a failure in the launched program's own
// configuration: each failure once, with its fix, and nothing launched.
func refuseLaunchOverFailures(errw io.Writer, home, bin string, failures []hostFailure) {
	fmt.Fprintf(errw, "yolo host: refusing to launch %s — some of its configuration cannot be "+
		"written:\n", bin)
	reportLaunchFailures(errw, home, failures)
	fmt.Fprintf(errw, "  Fix it, then launch again.\n")
}

// surveyOnlyNeedsLossPrompt reports whether the one pending decision is the first-apply MCP
// loss — the question this hook already surfaces itself (surveyNeedsPrompt), visibly on a TTY
// and as a refusal off one.
func surveyOnlyNeedsLossPrompt(survey *hostApplySurvey) bool {
	return surveyNeedsPrompt(survey) && len(survey.PendingDecisions()) == 1
}

// reportHostApplyGateIncompleteSet is the hook's refusal to render an incomplete pack set: every
// unresolvable pack with the resolver's reason, and the remedy.
func reportHostApplyGateIncompleteSet(errw io.Writer, bin string, unresolved []unresolvedPack) {
	fmt.Fprintf(errw, "yolo host: did not render your host configuration — %d configured %s "+
		"could not be resolved, and an incomplete pack set is never applied:\n",
		len(unresolved), plural(len(unresolved), "pack", "packs"))
	for _, u := range unresolved {
		fmt.Fprintf(errw, "  ✗ %s: %s\n", u.Name, u.Reason)
	}
	for _, g := range unresolvedPackGroups(unresolved) {
		fmt.Fprintf(errw, "  → %s\n", g.Remedy)
	}
	fmt.Fprintf(errw, "  Refusing to launch %s: a launch never runs on part of the pack set your "+
		"config asks for, at any notch.\n", bin)
}

// skewOnly reports whether every record in list is a pack that resolved but holds contributions
// this yolo cannot read (unresolvedPack.Skipped), and none is a pack that did not resolve.
func skewOnly(list []unresolvedPack) bool {
	for _, u := range list {
		if len(u.Skipped) == 0 {
			return false
		}
	}
	return len(list) > 0
}

// reportHostApplyGateSkewedSet is the hook's line for a pack set it does not render because a
// pack holds contributions this yolo cannot read: the packs, the remedy, and that the program
// launches on the last apply. The skips themselves are the composition's lines (selectionLines),
// printed once below this, so they are not repeated here.
func reportHostApplyGateSkewedSet(errw io.Writer, bin string, skewed []unresolvedPack) {
	names := make([]string, len(skewed))
	for i, u := range skewed {
		names[i] = u.Name
	}
	fmt.Fprintf(errw, "yolo host: did not render your host configuration — %s %s contributions "+
		"this yolo cannot read, and a real home is never rendered around them:\n",
		strings.Join(names, ", "), plural(len(skewed), "holds", "hold"))
	for _, g := range unresolvedPackGroups(skewed) {
		fmt.Fprintf(errw, "  → %s\n", g.Remedy)
	}
	fmt.Fprintf(errw, "  Launching %s against the configuration your last apply left in place.\n", bin)
}

// reportHostApplyGateDecisions is the hook's refusal to run an apply that would ask something:
// the questions, and the one command that asks them where the user can answer.
func reportHostApplyGateDecisions(errw io.Writer, bin string, decisions []string) {
	fmt.Fprintf(errw, "yolo host: did not render your host configuration — applying it needs "+
		"a decision this launch cannot ask you:\n")
	for _, d := range decisions {
		fmt.Fprintf(errw, "  • %s\n", d)
	}
	fmt.Fprintf(errw, "  → yolo host apply --assert   (shows each question and asks it), then "+
		"launch again.\n")
	fmt.Fprintf(errw, "  Launching %s against the configuration your last apply left in place.\n", bin)
}

// reportGateMisses prints the miss line (host-launch-environment.md §4.2, HE-D2) for every declared
// dependency the observe pass found missing on this launch's PATH: the program, the packs that
// need it, the whole PATH searched and the `host_path` fix. Once per missing program: the one this
// launch starts is left to the exec's own lookup, which prints the same line moments later if it
// misses too — and does not, when a floor copy or a given path runs instead.
func reportGateMisses(errw io.Writer, survey *hostApplySurvey, bin string) {
	// A program with no build for this host is looked up on the same PATH (MissLine), so its miss
	// is reported beside the missing ones.
	for _, dep := range append(survey.MissingDeps(), survey.UnpublishedDeps()...) {
		if dep == bin {
			continue
		}
		if line := survey.MissLine(dep, true); line != "" {
			fmt.Fprintf(errw, "yolo host: %s\n", line)
		}
	}
}

// noPromptStdin is the stdin the hook's buffered apply reads. The hook only runs an apply the
// observe pass found no question in, so a read here means the two disagreed: it answers NO
// (promptYesNo's EOF contract, so nothing is taken over) and records that it was asked.
type noPromptStdin struct{ asked bool }

func (r *noPromptStdin) Read([]byte) (int, error) {
	r.asked = true
	return 0, io.EOF
}

// surveyNeedsPrompt reports whether the apply would hit confirmHostLosses: a first-ever
// apply into an unmanaged home with pre-existing undeclared MCP entries.
func surveyNeedsPrompt(survey *hostApplySurvey) bool {
	if survey == nil {
		return false
	}
	entries, _ := survey.DroppedEntries()
	return survey.FirstApply() && entries > 0
}

func hostApplyGateApplyInteractive(errw io.Writer, stdin io.Reader, bin string) bool {
	wrote := &hostApplySurvey{}
	if rc := applyHostSurveyed(errw, errw, false, true, stdin, wrote); rc != 0 {
		// [OQ-HS17] here too: the report above already stated each failure with its fix, so an
		// unrelated one only needs the launch to say it is going ahead.
		if _, unrelated, blocking := splitLaunchFailures(wrote, bin); !blocking && len(unrelated) > 0 {
			fmt.Fprintf(errw, "yolo host: %s reads none of what was not written — launching %s.\n",
				bin, bin)
			return true
		}
		fmt.Fprintf(errw, "yolo host: the host apply did not complete (rc=%d, see above) — %s "+
			"was not launched.\n"+
			"  Fix what it reported and run `yolo host apply --assert`, then launch again.\n",
			rc, bin)
		return false
	}
	return true
}

// hostApplyGateApply is the unattended auto-apply. Its report is buffered — shown only if it
// fails — and it reads no terminal: noPromptStdin stands in, because a question asked into a
// buffer is a question nobody can see.
//
// "synchronized" lists what THIS apply's own survey says it changed, never the observe pass's
// prediction: the two differ exactly when something was declined, and a notice naming work
// that did not happen — on every launch, since the home never converges — is the measured
// defect.
func hostApplyGateApply(errw io.Writer, bin, home string) bool {
	var buf bytes.Buffer
	stdin := &noPromptStdin{}
	wrote := &hostApplySurvey{}
	rc := hostApplyGateWrite(&buf, &buf, false, true, stdin, wrote)
	if stdin.asked {
		// The pre-check missed a question: a bug, and reported as one rather than hidden.
		io.Copy(errw, &buf)
		fmt.Fprintf(errw, "yolo host: the host apply asked a question this launch could not "+
			"show you (answered no) — run `yolo host apply --assert` to answer it. Launching %s.\n",
			bin)
		return true
	}
	if rc != 0 {
		// A FAILURE THIS PROGRAM DOES NOT READ DOES NOT STOP IT ([OQ-HS17]). The apply did the
		// rest of its work; what it could not write is another program's, so it is reported with
		// its fix and the launch proceeds. Anything the gate cannot attribute to a pack, or that
		// is in this program's own configuration, still refuses (OQ-HS14).
		related, unrelated, blocking := splitLaunchFailures(wrote, bin)
		if !blocking {
			reportHostApplyGateSynchronized(errw, home, wrote)
			reportUnrelatedLaunchFailures(errw, home, bin, unrelated)
			return true
		}
		if len(related) > 0 && !wrote.unattributedFailure() {
			refuseLaunchOverFailures(errw, home, bin, related)
			return false
		}
		io.Copy(errw, &buf)
		fmt.Fprintf(errw, "yolo host: the host apply did not complete (rc=%d, see above) — %s "+
			"was not launched.\n"+
			"  Fix what it reported and run `yolo host apply --assert`, then launch again.\n",
			rc, bin)
		return false
	}
	reportHostApplyGateSynchronized(errw, home, wrote)
	return true
}

// hostApplyGateWrite is the gate's writing apply, behind a seam for hostApplyGateSurvey's
// reason: a render ERROR (as opposed to a broken link, which a test can build) cannot be
// provoked in a home a root-run test controls, so the relatedness rule's error half is driven
// through a substitute. Production never reassigns it.
var hostApplyGateWrite = applyHostSurveyed

func prettyHomePath(home, abs string) string {
	if rel, ok := strings.CutPrefix(abs, home+string(filepath.Separator)); ok {
		return "~/" + filepath.ToSlash(rel)
	}
	return abs
}

// shellHomePath is prettyHomePath for a path a remedy hands to a shell: what follows `~/` is one
// quoted word, so the tilde still expands and a space in the rest (~/Library/Application Support)
// stays inside the one argument.
func shellHomePath(home, abs string) string {
	if rel, ok := strings.CutPrefix(abs, home+string(filepath.Separator)); ok {
		return "~/" + shquote.Quote(filepath.ToSlash(rel))
	}
	return shquote.Quote(abs)
}

func reportHostApplyGateSynchronized(errw io.Writer, home string, survey *hostApplySurvey) {
	var targets []string
	seen := make(map[string]bool)
	for _, c := range survey.Changed {
		p := prettyHomePath(home, c.Path)
		if !seen[p] {
			seen[p] = true
			targets = append(targets, p)
		}
	}
	if len(targets) == 0 {
		fmt.Fprintf(errw, "yolo host: synchronized host configuration\n")
		return
	}
	fmt.Fprintf(errw, "yolo host: synchronized host configuration (%s)\n", strings.Join(targets, ", "))
}

// surveyHostApplyWithinBudget runs the observe pass under §4.4's budget and returns the
// roll-up, or nil plus the one-phrase reason it has no answer.
//
// IT RUNS THE APPLY, in observe posture, with the report captured and thrown away. Growing a
// separate traversal of the four written kinds here would be a second model of what an apply
// does, free to drift out of step with the apply it describes — the shape AGENTS.md records as
// having shipped five times.
//
// A NON-ZERO rc IS CANNOT-DETERMINE, not a change: an observe pass exits non-zero for an inert
// pack, an unresolvable config-overlay, a doubly-owned config surface. The one exception is a
// survey naming an unresolvable PACK, which is returned whatever the rc so the hook can name it. Every one of those is a
// pack-authoring problem `yolo check` owns, and none of them is something a launch may stop
// over.
//
// The goroutine is abandoned on expiry rather than cancelled, because the pass is synchronous
// filesystem work with no cancellation point. It writes only to its own buffer and only in
// observe posture, so an abandoned one cannot touch the home; the exec that follows ends it.
func surveyHostApplyWithinBudget() (*hostApplySurvey, string) {
	type outcome struct {
		survey *hostApplySurvey
		rc     int
	}
	done := make(chan outcome, 1)
	go func() {
		var sink bytes.Buffer
		survey := &hostApplySurvey{}
		rc := hostApplyGateSurvey(&sink, &sink, false, false, nil, survey)
		done <- outcome{survey, rc}
	}()
	select {
	case got := <-done:
		if got.rc != 0 && len(got.survey.UnresolvedPacks()) == 0 {
			return nil, fmt.Sprintf("`yolo host apply` reports a problem of its own (rc=%d); "+
				"run `yolo check`", got.rc)
		}
		// An unresolvable pack is returned even under a non-zero rc: it is the one finding the
		// caller reports by name rather than as cannot-determine.
		return got.survey, ""
	case <-time.After(hostApplyGateBudget):
		return nil, fmt.Sprintf("the check did not finish within %s", hostApplyGateBudget)
	}
}
