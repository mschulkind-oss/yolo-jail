package hostskills

// deliver_test.go pins the invariants that make it safe to point this code at a real home.
// Every test here answers one question a user would ask before running `apply --host`:
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

// testReq builds a Request with temp dirs for everything writable.
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
		Manifest:    &Manifest{Entries: map[string]string{}},
		ArchiveRoot: ArchiveRoot(filepath.Join(t.TempDir(), "archive")),
		Stamp:       "20260801-000000",
	}, packSkills
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
	if !req.Manifest.OwnedBy(dest, "matt-core") {
		t.Fatal("a delivered entry must be recorded, or the next apply cannot update it")
	}
	// Second apply updates its own entry rather than skipping it as the user's.
	writeSkill(t, packSkills, "packonly", "v2")
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
	req.Manifest = &Manifest{Entries: map[string]string{}}
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

// Tier B removal is authorized by the record and goes through the archive.
func TestFlatArchivesDroppedSkill(t *testing.T) {
	req, packSkills := testReq(t, TierFlat)
	dropped := writeSkill(t, packSkills, "goes", "bye")
	writeSkill(t, packSkills, "stays", "here")
	if _, err := Deliver(req); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dropped); err != nil {
		t.Fatal(err)
	}
	results, err := Deliver(req)
	if err != nil {
		t.Fatal(err)
	}
	r := find(t, results, "goes")
	if r.Action != ActionArchived {
		t.Fatalf("action = %q, want %q (%q)", r.Action, ActionArchived, r.Detail)
	}
	// Recoverable: the content is in the archive, not gone.
	archived := strings.TrimPrefix(r.Detail, "moved to ")
	if _, err := os.Stat(filepath.Join(archived, "SKILL.md")); err != nil {
		t.Errorf("archived content must be recoverable at the reported path %q: %v", archived, err)
	}
	if _, err := os.Stat(filepath.Join(req.SkillsDir, "stays", "SKILL.md")); err != nil {
		t.Errorf("the still-shipped skill must survive: %v", err)
	}
}

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

// An unstated tier is FLAT: the safe treatment, never the permissive one. A pack that says
// nothing must not get authority over a subtree of the user's home.
func TestUnstatedTierIsFlat(t *testing.T) {
	got, ok := ParseTier("")
	if !ok || got != TierFlat {
		t.Errorf("ParseTier(\"\") = %v, %v; want flat, true", got, ok)
	}
	// A typo must also degrade to flat, but be reportable.
	if got, ok := ParseTier("namespcaed"); ok || got != TierFlat {
		t.Errorf("ParseTier(typo) = %v, %v; want flat, false", got, ok)
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
