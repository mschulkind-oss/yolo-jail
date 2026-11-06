package hostskills

// composeinto_test.go pins ComposeInto, the jail's entry into the one layer writer
// (docs/plans/notch-convergence.md#7-decision-ledger): the same tiers and the same collision refusal as the
// host render, over a directory yolo owns wholesale, and a reserved child never composed.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// composeFixture makes one pack's skills source carrying the named skills, and returns its dir.
func intoSource(t *testing.T, names ...string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "skills")
	for _, n := range names {
		dir := filepath.Join(src, n)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# "+n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return src
}

// A namespaced pack's skills land at `<namespace>-<skill>`, each its own top-level entry, with no
// per-pack subtree: the namespace names them, and a Tier on the layer changes nothing about its
// own skills (docs/reference/pack-system.md PN-D9) — the shape the host render writes, at the jail too.
func TestComposeIntoNamespacesAPackThatAskedForIt(t *testing.T) {
	dir := t.TempDir()
	d := Destination{Dir: "~/.claude/skills", Layers: []Layer{
		{Pack: "house", Namespace: "house", Tier: TierNamespaced, Description: "house rules",
			Sources: []string{intoSource(t, "review")}},
	}}
	got, err := ComposeInto(d, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "house-review", "SKILL.md")); err != nil {
		t.Errorf("the namespaced skill is not at house-review: %v", err)
	}
	for _, gone := range []string{"review", "house"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("a namespaced pack's skill was also written at %s (%v)", gone, err)
		}
	}
	if got.Taken["house-review"] != "house" || len(got.Taken) != 1 {
		t.Errorf("Taken = %v, want only house-review", got.Taken)
	}
}

// Two packs, one unnamespaced name: refused before anything is written, with the host's message.
func TestComposeIntoRefusesACollisionBeforeWriting(t *testing.T) {
	dir := t.TempDir()
	d := Destination{Dir: "~/.claude/skills", Layers: []Layer{
		{Pack: "shared", Sources: []string{intoSource(t, "dup", "only-shared")}},
		{Pack: "local", Sources: []string{intoSource(t, "dup")}},
	}}
	_, err := ComposeInto(d, dir)
	if err == nil {
		t.Fatal("ComposeInto composed two packs claiming one unnamespaced name")
	}
	for _, want := range []string{`deliver the name "dup"`, "pack shared", "pack local",
		"bare = false"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q:\n%v", want, err)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a refused composition wrote %d entries", len(entries))
	}
}

// A reserved child is never composed, whichever layer carries it — the adopted sync root of
// docs/reference/pack-system.md's "a home an earlier apply already took from", sitting in a local pack — and the withholding is a Result
// the caller can say, not a silence.
func TestComposeIntoWithholdsAReservedChild(t *testing.T) {
	dir := t.TempDir()
	d := Destination{Dir: "~/.claude/skills", Reserved: []string{"synced"},
		ReservedNotes: map[string]string{"synced": "skills Claude Code syncs"},
		Layers:        []Layer{{Pack: "local", Sources: []string{intoSource(t, "synced", "mine")}}}}
	got, err := ComposeInto(d, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "synced")); !os.IsNotExist(err) {
		t.Errorf("the reserved child was composed (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mine", "SKILL.md")); err != nil {
		t.Errorf("the fence cost an ordinary skill: %v", err)
	}
	if _, taken := got.Taken["synced"]; taken {
		t.Errorf("Taken still names the withheld child: %v", got.Taken)
	}
	var said bool
	for _, r := range got.Results {
		if r.Action == ActionReserved && r.Name == "synced" &&
			strings.Contains(r.Detail, "pack local") && strings.Contains(r.Detail, "skills Claude Code syncs") {
			said = true
		}
	}
	if !said {
		t.Errorf("no reserved Result names the pack and the tree: %+v", got.Results)
	}
}
