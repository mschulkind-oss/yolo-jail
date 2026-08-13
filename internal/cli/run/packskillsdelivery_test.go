package run

// packskillsdelivery_test.go pins WHERE a pack's skills go in a jail — the two facts the
// S4 audit measured (outstanding-work.md), neither of which anything asserted before.
//
// S4 suspected a hole in the selection gate: `into` names a specific agent's directory, and
// nothing checks that agent is one the user selected. The measurement says the behavior is
// real and the hole is not, because `into` is a PATH and core has no agent concept to check
// it against (packdecl's opening comment). A destination only exists because a pack the user
// SELECTED declared it, so the set of destinations is a function of the `packs` gate — and
// `packs` is user-scope only, so a repo-committed config still cannot add one.
//
// What is NOT a function of any declaration is the CONTENT ROUTING inside that set:
// PrepareSkills copies every loaded pack's skills into every target, so pack A's skills reach
// pack B's destination. That is the `skills` kind's CombineMerge feature (pack-system.md §3,
// §14) and the zero-ceremony merge depends on it — but it is also the jail doing exactly what
// packload.ResolveDestinations refuses to do at the host, and that asymmetry is an open
// question, not a settled ruling. These tests record the measurement so answering it is a
// deliberate edit rather than a rediscovery.

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agents"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// skillsPack writes a pack at <parent>/<name> declaring one `skills` contribution into
// `into`, carrying one skill named `skill`, and returns its path.
func skillsPack(t *testing.T, parent, name, into, skill string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, dir, `{"name":"`+name+`","contributes":[`+
		`{"kind":"skills","from":"skills","into":"`+into+`"}]}`)
	writeSkillTree(t, filepath.Join(dir, "skills"), skill)
	return dir
}

// stageSkillTargets runs the real jail staging path — stagePacks, then packSkillTargets, then
// PrepareSkills — and returns the loaded packs, the targets, and the staging root. This is the
// whole delivery chain, rather than any one link, because S4's question spans all three.
func stageSkillTargets(t *testing.T, cname string) ([]*packload.Pack, []agents.SkillTarget, string) {
	t.Helper()
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf()}
	agents.SetPackSkillDirs(nil)
	agents.SetPackSkillTargets(nil)
	t.Cleanup(func() { agents.SetPackSkillDirs(nil); agents.SetPackSkillTargets(nil) })
	_, loaded, _, err := o.stagePacks(cname)
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	targets := packSkillTargets(loaded)
	agents.SetPackSkillTargets(targets)
	staging, err := agents.PrepareSkills(cname, os.Getenv("HOME"), nil, false)
	if err != nil {
		t.Fatalf("PrepareSkills: %v", err)
	}
	return loaded, targets, staging
}

