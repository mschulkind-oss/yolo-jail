package packload

// forkrewrite_test.go pins ApplyForks, the rewrite that gives a fork's base its delivery
// (docs/design/forked-programs-as-packs.md FP-D5, FP-D6), and the closure's join of a shipped base.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// richBase is a base pack whose program sets every field FP-D6 names, so the rewrite's keep and
// replace lists can each be checked against a value that was there.
func richBase(t *testing.T) *Pack {
	t.Helper()
	return claimPack(t, "pi", packdecl.Contribution{
		Kind: packdecl.KindProgram, Bin: "pi", Via: "npm", Package: "@earendil/pi-coding-agent@1.2.3",
		Flags: []string{"--omit=dev"}, Update: []string{"update", "--self"},
		InstallerEnv: map[string]string{"PREFIX": "~/.local"},
		InstallHints: map[string]string{"brew": "pi"}, Platforms: []string{"linux", "darwin"},
		ModelCatalog: []string{"dist/models.json"}, NodeFloor: "22.19",
		Refresh:                  &packdecl.Refresh{Argv: []string{"update", "--extensions"}, Lock: ".s/.yolo-update.lock"},
		Protocols:                []string{"openai"},
		ProviderSets:             true,
		PlatformSwitches:         []packdecl.PlatformSwitch{{Platform: "aws-bedrock", Surface: "pi/settings", Pointer: "/x"}},
		Capabilities:             []string{"web_search"},
		UnlistedBackgroundModels: true,
		ExactMenuRefuses:         &packdecl.ExactMenuRefusal{Providers: []string{"openai-codex"}},
		NeedsModelList:           []string{"aws-bedrock"},
	}, packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "other", Via: "npm", Package: "other"})
}

// TestTheForkRewriteKeepsAndReplacesExactlyFPD6sFields.
func TestTheForkRewriteKeepsAndReplacesExactlyFPD6sFields(t *testing.T) {
	base := richBase(t)
	fork := claimPack(t, "pi-matt", forkContribution("pi", "pi"))
	got, err := ApplyForks([]*Pack{base, fork})
	if err != nil {
		t.Fatal(err)
	}
	if got[1] != fork {
		t.Error("the fork pack itself must come back unchanged")
	}
	prog := got[0].Decl.Contributes[0]
	// REPLACED: the delivery fields are the fork's, so absent unless it sets them.
	if prog.Via != packdecl.ViaSource || prog.Package != "" || prog.URL != "" || prog.Flags != nil ||
		prog.Update != nil || prog.VersionsDir != "" || prog.InstallerEnv != nil || prog.InstallHints != nil ||
		prog.Platforms != nil || prog.ModelCatalog != nil {
		t.Errorf("a delivery field survived from the base: %+v", prog)
	}
	if prog.Source != fork.Decl.Contributes[0].Source || prog.Build != "make install" ||
		strings.Join(prog.Produces, ",") != ".local/bin/pi" || prog.ForkedBy != "pi-matt" || prog.ForkOf != "" {
		t.Errorf("the fork's delivery did not land on the base's program: %+v", prog)
	}
	// KEPT: what the program does once it is there.
	if prog.Refresh == nil || strings.Join(prog.Protocols, ",") != "openai" || !prog.ProviderSets ||
		len(prog.PlatformSwitches) != 1 || strings.Join(prog.Capabilities, ",") != "web_search" ||
		!prog.UnlistedBackgroundModels || prog.ExactMenuRefuses == nil ||
		strings.Join(prog.ExactMenuRefuses.Providers, ",") != "openai-codex" || prog.NodeFloor != "22.19" ||
		strings.Join(prog.NeedsModelList, ",") != "aws-bedrock" {
		t.Errorf("a field the base keeps was dropped: %+v", prog)
	}
	// The base's OTHER program is not the fork's.
	if other := got[0].Decl.Contributes[1]; other.Via != "npm" || other.Package != "other" {
		t.Errorf("the rewrite touched a program the fork does not fork: %+v", other)
	}
	// One program per bin, as every reader of programs sees it.
	ins := got[0].Decl.InstallContributions()
	if len(ins) != 2 || ins[0].Kind != packdecl.InstallKindSource || ins[0].ForkedBy != "pi-matt" {
		t.Errorf("the base's installs after the rewrite: %+v", ins)
	}
	// A fork's own node_floor and platforms are its to set.
	withFloor := forkContribution("pi", "pi")
	withFloor.NodeFloor, withFloor.Platforms = "24", []string{"linux"}
	got, err = ApplyForks([]*Pack{richBase(t), claimPack(t, "pi-matt", withFloor)})
	if err != nil {
		t.Fatal(err)
	}
	if p := got[0].Decl.Contributes[0]; p.NodeFloor != "24" || strings.Join(p.Platforms, ",") != "linux" {
		t.Errorf("the fork's node_floor/platforms did not replace the base's: %+v", p)
	}
}

