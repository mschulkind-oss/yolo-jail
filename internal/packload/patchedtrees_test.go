package packload

// patchedtrees_test.go pins the pack-set half of a PATCHED EXTENSION
// (docs/design/patched-extensions.md §1, §4, §8.2, §10): the selection lists each one as a Fork
// with its landing, its OWNING AGENT PACK is read off the contributing pack's list entry equal to
// `~/<into>` (PPX-D4) with where that entry reaches, `agent_updates` holds it from the contributing
// pack, the owner and the owner's forks (PPX-D9), the one lint names the line to add (PPX-D10),
// and the footprint marks it for review (PPX-D15).

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

const treeInto = ".pi/agent/yolo-patched/pi-subagents"

func patchedTreeContribution() packdecl.Contribution {
	return packdecl.Contribution{Kind: packdecl.KindFiles, Into: treeInto,
		Source:  "git+https://github.com/upstream/pi-subagents?ref=main",
		Patches: "patches/pi-subagents", Build: "npm install --omit=dev --ignore-scripts"}
}

func piPackagesList(entries ...string) packdecl.Contribution {
	add, _ := json.Marshal(entries)
	return packdecl.Contribution{Kind: packdecl.KindConfigList, Surface: "pi/settings", Path: "/packages", Add: add}
}

