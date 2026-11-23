package packsrc

// workingtree_test.go pins the WORKING-TREE STAGING of a `local` pack (docs/design/local-pack-clones.md §6.4-§6.5,
// LPC-D12..LPC-D14, slice 6) over real temp repositories: the file set, what never reaches a jail, a link that
// leaves the pack, an edit reaching the next launch, the lockfile, and a launch record that cannot be written.
// Each reads the decision from a real Store.Refresh, so a refresh that stops leaving the record fails them all.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stageTree stages the pack "tillr" from the record the last launch left, into a fresh directory.
func (f *localCopyFixture) stageTree(t *testing.T, subdir string) (*WorkingTree, string) {
	t.Helper()
	a, err := Parse(f.source)
	if err != nil {
		t.Fatal(err)
	}
	return f.store.ResolveWorkingTree(WorkingTreeSpec{
		Name: "tillr", Clone: f.clone, Addr: a, Subdir: subdir,
		Dir: func() (string, error) { return t.TempDir(), nil },
	})
}

func mustStage(t *testing.T, f *localCopyFixture, subdir string) *WorkingTree {
	t.Helper()
	wt, fallback := f.stageTree(t, subdir)
	if wt == nil {
		t.Fatalf("no working tree staged (fallback %q): the record says the clone runs", fallback)
	}
	return wt
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestTheWorkingTreeStagesTrackedAndUntrackedNotIgnored(t *testing.T) {
	f := newLocalCopyFixture(t)
	commitFile(t, f.clone, ".gitignore", "out/\n.env\n")
	commitFile(t, f.clone, "gone.md", "gone")
	commitFile(t, f.clone, "keep.md", "keep")
	write(t, filepath.Join(f.clone, "pack.json"), `{"name":"edited"}`) // tracked, uncommitted
	write(t, filepath.Join(f.clone, "new.md"), "new")                  // untracked, not ignored
	write(t, filepath.Join(f.clone, "out", "built.bin"), "x")          // ignored
	write(t, filepath.Join(f.clone, ".env"), "SECRET=1")               // ignored
	if err := os.Remove(filepath.Join(f.clone, "gone.md")); err != nil {
		t.Fatal(err)
	}
	f.launch(t, f.source, f.clone)
	wt := mustStage(t, f, "")
	for rel, body := range map[string]string{"pack.json": `{"name":"edited"}`, "new.md": "new", "keep.md": "keep", ".gitignore": "out/\n.env\n"} {
		got, err := os.ReadFile(filepath.Join(wt.Root, rel))
		if err != nil || string(got) != body {
			t.Errorf("%s = %q, %v, want %q", rel, got, err, body)
		}
	}
	for _, rel := range []string{"gone.md", "out/built.bin", ".env", ".git"} {
		if exists(filepath.Join(wt.Root, rel)) {
			t.Errorf("%s reached the staged tree", rel)
		}
	}
	if wt.Files != 4 || wt.Untracked != 1 {
		t.Errorf("files %d untracked %d, want 4 and 1", wt.Files, wt.Untracked)
	}
}

func TestASubmoduleIsNotStaged(t *testing.T) {
	f := newLocalCopyFixture(t)
	sha := gitIn(t, f.clone, "rev-parse", "HEAD")
	gitIn(t, f.clone, "update-index", "--add", "--cacheinfo", "160000,"+sha+",vendor/sub")
	write(t, filepath.Join(f.clone, "vendor", "sub", "inner.md"), "inner")
	f.launch(t, f.source, f.clone)
	wt := mustStage(t, f, "")
	if exists(filepath.Join(wt.Root, "vendor")) {
		t.Errorf("the submodule's directory was staged")
	}
	if !exists(filepath.Join(wt.Root, "pack.json")) {
		t.Errorf("control: pack.json must stage")
	}
}

func TestALinkThatLeavesThePackIsNotFollowedAndFallsBackNamingIt(t *testing.T) {
	f := newLocalCopyFixture(t)
	secret := filepath.Join(f.root, "secret.txt")
	write(t, secret, "host secret")
	if err := os.Symlink(secret, filepath.Join(f.clone, "leak")); err != nil {
		t.Fatal(err)
	}
	f.launch(t, f.source, f.clone)
	wt, fallback := f.stageTree(t, "")
	if wt != nil {
		t.Fatalf("a pack with an outward link staged: %+v", wt)
	}
	if !strings.Contains(fallback, "leak") || !strings.Contains(fallback, "origin") {
		t.Errorf("fallback %q must name the link and say origin runs", fallback)
	}
}

func TestALinkThatStaysInsideThePackIsCopiedAsALink(t *testing.T) {
	f := newLocalCopyFixture(t)
	write(t, filepath.Join(f.clone, "docs", "a.md"), "a")
	if err := os.Symlink("docs/a.md", filepath.Join(f.clone, "alias.md")); err != nil {
		t.Fatal(err)
	}
	f.launch(t, f.source, f.clone)
	wt := mustStage(t, f, "")
	got, err := os.Readlink(filepath.Join(wt.Root, "alias.md"))
	if err != nil || got != "docs/a.md" {
		t.Errorf("alias.md = %q, %v, want a link to docs/a.md", got, err)
	}
}

func TestAnEditToAnUncommittedFileReachesTheNextLaunch(t *testing.T) {
	f := newLocalCopyFixture(t)
	write(t, filepath.Join(f.clone, "wip.md"), "one")
	f.launch(t, f.source, f.clone)
	if b, _ := os.ReadFile(filepath.Join(mustStage(t, f, "").Root, "wip.md")); string(b) != "one" {
		t.Fatalf("first launch staged %q", b)
	}
	write(t, filepath.Join(f.clone, "wip.md"), "two")
	f.launch(t, f.source, f.clone)
	if b, _ := os.ReadFile(filepath.Join(mustStage(t, f, "").Root, "wip.md")); string(b) != "two" {
		t.Errorf("the next launch staged %q, want the edit", b)
	}
}

func TestAnOriginCopyStagesNoWorkingTree(t *testing.T) {
	f := newLocalCopyFixture(t)
	f.launch(t, f.source, f.clone)
	f.push(t, "theirs.md") // clean and behind: origin runs
	f.launch(t, f.source, f.clone)
	if wt, fb := f.stageTree(t, ""); wt != nil || fb != "" {
		t.Errorf("an origin decision staged %+v, %q", wt, fb)
	}
}

func TestADecisionOfAnotherCloneOrRefIsNotUsed(t *testing.T) {
	f := newLocalCopyFixture(t)
	f.launch(t, f.source, f.clone)
	a, _ := Parse(f.source)
	wt, _ := f.store.ResolveWorkingTree(WorkingTreeSpec{Name: "tillr", Clone: filepath.Join(f.root, "elsewhere"), Addr: a,
		Dir: func() (string, error) { return t.TempDir(), nil }})
	if wt != nil {
		t.Errorf("a record of one clone staged another path")
	}
}

func TestDirtyAndTheFileCountFollowTheSubdir(t *testing.T) {
	f := newLocalCopyFixture(t)
	commitFile(t, f.clone, "pk/pack.json", `{"name":"p"}`)
	commitFile(t, f.clone, "pk/a.md", "a")
	gitIn(t, f.clone, "push", "-q", "origin", "main")
	write(t, filepath.Join(f.clone, "elsewhere.md"), "outside the subdir")
	write(t, filepath.Join(f.clone, "pk", "pack.json"), `{"name":"edited"}`)
	src := f.source + "&path=pk"
	_ = src
	a := f.source + "//pk"
	_ = a
	f.source = "git+file://" + f.bare + "//pk?ref=main"
	o := f.launch(t, f.source, f.clone)
	d := o.Local
	if d.Changed != 1 || d.Untracked != 0 {
		t.Errorf("changed %d untracked %d, want 1 and 0: an edit outside the pack's subdirectory is not the pack's", d.Changed, d.Untracked)
	}
	if !strings.Contains(d.Line, "; 2 files)") {
		t.Errorf("line %q must carry the staged file count, 2 files", d.Line)
	}
	wt := mustStage(t, f, "")
	if exists(filepath.Join(wt.Root, "elsewhere.md")) || !exists(filepath.Join(wt.Root, "a.md")) {
		t.Errorf("the staged tree is not the subdirectory's files")
	}
	if wt.Files != 2 {
		t.Errorf("files = %d, want 2", wt.Files)
	}
}

func TestTheLineCarriesTheFileCount(t *testing.T) {
	f := newLocalCopyFixture(t)
	write(t, filepath.Join(f.clone, "new.md"), "n")
	o := f.launch(t, f.source, f.clone)
	if !strings.Contains(o.Local.Line, "1 untracked") || !strings.Contains(o.Local.Line, "; 2 files)") {
		t.Errorf("line %q lacks the count", o.Local.Line)
	}
}

func lockCommit(t *testing.T, f *localCopyFixture) string {
	t.Helper()
	l, err := LoadLock(f.lock)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := l.Get("tillr")
	return e.Commit
}

func TestTheLockfilePinsOnlyOriginsCopy(t *testing.T) {
	f := newLocalCopyFixture(t)
	f.launch(t, f.source, "") // no clone: origin delivered, pinned
	pinned := lockCommit(t, f)
	if pinned == "" {
		t.Fatal("control: an origin copy must pin")
	}
	f.push(t, "theirs.md")
	write(t, filepath.Join(f.clone, "wip.md"), "wip") // dirty and behind: the working tree runs
	o := f.launch(t, f.source, f.clone)
	if o.Local.Copy != CopyWorkingTree {
		t.Fatalf("copy = %s", o.Local.Copy)
	}
	if got := lockCommit(t, f); got != pinned {
		t.Errorf("a working-tree copy moved the pin %s -> %s", pinned, got)
	}
	if d := o.Disclosure(); d != "" {
		t.Errorf("Disclosure() = %q: the pin did not move, and the local line is the disclosure", d)
	}
	gitIn(t, f.clone, "stash", "-u", "-q") // clean and behind: origin runs again
	f.launch(t, f.source, f.clone)
	if got := lockCommit(t, f); got == pinned {
		t.Errorf("an origin copy did not move the pin")
	}
}

func TestAFirstLaunchOnAWorkingTreeRecordsNoPin(t *testing.T) {
	f := newLocalCopyFixture(t)
	f.launch(t, f.source, f.clone)
	if got := lockCommit(t, f); got != "" {
		t.Errorf("a working tree with no earlier pin recorded %s", got)
	}
}

func TestARecordThatCannotBeWrittenFallsBackToOrigin(t *testing.T) {
	f := newLocalCopyFixture(t)
	f.launch(t, f.source, f.clone)
	rec := f.store.localCopyRecordPath("tillr")
	if err := os.Remove(rec); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(rec, "block"), "x") // a non-empty directory: the rename cannot replace it
	o := f.launch(t, f.source, f.clone)
	if o.Local == nil || o.Local.Copy != CopyOrigin || !o.Local.Warn ||
		!strings.Contains(o.Local.Line, "launch record") || !strings.Contains(o.Local.Line, "origin main") {
		t.Errorf("decision = %+v, want origin, warned, naming the record", o.Local)
	}
	if got := lockCommit(t, f); got == "" {
		t.Errorf("origin runs, so its commit is the pin")
	}
	if wt, _ := f.stageTree(t, ""); wt != nil {
		t.Errorf("a working tree staged although no record says the clone runs")
	}
}