// TestTheForkRewriteLeavesTheInputUntouched: an embedded pack is one shared value per process, so
// the rewrite must copy — a verb that selects twice must not see the fork after the config
// dropped it.
func TestTheForkRewriteLeavesTheInputUntouched(t *testing.T) {
	base := richBase(t)
	before := base.Decl.Contributes[0]
	if _, err := ApplyForks([]*Pack{base, claimPack(t, "pi-matt", forkContribution("pi", "pi"))}); err != nil {
		t.Fatal(err)
	}
	if after := base.Decl.Contributes[0]; after.Via != before.Via || after.Package != before.Package ||
		after.ForkedBy != "" {
		t.Errorf("ApplyForks rewrote its input's program: %+v", after)
	}
	if got, err := ApplyForks([]*Pack{base}); err != nil || got[0] != base {
		t.Errorf("a selection with no fork must come back as it went in: %v %v", got, err)
	}
}

// TestTheEmbeddedBaseIsUntouchedAfterAForkSelection is the trap the plan names, measured on the
// real shared value: packload.Embedded()'s pi after a selection that forked it.
func TestTheEmbeddedBaseIsUntouchedAfterAForkSelection(t *testing.T) {
	// A materialization of this test's own, never the developer's home.
	t.Cleanup(OverrideEmbeddedCacheDir(t.TempDir()))
	var pi *Pack
	for _, p := range Embedded() {
		if p.Name == "pi" {
			pi = p
		}
	}
	if pi == nil {
		t.Fatal("no embedded pi pack in this build — the fixture this trap is about")
	}
	fork := claimPack(t, "pi-matt", forkContribution("pi", "pi"))
	got, err := ApplyForks([]*Pack{pi, fork})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] == pi {
		t.Fatal("the rewrite returned the shared embedded pack itself")
	}
	for _, in := range pi.Decl.InstallContributions() {
		if in.Bin == "pi" && in.Kind == packdecl.InstallKindSource {
			t.Fatal("the embedded pi pack carries the fork's delivery after one selection")
		}
	}
	for _, in := range got[0].Decl.InstallContributions() {
		if in.Bin == "pi" && in.Kind != packdecl.InstallKindSource {
			t.Fatalf("the rewritten pi's program is %q, not the fork's", in.Kind)
		}
	}
}

// TestTheForkRewritesRefusals: FP-D5's four refusals, each naming what to fix.
func TestTheForkRewritesRefusals(t *testing.T) {
	base := func() *Pack {
		return claimPack(t, "pi", packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "pi", Via: "npm", Package: "pi"})
	}
	cases := []struct {
		name  string
		packs func() []*Pack
		want  string
	}{
		{"no base", func() []*Pack { return []*Pack{claimPack(t, "pi-matt", forkContribution("pi", "pi"))} },
			"pi is not in this selection"},
		{"no such program", func() []*Pack {
			return []*Pack{base(), claimPack(t, "pi-matt", forkContribution("omp", "pi"))}
		}, `pi declares no program "omp"`},
		{"two forks of one program", func() []*Pack {
			return []*Pack{base(), claimPack(t, "fork-a", forkContribution("pi", "pi")),
				claimPack(t, "fork-b", forkContribution("pi", "pi"))}
		}, "both fork pack pi's"},
		{"a pack forking itself", func() []*Pack {
			return []*Pack{claimPack(t, "pi", forkContribution("pi", "pi"))}
		}, "forks itself"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ApplyForks(tc.packs())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
			if got != nil {
				t.Errorf("a refused rewrite returned packs: %v", got)
			}
		})
	}
}

// TestAForkBringsAShippedBase: the fork's base joins the closure the way an unconditional need
// does, with its own cause line — and a base yolo does not ship does not join (ApplyForks then
// names the fix).
func TestAForkBringsAShippedBase(t *testing.T) {
	pi := binPack("pi", "pi")
	fork := claimPack(t, "pi-matt", forkContribution("pi", "pi"))
	added, causes, err := ResolveNeeds([]*Pack{fork}, needsUniverse(pi))
	if err != nil {
		t.Fatal(err)
	}
	if addedNames(added) != "pi" || strings.Join(causes, "|") != "+ pi (forked by pi-matt)" {
		t.Errorf("added %q with causes %q, want pi forked by pi-matt", addedNames(added), causes)
	}
	// Already selected: a join, never a second copy.
	if added, _, _ := ResolveNeeds([]*Pack{pi, fork}, needsUniverse(pi)); len(added) != 0 {
		t.Errorf("a selected base was added again: %q", addedNames(added))
	}
	// Not shipped: nothing joins, and the rewrite is what refuses.
	if added, _, err := ResolveNeeds([]*Pack{fork}, needsUniverse()); err != nil || len(added) != 0 {
		t.Errorf("an unshipped base: added %q, err %v; want nothing and no refusal here", addedNames(added), err)
	}
}
