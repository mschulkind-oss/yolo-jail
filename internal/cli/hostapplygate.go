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
//   - It does not touch `yolo run` or `yolo host apply`. Both take
//     `--accept-config-changes`; honoring the environment variable there would buy nothing and
//     would let one shell-rc line pre-approve every jail launch on the machine (§1 P4).

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// acceptConfigChangesEnv grants the config-change approval for ONE wrapped host launch.
//
// AN ENVIRONMENT VARIABLE, on this path and this path only, and it is not a contradiction of
// `config.AcceptConfigChangesFlag`'s ruling that a per-launch approval must be a flag — it is
// the answer to a different question (OQ-HS10). For `yolo run` the choice is flag-vs-env-var
// and there the variable is pure cost. Here the wrapper body is fixed —
// `exec yolo host -- claude "$@"` (internal/hostwrap.Body) — and hostMain hands everything
// after `--` to the program, so a user typing `claude --print foo` has NO SLOT for a
// yolo-level flag. The choice is env-var-vs-nothing, and "nothing" means a scripted agent
// launch can never proceed.
//
// The cost is real and accepted knowingly (maintainer ruling 2026-09-03): exported in a shell
// profile, this becomes de facto standing consent for every wrapped launch in that shell. Two
// containments make that tolerable rather than a hole, and both are load-bearing:
//
//  1. IT IS HONORED HERE AND NOWHERE ELSE. Not by `yolo run`, not by `yolo host apply` — see
//     the file header.
//  2. THE WRAPPER MUST NOT BAKE IT IN. A generator that wrote the grant into the wrapper body
//     when a config key said so is the obvious next step and is REFUSED: it converts a
//     per-shell act into a permanent one, which is the standing consent §1's retraction
//     forbids. The variable is tolerable *because* someone has to type it.
//
// Named to match the flag it stands in for, so the two read as one grant in two spellings and
// a refusal can offer whichever channel its reader can reach. The constant lives beside the
// refusal that names it, following snapshot.go's rule for exactly this: the spelling a user is
// told to set and the spelling the code reads cannot drift apart.
const acceptConfigChangesEnv = "YOLO_ACCEPT_CONFIG_CHANGES"

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
	//
	// THE PROGRAM STILL LAUNCHES, which is this hook's contract for a problem found by the
	// observe pass (a pack-authoring or pack-set fault is `yolo check`'s to report and never a
	// reason to stop a launch, §4.4). Launching is not a half state: nothing is written, so the
	// home holds exactly what the last apply that ran left in it — a consistent render of an
	// older pack set, not a partial render of this one.
	if survey != nil && len(survey.UnresolvedPacks()) > 0 {
		reportHostApplyGateIncompleteSet(errw, bin, survey.UnresolvedPacks())
		return true
	}
	if survey == nil {
		// CANNOT DETERMINE (§4.4): a malformed pack manifest, an unreadable home, an
		// unresolvable file:// pack, a budget overrun. The predicate has no answer, so there is
		// no change to refuse over — exec, with at most one line. Per internal/version's
		// srcskew house rule, a gate that cannot prove its condition does not fire.
		fmt.Fprintf(errw, "yolo host: could not check whether your host render is up to date "+
			"(%s) — launching %s anyway.\n", why, bin)
		return true
	}
	if !survey.Changes() {
		// The common case, and the one R3 is about: silence. A freshly-applied home must
		// prompt not at all, ever, until something actually changes.
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
	if rc := applyHost(errw, errw, false, true, stdin); rc != 0 {
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
	rc := applyHostSurveyed(&buf, &buf, false, true, stdin, wrote)
	if stdin.asked {
		// The pre-check missed a question: a bug, and reported as one rather than hidden.
		io.Copy(errw, &buf)
		fmt.Fprintf(errw, "yolo host: the host apply asked a question this launch could not "+
			"show you (answered no) — run `yolo host apply --assert` to answer it. Launching %s.\n",
			bin)
		return true
	}
	if rc != 0 {
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

func prettyHomePath(home, abs string) string {
	if rel, ok := strings.CutPrefix(abs, home+string(filepath.Separator)); ok {
		return "~/" + filepath.ToSlash(rel)
	}
	return abs
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
