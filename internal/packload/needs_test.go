package packload

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// needsPack builds a hand-made pack that DECLARES needs and installs no bins;
// binPack is its mirror. Everything ResolveNeeds consumes is on the declaration,
// which is why a nil Root suffices — and that is the purity claim, tested rather
// than trusted. No validation runs on a hand-built manifest, so a program
// contribution needs no `via` here.
func needsPack(name string, needs ...packdecl.PackNeed) *Pack {
	return &Pack{Name: name, Decl: &packdecl.Manifest{Needs: needs}}
}

func binPack(name string, bins ...string) *Pack {
	var contributes []packdecl.Contribution
	for _, bin := range bins {
		contributes = append(contributes, packdecl.Contribution{
			Kind: packdecl.KindProgram, Bin: bin,
		})
	}
	return &Pack{Name: name, Decl: &packdecl.Manifest{Contributes: contributes}}
}

func need(pack string, whenBins ...string) packdecl.PackNeed {
	return packdecl.PackNeed{Pack: pack, WhenBins: whenBins}
}

// needsUniverse turns hand-made packs into the embedded-set predicate. A name
// absent from the returned map is a name outside the universe — the WB-D9
// refusal's trigger.
func needsUniverse(packs ...*Pack) func(string) (*Pack, bool) {
	byName := map[string]*Pack{}
	for _, p := range packs {
		byName[p.Name] = p
	}
	return func(name string) (*Pack, bool) {
		p, ok := byName[name]
		return p, ok
	}
}

// addedNames renders what the closure added, for a one-line comparison.
func addedNames(added []*Pack) string {
	var names []string
	for _, p := range added {
		names = append(names, p.Name)
	}
	return strings.Join(names, ",")
}

// TestResolveNeedsTransitiveClosure: a selected pack's live need adds its target,
// and the ADDED pack's own needs are then honored too — B joins because A needs
// it, C joins because B needs it, in that order, with one cause line per
// addition naming the needing pack and the bin that fired the condition.
func TestResolveNeedsTransitiveClosure(t *testing.T) {
	selected := []*Pack{
		binPack("trigger", "x"),
		needsPack("a", need("b", "x")),
	}
	added, causes, err := ResolveNeeds(selected, needsUniverse(
		needsPack("b", need("c")),
		binPack("c", "y"),
	))
	if err != nil {
		t.Fatalf("ResolveNeeds: %v", err)
	}
	if got := addedNames(added); got != "b,c" {
		t.Errorf("added = %q, want %q — the closure must reach through the pack it added",
			got, "b,c")
	}
	want := []string{
		"+ b (needed by a: x selected)",
		"+ c (needed by b)",
	}
	if len(causes) != len(want) {
		t.Fatalf("causes = %q, want %q", causes, want)
	}
	for i := range want {
		if causes[i] != want[i] {
			t.Errorf("cause %d = %q, want %q", i, causes[i], want[i])
		}
	}
}

// TestResolveNeedsRefusesACycle: two packs that need each other refuse, naming
// the loop. The join rule would terminate the fixpoint without this check — B's
// need on A finds A already selected — which is exactly why the cycle is checked
// structurally over the final live-edge graph and not left to termination to
// imply: the manifests are broken, and the user is owed the loop by name.
func TestResolveNeedsRefusesACycle(t *testing.T) {
	_, _, err := ResolveNeeds(
		[]*Pack{needsPack("a", need("b"))},
		needsUniverse(
			needsPack("a"), // a itself is embedded: the cycle, not WB-D9, must fire
			needsPack("b", need("a"))))
	if err == nil {
		t.Fatal("a needs cycle was accepted")
	}
	if !strings.Contains(err.Error(), "a → b → a") {
		t.Errorf("the refusal must name the loop: %v", err)
	}
}

// TestResolveNeedsRefusesASelfCycle: the length-one loop is still a loop.
func TestResolveNeedsRefusesASelfCycle(t *testing.T) {
	_, _, err := ResolveNeeds(
		[]*Pack{needsPack("a", need("a"))},
		needsUniverse(needsPack("a")))
	if err == nil || !strings.Contains(err.Error(), "a → a") {
		t.Errorf("a self-need must refuse naming the loop: %v", err)
	}
}

