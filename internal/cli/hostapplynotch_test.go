package cli

// hostapplynotch_test.go pins docs/design/report-tiers.md §9 step 3: a tier-1 fact is named
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
// apply.go leaves the kinds unnamed and fails the first test, and restoring the per-contribution
// prints fails the count in both.

import (
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
			"is nothing for §9 step 3 to collapse\n%s", report)
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
	// §4.6's closed vocabulary: `refused` belongs to an apply that STOPPED, and a kind with no
	// meaning off-container stopped nothing.
	if strings.Contains(lines[0], "refused") {
		t.Errorf("a notch fact says `does not apply`, never `refused` (§4.6): %q", lines[0])
	}
	// P8: the ~40-word reasons stay in internal/render and reach no terminal view. One of them,
	// picked because its wording is the most distinctive of the seven.
	if strings.Contains(report, "off-container the home simply") {
		t.Errorf("the kind rationale must not print — it is the manual's (§4.6):\n%s", report)
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
// vocabulary §4.6 fixes rather than on the whole sentence, so re-wording the line does not
// silently turn this test into a tautology over zero lines.
func notchKindLines(report string) []string {
	var out []string
	for _, l := range strings.Split(report, "\n") {
		if strings.Contains(l, "do not apply at the host notch") ||
			strings.Contains(l, "does not apply at the host notch") {
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
