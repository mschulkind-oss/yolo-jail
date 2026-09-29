package packsrc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// treesIn lists the checked-out trees in a store, so a test can say "nothing was checked out".
func treesIn(t *testing.T, store *Store) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(store.Dir, "trees"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestResolveExistingChecksNothingOut: ResolveExisting answers from a tree already in the store
// and never makes one. A fetched pack whose tree is missing, or was left incomplete, is an
// error with the store left exactly as it was, where Resolve would RemoveAll and check out.
func TestResolveExistingChecksNothingOut(t *testing.T) {
	repo := gitRepo(t, map[string]string{"sub/pack.json": `{"name":"p"}`})
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	a, err := Parse("git+file://" + repo + "//sub?ref=main")
	if err != nil {
		t.Skipf("grammar does not accept a local git transport: %v", err)
	}

	if _, err := store.ResolveExisting(a, "p"); err == nil || !strings.Contains(err.Error(), "pack install") {
		t.Errorf("never fetched: err = %v, want the fetch command named", err)
	}

	commit, err := store.Sync(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveExisting(a, "p"); err == nil || !strings.Contains(err.Error(), "not checked out") {
		t.Errorf("fetched, no tree: err = %v, want a not-checked-out error", err)
	}
	if trees := treesIn(t, store); len(trees) != 0 {
		t.Fatalf("ResolveExisting checked out %v", trees)
	}

	want, err := store.Resolve(a, "p")
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.ResolveExisting(a, "p")
	if err != nil {
		t.Fatalf("with the tree present: %v", err)
	}
	if got.Root != want.Root || got.Commit != commit || filepath.Base(got.Root) != "sub" {
		t.Errorf("ResolveExisting = %+v, want Resolve's %+v, the subdirectory of commit %s", got, want, commit)
	}

	// An interrupted checkout: content present, marker missing. Left alone, not re-done.
	tree := store.treeDir(a, commit)
	if err := os.Remove(filepath.Join(tree, treeCompleteMarker)); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(tree, "sub", "pack.json")
	if err := os.WriteFile(partial, []byte("PARTIAL"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveExisting(a, "p"); err == nil {
		t.Error("an incomplete tree resolved")
	}
	if b, _ := os.ReadFile(partial); string(b) != "PARTIAL" {
		t.Errorf("ResolveExisting rewrote the incomplete tree: %q", b)
	}
	if _, err := os.Stat(filepath.Join(tree, treeCompleteMarker)); err == nil {
		t.Error("ResolveExisting completed the tree")
	}
}

// A local pack and a delivered tree resolve as Resolve resolves them: neither writes.
func TestResolveExistingLocalAndStaged(t *testing.T) {
	pack := t.TempDir()
	store := &Store{Dir: t.TempDir(), Git: "/nonexistent/git", Getenv: noStagedTree}
	a, err := Parse("file://" + pack)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := store.ResolveExisting(a, "local"); err != nil || res.Root != pack {
		t.Errorf("local pack: %+v, %v, want root %s", res, err, pack)
	}

	staged, getenv := stagedTree(t, "p")
	store = &Store{Dir: t.TempDir(), Getenv: getenv}
	a, err = Parse("git+https://example.invalid/o/r//p?ref=main")
	if err != nil {
		t.Fatal(err)
	}
	if res, err := store.ResolveExisting(a, "p"); err != nil || res.Root != staged {
		t.Errorf("delivered tree: %+v, %v, want root %s", res, err, staged)
	}
}

// legacyWholeTree writes the tree a yolo from before subdirectory checkouts left for every git
// pack: the WHOLE commit at trees/<commit>, checked out by the command it ran, completion
// marker last. It returns the tree.
func legacyWholeTree(t *testing.T, store *Store, a Addr, commit string) string {
	t.Helper()
	tree := filepath.Join(store.Dir, "trees", commit)
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, store.mirrorPath(a.Repo), "--work-tree="+tree, "checkout", "--force", commit, "--", ".")
	if err := os.WriteFile(filepath.Join(tree, treeCompleteMarker), []byte(commit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return tree
}

// AFTER AN UPGRADE, A READ-ONLY RESOLUTION STILL READS THE WHOLE-COMMIT TREE AN EARLIER YOLO
// LEFT. A subdirectory pack's tree is trees/<commit>-<key> now, which nothing makes until the
// next launch or `yolo pack install`; until then `yolo check`, the agent footer, `yolo pack
// explain` and validation read through ResolveExisting, which writes nothing, and would each
// have lost the pack for that one launch. The earlier tree holds the same subdirectory of the
// same commit, so it answers, through treeResolved's walk. It stands in for nothing else: the
// launch still checks the subdirectory out on its own, after which that tree answers.
func TestResolveExistingReadsAWholeCommitTreeAnEarlierYoloLeft(t *testing.T) {
	repo := gitRepo(t, map[string]string{"sub/pack.json": `{"name":"p"}`, "other/f.txt": "a sibling\n"})
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	a := mustParse(t, "git+file://"+repo+"//sub?ref=main")
	commit, err := store.Sync(a)
	if err != nil {
		t.Fatal(err)
	}
	whole := legacyWholeTree(t, store, a, commit)

	res, err := store.ResolveExisting(a, "p")
	if err != nil {
		t.Fatalf("with an earlier yolo's whole-commit tree in the store: %v", err)
	}
	if res.Root != filepath.Join(whole, "sub") || res.Commit != commit {
		t.Errorf("ResolveExisting = %+v, want the subdirectory of %s", res, whole)
	}
	if trees := treesIn(t, store); len(trees) != 1 || trees[0] != commit {
		t.Errorf("ResolveExisting wrote trees: %v", trees)
	}

	launched, err := store.Resolve(a, "p")
	if err != nil {
		t.Fatal(err)
	}
	if own := filepath.Join(store.treeDir(a, commit), "sub"); launched.Root != own {
		t.Errorf("the launch resolved %s, want the subdirectory checked out on its own at %s", launched.Root, own)
	}
	if res, err := store.ResolveExisting(a, "p"); err != nil || res.Root != launched.Root {
		t.Errorf("after the launch's checkout, ResolveExisting = %+v, %v; want %s", res, err, launched.Root)
	}
}

// The earlier tree is held to what any tree is: complete, and no link on the way to the pack
// root, whatever git's trees say.
func TestResolveExistingHoldsAnEarlierTreeToTheSameLine(t *testing.T) {
	repo := gitRepo(t, map[string]string{"sub/pack.json": `{"name":"p"}`})
	store := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	a := mustParse(t, "git+file://"+repo+"//sub?ref=main")
	commit, err := store.Sync(a)
	if err != nil {
		t.Fatal(err)
	}
	whole := legacyWholeTree(t, store, a, commit)

	if err := os.Remove(filepath.Join(whole, treeCompleteMarker)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveExisting(a, "p"); !errors.Is(err, ErrNotCheckedOut) {
		t.Errorf("an incomplete earlier tree: err = %v, want ErrNotCheckedOut", err)
	}

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "pack.json"), []byte(`{"name":"elsewhere"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(whole, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(whole, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(whole, treeCompleteMarker), []byte(commit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err := store.ResolveExisting(a, "p"); err == nil || !strings.Contains(err.Error(), "passes through a symlink (sub)") {
		t.Errorf("an earlier tree whose subdirectory is a link on disk = %+v, %v; want it refused", res, err)
	}
}
