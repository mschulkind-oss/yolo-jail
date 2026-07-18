package prune

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// TestHardlinkDedupRealFS: duplicate files get hardlinked (share an inode)
// after HardlinkDuplicateFiles(apply=true); unique-size files are untouched;
// the original is NEVER unlinked mid-op (the .yolo-dedup-tmp discipline —
// asserted by no leftover tmp + content preserved).
func TestHardlinkDedupRealFS(t *testing.T) {
	dir := t.TempDir()
	// Two identical files (same size + content) + one unique.
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	uniq := filepath.Join(dir, "u")
	must(t, os.WriteFile(a, []byte("hello world dup"), 0o644))
	must(t, os.WriteFile(b, []byte("hello world dup"), 0o644))
	must(t, os.WriteFile(uniq, []byte("different length content here"), 0o644))

	entries := []Entry{
		{Path: a, Size: 15}, {Path: b, Size: 15}, {Path: uniq, Size: 29},
	}
	// Dry run: reports 1 link, mutates nothing.
	saved, links := HardlinkDuplicateFiles(entries, false)
	if links != 1 || saved != 15 {
		t.Errorf("dry-run = (%d bytes, %d links), want (15, 1)", saved, links)
	}
	if sameInode(t, a, b) {
		t.Error("dry-run must not actually link")
	}

	// Apply: a and b share an inode; content intact; no tmp leftover.
	saved, links = HardlinkDuplicateFiles(entries, true)
	if links != 1 || saved != 15 {
		t.Errorf("apply = (%d bytes, %d links), want (15, 1)", saved, links)
	}
	if !sameInode(t, a, b) {
		t.Error("a and b should share an inode after apply")
	}
	if got, _ := os.ReadFile(b); string(got) != "hello world dup" {
		t.Errorf("b content corrupted: %q", got)
	}
	if _, err := os.Stat(b + ".yolo-dedup-tmp"); !os.IsNotExist(err) {
		t.Error("leftover .yolo-dedup-tmp — atomic replace incomplete")
	}
	// Idempotent: a second run makes no new links (already same inode).
	_, links = HardlinkDuplicateFiles(entries, true)
	if links != 0 {
		t.Errorf("second run made %d links, want 0 (already linked)", links)
	}
}

func TestOldImagesLexicalSort(t *testing.T) {
	imgs := []ImageEntry{
		{"id1", "2026-07-10 09:00:00 +0000 UTC"},
		{"id2", "2026-07-18 09:00:00 +0000 UTC"}, // newest
		{"id3", "2026-07-01 09:00:00 +0000 UTC"}, // oldest
	}
	// keep=1 -> remove all but the newest (id2). Newest-first: id2, id1, id3.
	got := OldImagesToRemove(imgs, 1)
	if !reflect.DeepEqual(got, []string{"id1", "id3"}) {
		t.Errorf("keep=1 remove = %v, want [id1 id3]", got)
	}
	// keep >= len -> remove nothing.
	if got := OldImagesToRemove(imgs, 5); len(got) != 0 {
		t.Errorf("keep=5 remove = %v, want []", got)
	}
	// keep=0 -> remove all.
	if got := OldImagesToRemove(imgs, 0); len(got) != 3 {
		t.Errorf("keep=0 remove = %v, want 3", got)
	}
}

// TestBuildRootSweepTriState: liveness UNKNOWN (Known=false) must delete
// NOTHING even for an old orphan; known-empty deletes the old orphan; a
// referenced orphan is spared.
func TestBuildRootSweepTriState(t *testing.T) {
	mk := func() (string, string) {
		gs := t.TempDir()
		old := filepath.Join(gs, "nix-build-root.old.123")
		must(t, os.MkdirAll(old, 0o755))
		must(t, os.WriteFile(filepath.Join(old, "f"), []byte("x"), 0o644))
		// Backdate mtime well past any grace floor.
		past := time.Now().Add(-48 * time.Hour)
		must(t, os.Chtimes(old, past, past))
		return gs, old
	}
	now := time.Now()

	// (1) Liveness UNKNOWN -> decline; orphan survives.
	gs, old := mk()
	_, dirs := PruneOrphanBuildRoots(gs, ReferencedSet{Known: false}, time.Hour, true, now)
	if dirs != 0 {
		t.Errorf("unknown-liveness swept %d dirs, want 0 (fail-safe)", dirs)
	}
	if _, err := os.Stat(old); err != nil {
		t.Error("orphan deleted under unknown liveness — tri-state violated")
	}

	// (2) Known-empty -> old orphan reclaimed.
	gs, old = mk()
	_, dirs = PruneOrphanBuildRoots(gs, ReferencedSet{Known: true, Paths: map[string]struct{}{}}, time.Hour, true, now)
	if dirs != 1 {
		t.Errorf("known-empty swept %d dirs, want 1", dirs)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("old orphan should be reclaimed when liveness known + empty")
	}

	// (3) Referenced -> spared.
	gs, old = mk()
	ref := ReferencedSet{Known: true, Paths: map[string]struct{}{old: {}}}
	_, dirs = PruneOrphanBuildRoots(gs, ref, time.Hour, true, now)
	if dirs != 0 {
		t.Errorf("referenced orphan swept %d dirs, want 0", dirs)
	}
	if _, err := os.Stat(old); err != nil {
		t.Error("referenced orphan must be spared")
	}

	// (4) Too-recent (within grace) -> spared even when known+unreferenced.
	gs = t.TempDir()
	recent := filepath.Join(gs, "nix-build-root.old.999")
	must(t, os.MkdirAll(recent, 0o755))
	_, dirs = PruneOrphanBuildRoots(gs, ReferencedSet{Known: true, Paths: map[string]struct{}{}}, time.Hour, true, now)
	if dirs != 0 {
		t.Errorf("recent orphan swept %d dirs, want 0 (grace floor)", dirs)
	}
}

func TestWalkSkipsSymlinksAndEmpty(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "real"), []byte("data"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "empty"), nil, 0o644))
	must(t, os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, "link")))
	entries := WalkDedupTree(dir)
	if len(entries) != 1 || filepath.Base(entries[0].Path) != "real" {
		t.Errorf("walk yielded %v, want only 'real' (skip empty + symlink)", entries)
	}
}

func sameInode(t *testing.T, a, b string) bool {
	t.Helper()
	ai, ad, ok1 := inode(a)
	bi, bd, ok2 := inode(b)
	return ok1 && ok2 && ai == bi && ad == bd
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