// TestResolveNeedsRefusesANonEmbeddedTarget: a need naming a pack outside the
// embedded universe refuses, naming BOTH the needing pack and the target. The
// check runs even though nothing would be added — WB-D9 rules on the
// declaration, and a masked refusal would surface only after the user dropped
// the pack that satisfied it.
func TestResolveNeedsRefusesANonEmbeddedTarget(t *testing.T) {
	_, _, err := ResolveNeeds(
		[]*Pack{needsPack("a", need("ghost"))},
		needsUniverse(needsPack("b")))
	if err == nil {
		t.Fatal("a need naming a non-embedded pack was accepted")
	}
	for _, want := range []string{"pack a", `"ghost"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name %s: %v", want, err)
		}
	}
}

// TestResolveNeedsJoinsAnAlreadySelectedPack: the needed pack being selected
// already — by the user, or by an earlier need — is a no-op. Nothing is added,
// nothing prints, and the user's own selection is never overridden.
func TestResolveNeedsJoinsAnAlreadySelectedPack(t *testing.T) {
	a := needsPack("a", need("b"))
	selected := []*Pack{a, needsPack("b")}
	added, causes, err := ResolveNeeds(selected, needsUniverse(needsPack("b")))
	if err != nil {
		t.Fatalf("ResolveNeeds: %v", err)
	}
	if len(added) != 0 || len(causes) != 0 {
		t.Errorf("an already-selected need must be a silent no-op, got %v / %q",
			added, causes)
	}
	// Purity: the caller's slice is the caller's. The join must not have
	// reordered or rewritten it.
	if len(selected) != 2 || selected[0] != a {
		t.Errorf("ResolveNeeds modified the selected slice: %v", selected)
	}
}

// TestResolveNeedsSkipsAConditionNothingSatisfies: a when_bins naming a bin no
// selected pack installs is dead — nothing is added and nothing prints.
func TestResolveNeedsSkipsAConditionNothingSatisfies(t *testing.T) {
	added, causes, err := ResolveNeeds(
		[]*Pack{needsPack("a", need("b", "y")), binPack("p", "x")},
		needsUniverse(binPack("b", "y")),
	)
	if err != nil {
		t.Fatalf("ResolveNeeds: %v", err)
	}
	if len(added) != 0 || len(causes) != 0 {
		t.Errorf("a condition nothing satisfies must add nothing, got %v / %q",
			added, causes)
	}
}

// TestResolveNeedsAddsAnUnconditionalNeed: no when_bins, no condition — the need
// is live whenever the needing pack is selected, and the cause line names no bin.
func TestResolveNeedsAddsAnUnconditionalNeed(t *testing.T) {
	added, causes, err := ResolveNeeds(
		[]*Pack{needsPack("a", need("b"))},
		needsUniverse(needsPack("b")))
	if err != nil {
		t.Fatalf("ResolveNeeds: %v", err)
	}
	if len(added) != 1 || added[0].Name != "b" {
		t.Fatalf("added = %q, want %q", addedNames(added), "b")
	}
	if len(causes) != 1 || causes[0] != "+ b (needed by a)" {
		t.Errorf("causes = %q, want [\"+ b (needed by a)\"]", causes)
	}
}

// TestResolveNeedsConditionIsOROverTheBins: any one bin in when_bins firing
// makes the need live, and the cause names the first MATCH in declaration order.
func TestResolveNeedsConditionIsOROverTheBins(t *testing.T) {
	added, causes, err := ResolveNeeds(
		[]*Pack{needsPack("a", need("b", "y", "x")), binPack("p", "x")},
		needsUniverse(needsPack("b")))
	if err != nil {
		t.Fatalf("ResolveNeeds: %v", err)
	}
	if len(added) != 1 || added[0].Name != "b" {
		t.Fatalf("added = %q, want %q", addedNames(added), "b")
	}
	if len(causes) != 1 || causes[0] != "+ b (needed by a: x selected)" {
		t.Errorf("causes = %q, want the OR to fire on x", causes)
	}
}

// TestResolveNeedsRepeatsUntilStable: the design says "repeat until stable", not
// "one pass in selection order". Here A's condition is dead when A is first
// processed and live only after a LATER addition (f pulls in c, which installs
// the bin) — a single pass would stop short of b, so the second pass is what
// this pins.
func TestResolveNeedsRepeatsUntilStable(t *testing.T) {
	selected := []*Pack{
		needsPack("a", need("b", "x")),
		needsPack("f", need("c")),
	}
	added, _, err := ResolveNeeds(selected, needsUniverse(
		needsPack("b"),
		binPack("c", "x"),
	))
	if err != nil {
		t.Fatalf("ResolveNeeds: %v", err)
	}
	if got := addedNames(added); got != "c,b" {
		t.Errorf("added = %q, want %q — b's condition went live when c joined",
			got, "c,b")
	}
}

// TestResolveNeedsCycleThroughALiveConditionOnly: a needs cycle that passes
// through a condition-false need is no cycle — only live needs are edges.
func TestResolveNeedsCycleThroughALiveConditionOnly(t *testing.T) {
	added, _, err := ResolveNeeds(
		[]*Pack{needsPack("a", need("b"))},
		needsUniverse(
			// b needs a back, but only when some pack installs z — nothing does.
			needsPack("b", need("a", "z")),
		))
	if err != nil {
		t.Fatalf("a cycle through a dead condition is not a cycle: %v", err)
	}
	if len(added) != 1 || added[0].Name != "b" {
		t.Errorf("added = %q, want %q", addedNames(added), "b")
	}
}
