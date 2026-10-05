package cli

// hostapplynotch_test.go pins docs/reference/report-tiers.md the tiers: a tier-1 fact is named
// EXACTLY ONCE per run, however many contributions declare it.
//
// The assertion has to be "exactly once", not "at least once", and the difference is the whole
// step: the old report named the same seven kinds nineteen times and the same autonomy posture
// five times, and every one of those lines passed a Contains check. So each test below also
// measures the MULTIPLICITY IT IS COLLAPSING from the same pack set — if the shipped packs ever
// stop declaring a kind twice, the test says the fixture went vacuous instead of passing for
// free.
//
// Both go through applyHostSurveyed, which is the call site: deleting printNotchFacts from
// apply.go leaves the kinds unnamed and fails the first test, and restoring the
// per-contribution prints fails the count in both. The at-launch tests go through it too, and
// read the doorway set apply.go hands the survey.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// TestHostApplyNamesEachKindOncePerGroup is the kinds half. Every kind the shipped pack set
// declares that this notch does not render is named on the one notch line, and named once in
// each group it lands in: AT LAUNCH ONLY for what `yolo host --` delivers, DOES NOT APPLY for
// what no host verb does anything with. A kind can be in both groups (a credential loophole's
// doorway, and the claude pack's interception loophole whose only client is a container), and
// once in each is still once per group.
func TestHostApplyNamesEachKindOncePerGroup(t *testing.T) {
	shippedPacksFixture(t)
	_, report := surveyApply(t)

	groups, contributions := notchGroupsInConfig(t)
	for _, outcome := range []hostNotchOutcome{notchAtLaunch, notchDoesNotApply} {
		if len(groups[outcome]) == 0 {
			t.Fatalf("fixture bug: the shipped packs declare no kind in group %d, so there is "+
				"nothing for the tiers to collapse\n%s", outcome, report)
		}
	}
	// The collapse is only worth asserting while there is something to collapse: the measured
	// home had 19 contributions across 7 kinds. A set where the two numbers agree would make
	// "exactly once" free.
	kinds := len(groups[notchAtLaunch]) + len(groups[notchDoesNotApply])
	if contributions <= kinds {
		t.Fatalf("fixture bug: %d contributions across %d kinds — no kind is declared twice, "+
			"so the once-per-run assertion below cannot fail\n%s", contributions, kinds, report)
	}

	lines := notchKindLines(report)
	if len(lines) != 1 {
		t.Fatalf("the kinds that do not apply are a property of the NOTCH, so they are named "+
			"once per run; got %d lines:\n%s", len(lines), report)
	}
	clauses := notchClauses(lines[0])
	for outcome, prefix := range map[hostNotchOutcome]string{
		notchAtLaunch: atLaunchClause, notchDoesNotApply: doesNotApplyClause,
	} {
		clause, ok := clauses[prefix]
		if !ok {
			t.Fatalf("the notch line has no %q clause: %q", prefix, lines[0])
		}
		for _, k := range groups[outcome] {
			if n := countWord(clause, string(k)); n != 1 {
				t.Errorf("kind %q appears %d times in the %q clause, want exactly 1 — the "+
					"census is satisfied by naming, and naming twice is repetition: %q",
					k, n, prefix, clause)
			}
		}
	}
	// the report vocabulary: `refused` belongs to an apply that STOPPED, and a kind with no
	// meaning off-container stopped nothing.
	if strings.Contains(lines[0], "refused") {
		t.Errorf("a notch fact says `does not apply`, never `refused` (the report vocabulary): %q", lines[0])
	}
	// P8: the ~40-word reasons stay in internal/render and reach no terminal view. One of them,
	// picked because its wording is the most distinctive of the seven, and one at-launch one.
	for _, prose := range []string{"off-container the home simply", "editing your shell rc"} {
		if strings.Contains(report, prose) {
			t.Errorf("the kind rationale must not print — it is the manual's (the report "+
				"vocabulary): %q\n%s", prose, report)
		}
	}
}

