package hostskills

// deliver_test.go pins the invariants that make it safe to point this code at a real home.
// Every test here answers one question a user would ask before running `yolo host apply`:
// "will this delete my skills?", "can I still add my own?", "what happens if I run it
// twice?", "what happens when a pack drops a skill?"
//
// The homes are all t.TempDir(). A test in this package must never touch a real $HOME —
// that is the one failure whose cost is not bounded by the test run.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkill creates a minimal skill dir (the shape every one of these tools reads: a
// directory whose name is the identity, containing SKILL.md).
func writeSkill(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: d\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// testReq builds a Request with temp dirs for everything writable — ONE LAYER of a composition,
// which is what this file tests. The destination-wide passes (adoption, migration, retire) belong
// to compose.go and are tested in compose_test.go.
//
// Composed and Claimed are the composition's plumbing: Composed is the record a real apply
// persists, Claimed the per-run set that makes layer order the precedence. Both start empty, which
// is a first apply into an untouched home.
func testReq(t *testing.T, tier Tier) (Request, string) {
	t.Helper()
	home := t.TempDir()
	packSkills := filepath.Join(t.TempDir(), "skills")
	if err := os.MkdirAll(packSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	return Request{
		Pack:        "matt-core",
		Description: "test pack",
		Sources:     []string{packSkills},
		SkillsDir:   filepath.Join(home, ".claude", "skills"),
		Tier:        tier,
		Composed:    &Manifest{Entries: map[string]string{}},
		Claimed:     map[string]string{},
		ArchiveRoot: ArchiveRoot(filepath.Join(t.TempDir(), "archive")),
		Stamp:       "20260801-000000",
	}, packSkills
}

// reapply re-runs one layer the way a SECOND apply would: the record persists, the per-run claim
// set does not, and what the record owned going in is what may be archived on the way out.
//
// A test that reused the same Request would carry the first run's Claimed forward, which is
// precisely the state a real second apply does not have — and Claimed is what suppresses the
// archive-the-previous-copy branch, so reusing it would hide the very behavior these tests pin.
func reapply(req *Request) {
	req.PreOwned = map[string]bool{}
	for dest := range req.Composed.Entries {
		req.PreOwned[dest] = true
	}
	req.Claimed = map[string]string{}
}

func find(t *testing.T, results []Result, name string) Result {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no result for %q in %+v", name, results)
	return Result{}
}

// TIER A, the headline claim: a pack owns ONE subtree and the user's sibling entries are
// untouched — so "can a user add a skill directly?" is yes, structurally, with no
// collision possible.
func TestNamespacedLeavesSiblingsAlone(t *testing.T) {
	req, packSkills := testReq(t, TierNamespaced)
	writeSkill(t, packSkills, "packskill", "from the pack")

	// The user's own skills, including one with the SAME name the pack ships.
	if err := os.MkdirAll(req.SkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, req.SkillsDir, "my-own", "MINE")
	writeSkill(t, req.SkillsDir, "packskill", "ALSO MINE, same name")

	if _, err := Deliver(req); err != nil {
		t.Fatal(err)
	}

	// Both user skills survive byte-for-byte.
	for _, name := range []string{"my-own", "packskill"} {
		data, err := os.ReadFile(filepath.Join(req.SkillsDir, name, "SKILL.md"))
		if err != nil {
			t.Fatalf("user skill %q must survive: %v", name, err)
		}
		if !strings.Contains(string(data), "MINE") {
			t.Errorf("user skill %q was overwritten by the pack: %s", name, data)
		}
	}
	// The pack's copy lives in its own namespace.
	if _, err := os.Stat(filepath.Join(req.SkillsDir, "matt-core", "skills", "packskill", "SKILL.md")); err != nil {
		t.Errorf("pack skill should land under its own subtree: %v", err)
	}
	// And the manifest marks the subtree as yolo's.
	if !IsYoloPluginDir(filepath.Join(req.SkillsDir, "matt-core")) {
		t.Error("delivered subtree must carry the yolo-managed marker, or a later apply " +
			"cannot tell it apart from a plugin the user wrote by hand")
	}
}

// Idempotence: two asserts produce identical trees. This is the test that catches an
// accumulating render (the class of bug that made a naive briefing append duplicate prose).
func TestNamespacedIsIdempotent(t *testing.T) {
	req, packSkills := testReq(t, TierNamespaced)
	writeSkill(t, packSkills, "a", "one")
	writeSkill(t, packSkills, "b", "two")

	if _, err := Deliver(req); err != nil {
		t.Fatal(err)
	}
	first := treeSnapshot(t, req.SkillsDir)
	if _, err := Deliver(req); err != nil {
		t.Fatal(err)
	}
	if second := treeSnapshot(t, req.SkillsDir); first != second {
		t.Errorf("second apply changed the tree:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// A pre-existing directory at the pack's name that yolo did NOT write must not be absorbed:
// the tier degrades to flat and says why. Taking it over would mean silently adopting
// whatever the user put there — a plugin they authored, most likely.
func TestNamespacedRefusesForeignPackDir(t *testing.T) {
	req, packSkills := testReq(t, TierNamespaced)
	writeSkill(t, packSkills, "packskill", "from the pack")

	// The user has their OWN dir at the pack's name (no yolo marker).
	foreign := filepath.Join(req.SkillsDir, "matt-core")
	writeSkill(t, foreign, "handwritten", "MINE")

	results, err := Deliver(req)
	if err != nil {
		t.Fatal(err)
	}
	var downgraded bool
	for _, r := range results {
		if r.Action == ActionRefused && strings.Contains(r.Detail, "not written by yolo") {
			downgraded = true
		}
	}
	if !downgraded {
		t.Errorf("a foreign dir at the pack's name must downgrade the tier and say so: %+v", results)
	}
	// The user's content is intact.
	if _, err := os.Stat(filepath.Join(foreign, "handwritten", "SKILL.md")); err != nil {
		t.Errorf("user content at the pack's path must survive: %v", err)
	}
}

// Tier A removal: a skill the pack stops shipping is retired from yolo's own subtree — and
// archived rather than deleted, so even here a mistake is recoverable.
func TestNamespacedRetiresDroppedSkill(t *testing.T) {
	req, packSkills := testReq(t, TierNamespaced)
	writeSkill(t, packSkills, "keep", "stays")
	dropped := writeSkill(t, packSkills, "drop", "goes away")
	if _, err := Deliver(req); err != nil {
		t.Fatal(err)
	}
	// The pack stops shipping "drop".
	if err := os.RemoveAll(dropped); err != nil {
		t.Fatal(err)
	}
	results, err := Deliver(req)
	if err != nil {
		t.Fatal(err)
	}
	r := find(t, results, "drop")
	if r.Action != ActionArchived {
		t.Errorf("dropped skill action = %q, want %q", r.Action, ActionArchived)
	}
	if !strings.Contains(r.Detail, "moved to ") {
		t.Errorf("archive must report WHERE it moved the skill, or it reads as a delete: %q", r.Detail)
	}
	if _, err := os.Stat(filepath.Join(req.SkillsDir, "matt-core", "skills", "drop")); !os.IsNotExist(err) {
		t.Error("dropped skill should no longer be loadable")
	}
	if _, err := os.Stat(filepath.Join(req.SkillsDir, "matt-core", "skills", "keep", "SKILL.md")); err != nil {
		t.Errorf("the still-shipped skill must survive: %v", err)
	}
}

// TIER B, the headline claim: an entry yolo cannot prove it wrote is the user's, and is
// skipped with a report — never overwritten. This is what makes a flat, namespace-less tool
// safe to manage at all.
func TestFlatSkipsUserOwnedEntry(t *testing.T) {
	req, packSkills := testReq(t, TierFlat)
	writeSkill(t, packSkills, "shared-name", "FROM PACK")

	if err := os.MkdirAll(req.SkillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, req.SkillsDir, "shared-name", "MINE")

	results, err := Deliver(req)
	if err != nil {
		t.Fatal(err)
	}
	if r := find(t, results, "shared-name"); r.Action != ActionSkippedUser {
		t.Errorf("action = %q, want %q (detail %q)", r.Action, ActionSkippedUser, r.Detail)
	}
	data, err := os.ReadFile(filepath.Join(req.SkillsDir, "shared-name", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "MINE") {
		t.Errorf("the user's skill was overwritten: %s", data)
	}
}

// Tier B writes into a free name and records it, so the NEXT apply knows it owns that entry
// and may update it. Without the record, yolo could add a skill but never fix one.
func TestFlatWritesAndRecordsOwnership(t *testing.T) {
	req, packSkills := testReq(t, TierFlat)
	writeSkill(t, packSkills, "packonly", "v1")

	results, err := Deliver(req)
	if err != nil {
		t.Fatal(err)
	}
	if r := find(t, results, "packonly"); r.Action != ActionWrote {
		t.Fatalf("action = %q, want %q (%q)", r.Action, ActionWrote, r.Detail)
	}
	dest := filepath.Join(req.SkillsDir, "packonly")
	// The composing PACK is recorded, which is what still answers ruling R1's "did a pack that left
	// my config put this here?". What §6a-5 changed is not the value but how the write gate READS it
	// — membership, not equality — so a later layer can legitimately take the name
	// (TestFlatLaterLayerWinsTheName).
	if !req.Composed.OwnedBy(dest, "matt-core") {
		t.Fatalf("a delivered entry must be recorded under its composing pack, or a later apply "+
			"can neither update nor retire it: %v", req.Composed.Entries)
	}
	// Second apply updates its own entry rather than skipping it as the user's.
	writeSkill(t, packSkills, "packonly", "v2")
	reapply(&req)
	results, err = Deliver(req)
	if err != nil {
		t.Fatal(err)
	}
	if r := find(t, results, "packonly"); r.Action != ActionWrote {
		t.Errorf("yolo must be able to update its OWN entry: action = %q (%q)", r.Action, r.Detail)
	}
	data, _ := os.ReadFile(filepath.Join(dest, "SKILL.md"))
	if !strings.Contains(string(data), "v2") {
		t.Errorf("update did not land: %s", data)
	}
}

// A LOST record (pruned state dir, fresh machine, interrupted write) must fail CLOSED: the
// entry becomes the user's and is left alone. The cost is that yolo stops updating its own
// skill; the alternative cost is overwriting a file the user may have written.
func TestFlatFailsClosedWhenRecordIsLost(t *testing.T) {
	req, packSkills := testReq(t, TierFlat)
	writeSkill(t, packSkills, "packonly", "v1")
	if _, err := Deliver(req); err != nil {
		t.Fatal(err)
	}
	// Simulate the record being lost.
	req.Composed = &Manifest{Entries: map[string]string{}}
	req.Claimed = map[string]string{}
	writeSkill(t, packSkills, "packonly", "v2")

	results, err := Deliver(req)
	if err != nil {
		t.Fatal(err)
	}
	if r := find(t, results, "packonly"); r.Action != ActionSkippedUser {
		t.Errorf("action = %q, want %q: with no record, yolo cannot prove ownership and "+
			"must not overwrite", r.Action, ActionSkippedUser)
	}
	data, _ := os.ReadFile(filepath.Join(req.SkillsDir, "packonly", "SKILL.md"))
	if !strings.Contains(string(data), "v1") {
		t.Errorf("content should be untouched when ownership is unprovable: %s", data)
	}
}

// Tier-B REMOVAL is not a layer's job any more: it moved to the destination-wide pass, and its
// tests moved with it (compose_test.go's TestComposeRetiresWhatNoLayerShips and
// TestComposeRetireClearsADanglingEntry).
//
// Not merely relocated — the behavior it pinned was WRONG once several packs merge into one dir. A
// per-pack retire can only see "what this pack recorded minus what it ships now", so a name moving
// from pack A to pack B between applies read as A retiring it and B being refused it.
// TestFlatNameChangingHandsIsAnUpdate is the case that could not be expressed here.

// Observe posture writes NOTHING, at either tier, while still reporting what it would do.
// A dry run that diverges from the real thing is worse than no dry run.
func TestObserveWritesNothing(t *testing.T) {
	for _, tier := range []Tier{TierNamespaced, TierFlat} {
		t.Run(tier.String(), func(t *testing.T) {
			req, packSkills := testReq(t, tier)
			req.Observe = true
			writeSkill(t, packSkills, "packskill", "x")

			results, err := Deliver(req)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) == 0 {
				t.Fatal("observe must still report what it would do")
			}
			if _, err := os.Stat(req.SkillsDir); !os.IsNotExist(err) {
				t.Errorf("observe created %s — it must write nothing", req.SkillsDir)
			}
		})
	}
}

// UNNAMESPACED IS THE DEFAULT (S1), and namespacing takes a positive declaration. A pack that
// says nothing must not get authority over a subtree of the user's home — and, since the ruling,
// must not have its skills invoked under a namespace it never asked for either.
func TestUnstatedTierIsFlat(t *testing.T) {
	if got := PackTier(""); got != TierFlat {
		t.Errorf("PackTier(\"\") = %v, want flat — unnamespaced is the default", got)
	}
	if got := PackTier("flat"); got != TierFlat {
		t.Errorf("PackTier(\"flat\") = %v, want flat", got)
	}
	// The POSITIVE choice, and the only spelling of it.
	if got := PackTier("namespaced"); got != TierNamespaced {
		t.Errorf("PackTier(\"namespaced\") = %v, want namespaced", got)
	}
	// A value packdecl's validator would have rejected reaches the default rather than the
	// permissive tier: the one route here is a manifest read tolerantly across a version
	// boundary, and inventing a namespace from a typo is the outcome that costs the user a name.
	if got := PackTier("namespcaed"); got != TierFlat {
		t.Errorf("PackTier(typo) = %v, want flat", got)
	}
}

// treeSnapshot renders a dir's paths+contents into a comparable string, for idempotence
// assertions.
func treeSnapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if fi.IsDir() {
			b.WriteString("d " + rel + "\n")
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		b.WriteString("f " + rel + " " + string(data) + "\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// A pack that carries no skills must leave NO trace — not even an empty plugin dir.
//
// This is not a hypothetical: the shipped agent packs each declare a `skills` contribution
// purely to name the destination their agent reads from, and ship no skills of their own. An
// earlier cut created <skillsDir>/<pack>/ with a manifest and an empty skills/ subdir for
// every one of them, which is a loadable, listed, empty plugin in the user's home.
func TestNoSkillsLeavesNoTrace(t *testing.T) {
	for _, tier := range []Tier{TierNamespaced, TierFlat} {
		t.Run(tier.String(), func(t *testing.T) {
			req, _ := testReq(t, tier) // source dir exists but is empty
			results, err := Deliver(req)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 0 {
				t.Errorf("a pack with no skills should report nothing, got %+v", results)
			}
			if _, err := os.Stat(req.SkillsDir); !os.IsNotExist(err) {
				t.Errorf("%s was created for a pack that ships no skills", req.SkillsDir)
			}
		})
	}
}