func embeddedPack(t *testing.T, name string) *Pack {
	t.Helper()
	for _, p := range Embedded() {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no embedded pack %s", name)
	return nil
}

func TestPatchedTreesListEachExtensionWithItsOwningAgentPack(t *testing.T) {
	pi := embeddedPack(t, "pi")
	matt := agentPack(t, "matt", patchedTreeContribution(), piPackagesList("npm:other", "~/"+treeInto))
	trees := PatchedTrees([]*Pack{pi, matt})
	if len(trees) != 1 {
		t.Fatalf("PatchedTrees = %d entries, want 1", len(trees))
	}
	f := trees[0]
	if !f.IsTree() || f.Key() != "matt/pi-subagents" || f.Bin != "pi-subagents" || f.Into != treeInto || f.Base != "" {
		t.Errorf("the extension reads as %+v", f)
	}
	if f.Owner != "pi" || !f.ListedInJail || !f.ListedAtHost {
		t.Errorf("owner %q (jail %v, host %v), want pi on a config-list, which reaches both", f.Owner,
			f.ListedInJail, f.ListedAtHost)
	}
	if f.Label() != "extension matt/pi-subagents" || f.Thing() != "extension matt/pi-subagents" ||
		f.CaptureArg() != "matt/pi-subagents" {
		t.Errorf("an extension's names: %q, %q, %q", f.Label(), f.Thing(), f.CaptureArg())
	}
	if len(Forks([]*Pack{pi, matt})) != 0 {
		t.Error("a patched extension is listed as a fork of a program")
	}
}

// A GUARDED POSTURE LIST reaches the host and no jail (patched-extensions.md §3.2, pi-automode), and
// no list entry at all is no owner.
func TestTheOwnersReachIsTheListEntrysAndNoEntryIsNoOwner(t *testing.T) {
	pi := embeddedPack(t, "pi")
	add, _ := json.Marshal([]string{"~/" + treeInto})
	guarded := packdecl.Contribution{Kind: packdecl.KindAutonomy, Guarded: &packdecl.AutonomyPosture{
		Lists: []packdecl.PostureList{{Surface: "pi/settings", Path: "/packages", Add: add}}}}
	matt := agentPack(t, "matt", patchedTreeContribution(), guarded)
	f := PatchedTrees([]*Pack{pi, matt})[0]
	if f.Owner != "pi" || f.ListedInJail || !f.ListedAtHost {
		t.Errorf("a guarded list: owner %q, jail %v, host %v; want pi at the host alone", f.Owner, f.ListedInJail, f.ListedAtHost)
	}
	bare := agentPack(t, "matt", patchedTreeContribution(), piPackagesList("~/"+treeInto+"/"))
	if f := PatchedTrees([]*Pack{pi, bare})[0]; f.Owner != "" {
		t.Errorf("an entry with a trailing slash is not equal to ~/<into>, and named owner %q", f.Owner)
	}
	if f := PatchedTrees([]*Pack{agentPack(t, "matt", patchedTreeContribution(), piPackagesList("~/"+treeInto))})[0]; f.Owner != "" {
		t.Errorf("with no selected pack declaring pi/settings the owner is %q", f.Owner)
	}
}

// PPX-D9: the contributing pack, the owning agent pack and every fork of the owner's programs hold
// the extension; a fork's hold packs are unchanged.
func TestAPatchedExtensionsHoldPacks(t *testing.T) {
	pi := embeddedPack(t, "pi")
	matt := agentPack(t, "matt", patchedTreeContribution(), piPackagesList("~/"+treeInto))
	fork := claimPack(t, "pi-fork", forkContribution("pi", "pi"))
	f := PatchedTrees([]*Pack{pi, fork, matt})[0]
	if got := strings.Join(f.HoldPacks(), ","); got != "matt,pi,pi-fork" {
		t.Errorf("HoldPacks = %s, want matt,pi,pi-fork", got)
	}
	if got := strings.Join(Forks([]*Pack{pi, fork})[0].HoldPacks(), ","); got != "pi-fork,pi" {
		t.Errorf("a fork's HoldPacks = %s, want pi-fork,pi", got)
	}
}

// PPX-D10: the lint warns, naming the line to add, when no list entry of the pack equals
// `~/<into>`, and is silent once one does.
func TestThePatchedTreeLintNamesTheListEntryToAdd(t *testing.T) {
	matt := agentPack(t, "matt", patchedTreeContribution(), piPackagesList("git:github.com/me/pi-subagents"))
	got := LintPatchedTrees(matt, nil)
	if len(got) != 1 || !strings.Contains(got[0], `"~/`+treeInto+`"`) || !strings.Contains(got[0], "no agent loads it") {
		t.Errorf("lint = %q, want one warning naming ~/%s", got, treeInto)
	}
	listed := agentPack(t, "matt", patchedTreeContribution(), piPackagesList("~/"+treeInto))
	if got := LintPatchedTrees(listed, nil); len(got) != 0 {
		t.Errorf("a listed tree is linted: %q", got)
	}
}

// PPX-D15: the footprint marks a patched extension for review, naming its source with its ref, its
// series, its follow rule, its build and its landing, in place of "read-only tree".
func TestAPatchedExtensionsFootprintClaimIsMarkedForReview(t *testing.T) {
	matt := agentPack(t, "matt", patchedTreeContribution())
	fp := FootprintOf(matt)
	var claim *Claim
	for i := range fp.Claims {
		if fp.Claims[i].Kind == packdecl.KindFiles {
			claim = &fp.Claims[i]
		}
	}
	if claim == nil {
		t.Fatal("no files claim for the patched extension")
	}
	if !claim.ReviewWorthy || claim.Target != treeInto {
		t.Errorf("claim %+v is not review-marked at its landing", claim)
	}
	for _, w := range []string{"github.com/upstream/pi-subagents?ref=main", "patches/pi-subagents",
		"following", "npm install --omit=dev"} {
		if !strings.Contains(claim.Detail, w) {
			t.Errorf("claim detail %q does not name %q", claim.Detail, w)
		}
	}
	if strings.Contains(claim.Detail, "read-only tree") {
		t.Error("a patched extension still claims a plain read-only tree")
	}
	sentence := claim.DisclosureSentence()
	if !strings.Contains(sentence, "UNREVIEWED") || !strings.Contains(sentence, "~/"+treeInto) {
		t.Errorf("disclosure %q does not say the upstream's code arrives unreviewed at the landing", sentence)
	}
}