// THE AT-LAUNCH OUTCOME, the regression this notch line had: `yolo host apply` named env,
// adapter, service and loophole as not applying at the host while `yolo host -- <program>`
// delivers each (MEASURED 2026-10-04: the dry run said so while `yolo host -- env` printed the
// pack env). Over shipped packs that declare all five at-launch kinds: pi's plain env, the
// guardrails blockers, wire-bridge's adapters and service (an official host half), and aws-auth's
// credential doorway, with audio beside them for the shape the host delivers nowhere.
func TestHostApplyNamesWhatYoloHostDeliversAtLaunch(t *testing.T) {
	home := t.TempDir()
	selectPacks(t, home, `"pi","guardrails","wire-bridge","aws-auth","audio"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	defaultReport(t)
	_, report := surveyApply(t)

	lines := notchKindLines(report)
	if len(lines) != 1 {
		t.Fatalf("want one notch line, got %d:\n%s", len(lines), report)
	}
	clauses := notchClauses(lines[0])
	launched, notApplying := clauses[atLaunchClause], clauses[doesNotApplyClause]
	for _, k := range []packdecl.Kind{packdecl.KindEnv, packdecl.KindBlockedTool,
		packdecl.KindAdapter, packdecl.KindService, packdecl.KindLoophole} {
		if countWord(launched, string(k)) != 1 {
			t.Errorf("%s is delivered by `yolo host -- <program>` and is not named once as at "+
				"launch only: %q", k, lines[0])
		}
	}
	// Two kinds whose every contribution here is delivered: neither may be said not to apply.
	for _, k := range []packdecl.Kind{packdecl.KindAdapter, packdecl.KindBlockedTool, packdecl.KindService} {
		if countWord(notApplying, string(k)) != 0 {
			t.Errorf("%s is said not to apply at the host, and `yolo host --` delivers it: %q", k, lines[0])
		}
	}
	// audio's pointer at a socket only a jail binds is withheld at `yolo host --` (LP-D1), and
	// its loophole has no doorway, so env and loophole are in both groups: the line and the
	// launch decide with the launch's own predicates.
	for _, k := range []packdecl.Kind{packdecl.KindEnv, packdecl.KindLoophole} {
		if countWord(notApplying, string(k)) != 1 {
			t.Errorf("audio's %s has no meaning at the host (the launch withholds it), and the "+
				"line does not say so: %q", k, lines[0])
		}
	}

	// --verbose says which pack's contributions landed in each group, for a kind in both.
	verboseReport(t)
	_, verbose := surveyApply(t)
	for _, want := range []string{"env (aws-auth, pi)", "env (audio)", "loophole (aws-auth, openai-auth)",
		"loophole (audio)", "`yolo config-ref` says how"} {
		if !strings.Contains(verbose, want) {
			t.Errorf("--verbose does not say %q:\n%s", want, verbose)
		}
	}
}

// A loophole whose only client is a container is named as not applying, and never as delivered
// at launch. host-processes and journal ship no doorway, and no selected pack ships one, so the
// doorway set apply.go hands the survey is what decides it: handing it every loophole would
// name these as delivered, as handing it none would fail the test above.
func TestHostApplyNamesAContainerOnlyLoopholeAsNotApplying(t *testing.T) {
	home := t.TempDir()
	selectPacks(t, home, `"host-processes","journal"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	defaultReport(t)
	_, report := surveyApply(t)

	lines := notchKindLines(report)
	if len(lines) != 1 {
		t.Fatalf("want one notch line, got %d:\n%s", len(lines), report)
	}
	clauses := notchClauses(lines[0])
	if _, ok := clauses[atLaunchClause]; ok {
		t.Errorf("neither pack declares anything `yolo host --` delivers: %q", lines[0])
	}
	if countWord(clauses[doesNotApplyClause], "loophole") != 1 {
		t.Errorf("a loophole whose only client is a container is not named as not applying: %q", lines[0])
	}
}

// atLaunchClause and doesNotApplyClause open the default notch line's two kind clauses.
const (
	atLaunchClause     = "at launch only (`yolo host --`): "
	doesNotApplyClause = "does not apply at the host: "
)

// notchClauses splits the default notch line into its clauses, keyed by the clause's opening
// words (atLaunchClause, doesNotApplyClause) and holding the names after them.
func notchClauses(line string) map[string]string {
	out := map[string]string{}
	line = strings.TrimSpace(line)
	for _, part := range strings.Split(line, " · ") {
		for _, prefix := range []string{atLaunchClause, doesNotApplyClause} {
			if rest, ok := strings.CutPrefix(part, prefix); ok {
				out[prefix] = rest
			}
		}
	}
	return out
}

// TestHostApplyNamesTheAutonomyPostureOnce is the posture half. Its value comes from the
// notch's own render target and cannot differ between packs in one run, so five packs
// declaring `autonomy` are one fact.
func TestHostApplyNamesTheAutonomyPostureOnce(t *testing.T) {
	shippedPacksFixture(t)
	_, report := surveyApply(t)

	declaring := 0
	for _, p := range loadedPacksForTest(t) {
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindAutonomy {
				declaring++
				break
			}
		}
	}
	if declaring < 2 {
		t.Fatalf("fixture bug: %d pack(s) declare autonomy, so once-per-run is free\n%s",
			declaring, report)
	}
	if n := strings.Count(report, "guarded posture"); n != 1 {
		t.Errorf("%d packs declare `autonomy` and the posture is the NOTCH's, so it is stated "+
			"once; got %d lines:\n%s", declaring, n, report)
	}
}

