package packload

// forks_test.go pins the pack-set half of the fork route (docs/design/forked-programs-as-packs.md):
// a fork claims no name, and its footprint keys apart from its base's.

import (
	"slices"
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

// TestPackBasesFollowAConfiguredBasesOwnForks: a fork whose configured base itself forks a third
// configured pack's program hands its fork every base down that chain (Fork.PackBases), so a build
// sealed to the fork pack and its bases is a selection the fork rewrite accepts, as it accepts the
// user's whole selection; a fork rewrite refused for a base it lacks is what a build jail met
// before its build line ran ("basepack forks pack cpack's "c", and cpack is not in this selection",
// docs/design/patched-extensions.md PPX-D39). A cycle of forks ends the walk. Red if PackBases
// stops at the fork pack's own bases.
func TestPackBasesFollowAConfiguredBasesOwnForks(t *testing.T) {
	cpack := claimPack(t, "cpack", packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "c", Via: "npm", Package: "c"})
	basepack := claimPack(t, "basepack",
		packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "tool", Via: "npm", Package: "tool"},
		forkContribution("c", "cpack"))
	forkpack := claimPack(t, "forkpack", forkContribution("tool", "basepack"))
	all := []*Pack{cpack, basepack, forkpack}
	if _, err := ApplyForks(all); err != nil {
		t.Fatalf("the user's whole selection is refused: %v", err)
	}
	byName := map[string]*Pack{}
	for _, p := range all {
		byName[p.Name] = p
	}
	for _, f := range Forks(all) {
		sealed := []*Pack{byName[f.Pack], byName[f.Base]}
		for _, b := range f.PackBases {
			if b != f.Base {
				sealed = append(sealed, byName[b])
			}
		}
		if _, err := ApplyForks(sealed); err != nil {
			t.Errorf("fork %s sealed to its pack and PackBases %v is refused: %v", f.Key(), f.PackBases, err)
		}
		if f.Pack == "forkpack" && !slices.Equal(f.PackBases, []string{"basepack", "cpack"}) {
			t.Errorf("fork %s carries PackBases %v, want [basepack cpack]", f.Key(), f.PackBases)
		}
	}

	a := claimPack(t, "a", packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "x", Via: "npm", Package: "x"},
		forkContribution("y", "b"))
	b := claimPack(t, "b", packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "y", Via: "npm", Package: "y"},
		forkContribution("x", "a"))
	for _, f := range Forks([]*Pack{a, b}) {
		if want := map[string]string{"a": "b", "b": "a"}[f.Pack]; !slices.Equal(f.PackBases, []string{want}) {
			t.Errorf("in a cycle, fork %s carries PackBases %v, want [%s]", f.Key(), f.PackBases, want)
		}
	}
}
