package run

// packprune_test.go pins "the MOUNT is the filter" under ONE PACK TREE PER LAUNCH
// (packtree.go, docs/reference/pack-system.md#oq-pk2): a pack the config no longer selects is
// absent from the next launch's tree, and staging never touches a tree another launch staged.
//
// This is not a tidiness property. The in-jail entrypoint renders every pack it finds under
// YOLO_PACK_ROOT and cannot read the config to learn which ones were selected, so a leftover
// directory in a staged tree is a fully ACTIVE pack — its surfaces render, its hooks run, its
// shims generate. That is how the original bug was found: a deleted test pack kept
// regenerating a broken `fzf` shim across launches, after the user had removed both the pack
// and its config entry. The shared tree needed two prunes to keep that from happening; a fresh
// tree per launch needs none, and these tests are what hold it to that.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// localPackDir writes a minimal local pack (manifest + one marker file) and returns its
// root, for use as a `file://` source.
func localPackDir(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"),
		[]byte(`{"name":"`+name+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "marker.txt"), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// stagingOptions returns Options whose console output is captured.
func stagingOptions(t *testing.T) (*Options, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	return &Options{Workspace: t.TempDir(), Stdout: &out}, &out
}

// packTreesUnder lists the tree directories under cname's pack-tree root.
func packTreesUnder(t *testing.T, cname string) []string {
	t.Helper()
	entries, err := os.ReadDir(paths.PackTreeRoot(cname))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(paths.PackTreeRoot(cname), e.Name()))
		}
	}
	return out
}

