package packsrc

import (
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
	tree := filepath.Join(store.Dir, "trees", commit)
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