// stagedFor returns the sorted skill names staged for `dest`, and whether `dest` is a target
// at all.
func stagedFor(t *testing.T, targets []agents.SkillTarget, staging, dest string) ([]string, bool) {
	t.Helper()
	for _, tg := range targets {
		if tg.Dest != dest {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(staging, tg.Staging))
		if err != nil {
			t.Fatalf("reading staged dir for %s: %v", dest, err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		return names, true
	}
	return nil, false
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// S4 PROBE 1. A pack's `into` is honored against NO agent set: select the `claude` pack and
// one content pack naming `.codex/skills`, and the jail builds and mounts `.codex/skills`
// though no codex pack was selected.
//
// This is the intended model, not a leak, and the distinction is worth stating because the
// behavior reads alarming on its own. `into` is a home-relative PATH; core does not know that
// `.codex/skills` belongs to a tool called codex, and an agent registry to check it against is
// precisely what the pack model deleted. The gate that matters — could a WORKSPACE cause this? —
// is elsewhere and holds: `packs` is user-scope only, so every destination here traces back to a
// pack the user themselves named.
//
// Confirmed in a real nested jail, not only here: `~/.codex/skills` existed and held the content
// pack's skill.
func TestJailSkillsIntoNeedsNoAgentPackToBeSelected(t *testing.T) {
	home := packHome(t)
	rogue := skillsPack(t, t.TempDir(), "rogue", ".codex/skills", "rogueskill")
	writeUserPacks(t, home, `["claude",{"source":"file://`+rogue+`","name":"rogue"}]`)

	_, targets, staging := stageSkillTargets(t, "s4-into")

	names, ok := stagedFor(t, targets, staging, ".codex/skills")
	if !ok {
		t.Fatalf("no target for .codex/skills — a selected pack's `into` must be honored "+
			"whatever it names. targets = %v", targets)
	}
	if !hasName(names, "rogueskill") {
		t.Errorf(".codex/skills staged %v, without the declaring pack's own skill", names)
	}
	// And the built-in suite reaches it too, which is what makes it a real destination rather
	// than a bare copy: PrepareSkills treats every declared target identically.
	if !hasName(names, "jail-startup") {
		t.Errorf(".codex/skills staged %v, without the built-in suite", names)
	}
}

// S4 PROBE 2. Every loaded pack's skills reach EVERY declared destination — packa's skill lands
// in packb's `into` and vice versa, though neither pack named the other's path.
//
// INTENDED, and load-bearing: this is what lets a zero-ceremony pack (a bare `skills/` dir, no
// manifest) deliver anything at all. An agent pack's `skills` contribution "exists to NAME the
// destination other packs merge into" (pack-system.md §3), so the merge is the feature and the
// declared pairing is not the unit of delivery.
//
// The consequence the audit flagged, recorded here so a reader of this test sees it: what a pack
// DECLARES therefore understates where its content goes. `yolo pack footprint packa` prints
// `.alpha/skills` and nothing else.
func TestJailSkillsReachEveryDeclaredDestination(t *testing.T) {
	home := packHome(t)
	parent := t.TempDir()
	a := skillsPack(t, parent, "packa", ".alpha/skills", "alpha-only")
	b := skillsPack(t, parent, "packb", ".beta/skills", "beta-only")
	writeUserPacks(t, home,
		`[{"source":"file://`+a+`","name":"packa"},{"source":"file://`+b+`","name":"packb"}]`)

	_, targets, staging := stageSkillTargets(t, "s4-fanout")

	for _, tc := range []struct{ dest, foreign string }{
		{".alpha/skills", "beta-only"},
		{".beta/skills", "alpha-only"},
	} {
		names, ok := stagedFor(t, targets, staging, tc.dest)
		if !ok {
			t.Fatalf("no target for %s; targets = %v", tc.dest, targets)
		}
		if !hasName(names, tc.foreign) {
			t.Errorf("%s staged %v, missing %q — every pack's skills reach every destination, "+
				"which is what a zero-ceremony pack depends on", tc.dest, names, tc.foreign)
		}
	}
}

// THE NOTCH ASYMMETRY, measured rather than argued: the same two-pack set delivers the full
// cross product in a jail and only the declared pairing at the host.
//
// packload.ResolveDestinations' doc comment states the host's narrower rule and its reason —
// mirroring the jail "would mean an existing manifest suddenly writes into home directories its
// author never named". That reasoning is about a REAL $HOME and does not obviously transfer to a
// container, so this is not a defect either notch owns; it is the open question S4 leaves behind.
//
// The test exists so that answering it moves this assertion deliberately. Whichever way it goes,
// one of the two halves below changes, and a reader gets told the other half exists.
func TestJailAndHostDisagreeOnSkillsFanOut(t *testing.T) {
	home := packHome(t)
	parent := t.TempDir()
	a := skillsPack(t, parent, "packa", ".alpha/skills", "alpha-only")
	b := skillsPack(t, parent, "packb", ".beta/skills", "beta-only")
	writeUserPacks(t, home,
		`[{"source":"file://`+a+`","name":"packa"},{"source":"file://`+b+`","name":"packb"}]`)

	loaded, targets, staging := stageSkillTargets(t, "s4-notches")

	// The JAIL: both skills at .alpha/skills.
	names, ok := stagedFor(t, targets, staging, ".alpha/skills")
	if !ok {
		t.Fatalf("no jail target for .alpha/skills; targets = %v", targets)
	}
	if !hasName(names, "alpha-only") || !hasName(names, "beta-only") {
		t.Errorf("jail staged %v at .alpha/skills, want both packs' skills", names)
	}

	// The HOST: .alpha/skills is composed from packa alone.
	resolved, _ := packload.ResolveDestinations(loaded)
	var layers []string
	for _, d := range hostskills.ComposeHostSkills(resolved, home) {
		if d.Dir != filepath.Join(home, ".alpha", "skills") {
			continue
		}
		for _, l := range d.Layers {
			layers = append(layers, l.Pack)
		}
	}
	if len(layers) != 1 || layers[0] != "packa" {
		t.Errorf("host composed .alpha/skills from %v, want [packa] only — if this now matches "+
			"the jail's fan-out, the S4 open question was answered and the jail half above is "+
			"the assertion to revisit", layers)
	}
}
