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
// per-contribution prints fails the count in both.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// TestHostApplyNamesEachInapplicableKindOnce is the kinds half. Every kind the shipped pack set
// declares and this notch does nothing with is named, and named once.
func TestHostApplyNamesEachInapplicableKindOnce(t *testing.T) {
	shippedPacksFixture(t)
	_, report := surveyApply(t)

	kinds, contributions := inapplicableKindsInConfig(t)
	if len(kinds) == 0 {
		t.Fatalf("fixture bug: the shipped packs declare no kind this notch skips, so there "+
			"is nothing for the tiers to collapse\n%s", report)
	}
	// The collapse is only worth asserting while there is something to collapse: the measured
	// home had 19 contributions across 7 kinds. A set where the two numbers agree would make
	// "exactly once" free.
	if contributions <= len(kinds) {
		t.Fatalf("fixture bug: %d contributions across %d kinds — no kind is declared twice, "+
			"so the once-per-run assertion below cannot fail\n%s", contributions, len(kinds), report)
	}

	lines := notchKindLines(report)
	if len(lines) != 1 {
		t.Fatalf("the kinds that do not apply are a property of the NOTCH, so they are named "+
			"once per run; got %d lines:\n%s", len(lines), report)
	}
	for _, k := range kinds {
		if n := strings.Count(lines[0], string(k)); n != 1 {
			t.Errorf("kind %q appears %d times on the notch line, want exactly 1 — the "+
				"census is satisfied by naming, and naming twice is repetition: %q",
				k, n, lines[0])
		}
	}
	// the report vocabulary: `refused` belongs to an apply that STOPPED, and a kind with no
	// meaning off-container stopped nothing.
	if strings.Contains(lines[0], "refused") {
		t.Errorf("a notch fact says `does not apply`, never `refused` (the report vocabulary): %q", lines[0])
	}
	// P8: the ~40-word reasons stay in internal/render and reach no terminal view. One of them,
	// picked because its wording is the most distinctive of the seven.
	if strings.Contains(report, "off-container the home simply") {
		t.Errorf("the kind rationale must not print — it is the manual's (the report vocabulary):\n%s", report)
	}
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

// inapplicableKindsInConfig reports which kinds the configured packs declare that this notch
// does nothing with, and how many CONTRIBUTIONS declare them. The second number is what makes
// the once-per-run assertion non-vacuous.
func inapplicableKindsInConfig(t *testing.T) (kinds []packdecl.Kind, contributions int) {
	t.Helper()
	fields := render.HostFields()
	seen := map[packdecl.Kind]bool{}
	for _, p := range loadedPacksForTest(t) {
		for _, c := range p.Decl.Contributions() {
			if !notchInapplicable(fields, c.Kind) {
				continue
			}
			contributions++
			if !seen[c.Kind] {
				seen[c.Kind] = true
				kinds = append(kinds, c.Kind)
			}
		}
	}
	return kinds, contributions
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
