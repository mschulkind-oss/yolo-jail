package packload

// forks_test.go pins the pack-set half of the fork route (docs/design/forked-programs-as-packs.md):
// a fork claims no name, and its footprint keys apart from its base's.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// forkContribution is a valid fork of base's bin program.
func forkContribution(bin, base string) packdecl.Contribution {
	return packdecl.Contribution{
		Kind: packdecl.KindProgram, Bin: bin, Via: packdecl.ViaSource, ForkOf: base,
		Source: "git+https://example.com/" + bin + "-fork?ref=main", Build: "make install",
		Produces: []string{".local/bin/" + bin},
	}
}

// TestAForkClaimsNoAgentName is the OQ-FP5 ruling at the pass that would refuse it: the base
// declares `program pi`, the fork declares a fork of it, and the two are not two owners of `pi`.
// Without the skip every fork would refuse its own launch, which is the claude-matt-fork shape
// agentnamecollisions_test.go pins as a real collision when the second pack DOES claim the name.
func TestAForkClaimsNoAgentName(t *testing.T) {
	base := claimPack(t, "pi",
		packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "pi", Via: "npm", Package: "pi-coding-agent"})
	fork := claimPack(t, "pi-matt", forkContribution("pi", "pi"))
	if cols := AgentNameCollisions([]*Pack{base, fork}); len(cols) != 0 {
		t.Errorf("a fork and its base collided on an agent name: %+v", cols)
	}
	if names := AgentNames([]*Pack{fork}); len(names) != 0 {
		t.Errorf("a fork alone claims agent names %v — it claims none", names)
	}
	if got := fork.InstallBins(); len(got) != 0 {
		t.Errorf("a fork pack installs bins %v by itself — its program is its base's", got)
	}
}

// TestAForkFootprintKeysApartFromItsBase: the generic exclusive `program` loop must not report the
// base and its fork as two owners of one bin, and two forks of one base DO collide, as the launch
// refuses them.
func TestAForkFootprintKeysApartFromItsBase(t *testing.T) {
	base := claimPack(t, "pi",
		packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "pi", Via: "npm", Package: "pi-coding-agent"})
	one := claimPack(t, "pi-one", forkContribution("pi", "pi"))
	two := claimPack(t, "pi-two", forkContribution("pi", "pi"))
	for _, c := range Collisions([]*Pack{base, one}) {
		if c.Kind == packdecl.KindProgram {
			t.Errorf("base and fork reported as a program collision: %+v", c)
		}
	}
	var forkCollision bool
	for _, c := range Collisions([]*Pack{base, one, two}) {
		if c.Kind == packdecl.KindProgram && c.Target == ForkClaimTarget("pi", "pi") {
			forkCollision = true
		}
	}
	if !forkCollision {
		t.Error("two forks of one base's program are not reported as a program collision")
	}
}

// TestEveryForkCarriesItsPacksForkBases: a pack forking two bases' programs hands each fork both
// bases (Fork.PackBases), which a build sealed to that pack carries so the rewrite of the other fork
// finds its base (docs/design/patched-extensions.md PPX-D39).
func TestEveryForkCarriesItsPacksForkBases(t *testing.T) {
	p := claimPack(t, "forks", forkContribution("pi", "pi"), forkContribution("claude", "claude"),
		forkContribution("pi2", "pi"))
	forks := Forks([]*Pack{p})
	if len(forks) != 3 {
		t.Fatalf("Forks = %+v, want three", forks)
	}
	for _, f := range forks {
		if len(f.PackBases) != 2 || f.PackBases[0] != "pi" || f.PackBases[1] != "claude" {
			t.Errorf("fork %s carries PackBases %v, want [pi claude]", f.Key(), f.PackBases)
		}
	}
}