// TestADroppedConfiguredPackIsAbsentFromTheNextTree is the defect itself, under the new model:
// removing a USER's pack from `packs` must stop it rendering at the next launch. The next
// launch stages a NEW tree that holds only what it selected, and the earlier launch's tree —
// which a running jail may still bind — is left exactly as it was.
func TestADroppedConfiguredPackIsAbsentFromTheNextTree(t *testing.T) {
	home := packHome(t)
	keep := localPackDir(t, "keeper")
	drop := localPackDir(t, "dropped")
	writeUserPacks(t, home, `["file://`+keep+`", "file://`+drop+`"]`)

	o, _ := stagingOptions(t)
	first, loaded, _, err := o.stagePacks("yolo-test-prune")
	if err != nil {
		t.Fatalf("first launch: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("first launch: want 2 packs staged, got %d", len(loaded))
	}
	for _, slug := range []string{"keeper", "dropped"} {
		if !isDir(filepath.Join(first, slug)) {
			t.Fatalf("first launch did not stage %s", slug)
		}
	}
	before := snapshotTree(t, first, nil)

	writeUserPacks(t, home, `["file://`+keep+`"]`)
	second, loaded, _, err := o.stagePacks("yolo-test-prune")
	if err != nil {
		t.Fatalf("second launch: %v", err)
	}
	if second == first {
		t.Fatalf("the second launch staged into the first launch's tree %s; every launch must "+
			"stage a tree of its own", first)
	}
	if filepath.Dir(second) != paths.PackTreeRoot("yolo-test-prune") {
		t.Fatalf("the tree %s is not under the pack-tree root %s", second, paths.PackTreeRoot("yolo-test-prune"))
	}
	if len(loaded) != 1 || loaded[0].Name != "keeper" {
		t.Fatalf("second launch: want [keeper], got %d packs", len(loaded))
	}
	if _, statErr := os.Stat(filepath.Join(second, "dropped")); !os.IsNotExist(statErr) {
		t.Errorf("the dropped pack is in the next launch's tree (%v) — the entrypoint renders "+
			"every pack under YOLO_PACK_ROOT, so it would still generate shims and run hooks", statErr)
	}
	if body, rerr := os.ReadFile(filepath.Join(second, "keeper", "marker.txt")); rerr != nil ||
		strings.TrimSpace(string(body)) != "keeper" {
		t.Errorf("the KEPT pack's content is not in the next tree: %v / %q", rerr, body)
	}
	if d := diffSnapshots(before, snapshotTree(t, first, nil)); len(d) != 0 {
		t.Errorf("the second launch's staging changed the first launch's tree, which a running "+
			"jail may bind:\n  %s", strings.Join(d, "\n  "))
	}
}

// TestADroppedEmbeddedPackIsAbsentFromTheNextTree is the same rule for an EMBEDDED pack, the job
// the old prune of _official did: the next tree's _official holds only what the next launch
// selected (the needs closure's additions included, which is why claude brings openai-auth and
// wire-bridge along and a bare config brings nothing).
func TestADroppedEmbeddedPackIsAbsentFromTheNextTree(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	o, _ := stagingOptions(t)
	first, _, _, err := o.stagePacks("yolo-test-prune-embedded")
	if err != nil {
		t.Fatalf("first launch: %v", err)
	}
	if !isDir(filepath.Join(first, officialStagingDir, "claude")) {
		t.Fatalf("the first launch staged no claude under %s", first)
	}
	writeUserPacks(t, home, `[]`)
	second, loaded, _, err := o.stagePacks("yolo-test-prune-embedded")
	if err != nil {
		t.Fatalf("second launch: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("an empty config loaded %d packs", len(loaded))
	}
	if entries, _ := os.ReadDir(filepath.Join(second, officialStagingDir)); len(entries) != 0 {
		t.Errorf("the next launch's tree still holds embedded packs nobody selected: %v", entries)
	}
	if !isDir(filepath.Join(first, officialStagingDir, "claude")) {
		t.Error("the next launch removed claude from the first launch's tree, which a running jail may bind")
	}
}

// TestARefusedStagingLeavesNoTreeAndTouchesNoOther: a launch whose staging refuses — here an
// unfetched git pack, fatal because a launch never fetches outside its refresh — takes its own
// partial tree with it, and the tree an earlier launch staged is untouched. Under the shared tree
// this was the constraint that ruled out clear-then-restage (an unreachable git remote is not a
// deactivation signal); a tree per launch meets it by construction.
func TestARefusedStagingLeavesNoTreeAndTouchesNoOther(t *testing.T) {
	home := packHome(t)
	local := localPackDir(t, "acme")
	writeUserPacks(t, home, `["file://`+local+`"]`)
	o, _ := stagingOptions(t)
	const cname = "yolo-test-unresolvable"
	first, _, _, err := o.stagePacks(cname)
	if err != nil {
		t.Fatalf("first launch: %v", err)
	}
	before := snapshotTree(t, first, nil)

	writeUserPacks(t, home, `["file://`+local+`", "git+ssh://example.invalid/org/repo//gone?ref=v1"]`)
	if _, _, _, err := o.stagePacks(cname); err == nil {
		t.Fatal("an unfetched git pack must fail the launch (C5: launch never fetches)")
	}
	if trees := packTreesUnder(t, cname); len(trees) != 1 || trees[0] != first {
		t.Errorf("after a refused staging the pack-tree root holds %v, want only the first "+
			"launch's %s: a refused launch must take its partial tree with it", trees, first)
	}
	if d := diffSnapshots(before, snapshotTree(t, first, nil)); len(d) != 0 {
		t.Errorf("a refused staging changed another launch's tree:\n  %s", strings.Join(d, "\n  "))
	}
}

// TestAStagedTreeRecordsItsPacksInLoadOrder: the tree's own record (packTreeRecordName) names
// every pack it holds, by the name and in the order the launch loaded them, and loadPackTree
// reads the same set back — the names and the order an attach needs to compose what the
// jail's launch composed.
func TestAStagedTreeRecordsItsPacksInLoadOrder(t *testing.T) {
	home := packHome(t)
	local := localPackDir(t, "zeta")
	writeUserPacks(t, home, `["file://`+local+`", "claude"]`)
	o, _ := stagingOptions(t)
	tree, loaded, _, err := o.stagePacks("yolo-test-record")
	if err != nil {
		t.Fatal(err)
	}
	reread, err := loadPackTree(tree)
	if err != nil {
		t.Fatalf("loadPackTree: %v", err)
	}
	var want, got []string
	for _, p := range loaded {
		want = append(want, p.Name+"@"+p.Root)
	}
	for _, p := range reread {
		got = append(got, p.Name+"@"+p.Root)
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the tree reads back as\n  %s\nwant the launch's own load order\n  %s",
			strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	if _, err := os.Stat(filepath.Join(tree, packTreeRecordName)); err != nil {
		t.Errorf("the tree carries no record: %v", err)
	}
}

// TestNoPackNameCollidesWithTheTreeRecord: a pack name may be any string without "/", "\\" or
// ":", so a configured pack named after the tree's record must still stage beside it, and the tree
// must still read back. The record's name is one no slug can spell (packTreeRecordName).
func TestNoPackNameCollidesWithTheTreeRecord(t *testing.T) {
	home := packHome(t)
	for _, name := range []string{packTreeRecordName, ".yolo-pack-tree.json"} {
		dir := localPackDir(t, "collider")
		writePack(t, dir, `{"name":"collider"}`)
		writeUserPacks(t, home, `[{"source":"file://`+dir+`","name":"`+name+`"}]`)
		o, out := stagingOptions(t)
		tree, _, _, err := o.stagePacks("yolo-test-record-name")
		if err != nil {
			t.Fatalf("a pack named %q refused the launch: %v\n%s", name, err, out.String())
		}
		reread, err := loadPackTree(tree)
		if err != nil || len(reread) != 1 || reread[0].Name != name {
			t.Errorf("a pack named %q does not read back from its tree: %v (%d packs)", name, err, len(reread))
		}
	}
}