// notchKindLines returns the report lines stating which kinds do not apply. Matched on the
// vocabulary the report vocabulary fixes rather than on the whole sentence, so re-wording the
// line does not silently turn this test into a tautology over zero lines.
func notchKindLines(report string) []string {
	var out []string
	for _, l := range strings.Split(report, "\n") {
		if strings.Contains(l, "do not apply at the host notch") ||
			strings.Contains(l, "does not apply at the host notch") ||
			strings.Contains(l, "does not apply at the host:") {
			out = append(out, l)
		}
	}
	return out
}

// loadedPacksForTest resolves the packs the fixture's config selects, the way the apply does.
func loadedPacksForTest(t *testing.T) []*packload.Pack {
	t.Helper()
	entries, err := config.LoadPacks(nil)
	if err != nil {
		t.Fatalf("load the fixture's packs: %v", err)
	}
	var loaded []*packload.Pack
	for _, e := range entries {
		if p := packForCheckDeps(e); p != nil {
			loaded = append(loaded, p)
		}
	}
	loaded, _ = packload.ResolveDestinations(loaded)
	return loaded
}

// selectedPacksForTest is loadedPacksForTest through the launch's own selection, as the apply
// takes it, so the packs a `needs` joins are in the set: the doorways a survey reads are theirs
// (openai-auth's, which every agent pack but copilot joins).
func selectedPacksForTest(t *testing.T) []*packload.Pack {
	t.Helper()
	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		t.Fatalf("load the fixture's packs: %v", sel.loadErr)
	}
	if problems := sel.problems(); len(problems) > 0 {
		t.Fatalf("the fixture's packs do not resolve: %+v", problems)
	}
	loaded, _ := packload.ResolveDestinations(sel.packs)
	return loaded
}

// notchGroupsInConfig reports which kinds the configured packs declare in each notch-line group,
// and how many CONTRIBUTIONS landed in either group. The second number is what makes the
// once-per-group assertion non-vacuous.
func notchGroupsInConfig(t *testing.T) (map[hostNotchOutcome][]packdecl.Kind, int) {
	t.Helper()
	fields := render.HostFields()
	loaded := selectedPacksForTest(t)
	doorways := run.HostDoorwayLoopholes(config.UserScopeConfigOrEmpty(), loaded)
	groups := map[hostNotchOutcome][]packdecl.Kind{}
	seen := map[hostNotchOutcome]map[packdecl.Kind]bool{notchAtLaunch: {}, notchDoesNotApply: {}}
	contributions := 0
	for _, p := range loaded {
		for _, c := range p.Decl.Contributions() {
			outcome := hostNotchOutcomeOf(loaded, fields, c, doorways)
			if outcome == notchApplies {
				continue
			}
			contributions++
			if !seen[outcome][c.Kind] {
				seen[outcome][c.Kind] = true
				groups[outcome] = append(groups[outcome], c.Kind)
			}
		}
	}
	return groups, contributions
}

// THE AUTONOMY LINE MUST NOT PROMISE A FOLD THAT DOES NOT HAPPEN.
//
// The line ends "folded into the config surfaces below", and `f.Autonomy` was "any pack
// declares the kind" — the same answer for a pack whose GUARDED posture patches a settings key
// and for one that has no guarded posture at all. copilot is the second kind and became so on
// the day `--yolo` moved under `kind: "autonomy"`: its autonomy is a launch flag, a flag has no
// persistence, and not selecting it IS the tightening, so there is no guarded posture and
// nothing to fold. Before that move copilot declared no autonomy kind, so a copilot-only apply
// printed no line at all — the false promise arrived with the fix.
//
// The posture is still STATED, because "did my jail-bypass keys reach my real home?" is the
// question this command exists to answer and "no, and there were none" is an answer. What
// changes is the second clause.
func TestHostApplyAutonomyLineDoesNotPromiseAnAbsentFold(t *testing.T) {
	home := t.TempDir()
	t.Setenv("YOLO_VERBOSE", "1") // where the fold goes is the --verbose line's (printNotchFacts)
	selectPacks(t, home, `"copilot"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	_, report := surveyApply(t)

	// Fixture guard: the whole test is vacuous if copilot ever grows a guarded posture.
	for _, p := range loadedPacksForTest(t) {
		if p.Name != "copilot" {
			continue
		}
		if posture := p.Decl.PostureFor(render.ProfileFor(render.KindHost).AgentAutonomy); posture != nil {
			t.Fatalf("fixture bug: copilot now HAS a guarded posture, so this notch does fold " +
				"something and the test below asserts the wrong half")
		}
	}

	if !strings.Contains(report, "guarded posture") {
		t.Errorf("the posture is still the answer to the question this command exists for, so "+
			"the line must print:\n%s", report)
	}
	if strings.Contains(report, "folded into the config surfaces below") {
		t.Errorf("no pack's guarded posture patches a surface in this run, so the report points "+
			"at surfaces that carry no patch:\n%s", report)
	}
}

// The other direction, so the clause is not simply deleted: a pack set whose guarded posture
// DOES patch a surface still says where the patch landed.
func TestHostApplyAutonomyLineNamesTheFoldWhenThereIsOne(t *testing.T) {
	shippedPacksFixture(t)
	t.Setenv("YOLO_VERBOSE", "1") // where the fold goes is the --verbose line's (printNotchFacts)
	_, report := surveyApply(t)

	if !strings.Contains(report, "folded into the config surfaces below") {
		t.Errorf("claude, codex, agy, opencode and pi all patch a settings key in their guarded "+
			"posture, so the fold is real and the line must say where it went:\n%s", report)
	}
}

// THE DEFAULT VIEW'S TIER-1 FACTS ARE ONE LINE — the kinds, inert `packages:`, the posture — and
// the rationale pointer and the fold clause are --verbose's. The maintainer's report opened with
// three lines of this above every apply.
func TestHostApplyTierOneFactsAreOneLineByDefault(t *testing.T) {
	shippedPacksFixture(t)
	_, report := surveyApply(t)
	lines := notchKindLines(report)
	if len(lines) != 1 || !strings.Contains(lines[0], "guarded posture") {
		t.Fatalf("want one line naming the kinds and the posture, got %q:\n%s", lines, report)
	}
	if n := strings.Count(report, "guarded posture"); n != 1 {
		t.Errorf("the posture is stated %d times:\n%s", n, report)
	}
	for _, verboseOnly := range []string{"`yolo config-ref` says why", "folded into the config surfaces below",
		"inert at this notch"} {
		if strings.Contains(report, verboseOnly) {
			t.Errorf("the default view prints %q, which is --verbose's:\n%s", verboseOnly, report)
		}
	}
	t.Setenv("YOLO_VERBOSE", "1")
	_, verbose := surveyApply(t)
	for _, want := range []string{"`yolo config-ref` says why", "folded into the config surfaces below"} {
		if !strings.Contains(verbose, want) {
			t.Errorf("--verbose lost %q:\n%s", want, verbose)
		}
	}
}
