package prune

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// TestPruneLegacyBuildRoots: the legacy nix-build-root* staging mechanism is
// gone (the image now bakes the flake bundle; nothing binds these dirs), so the
// sweep is an unconditional one-shot cleanup — no liveness gate. An old orphan
// of either prefix is reclaimed; a dir within the age grace floor (mid-upgrade
// window) is spared; dry-run reports without touching disk.
func TestPruneLegacyBuildRoots(t *testing.T) {
	mk := func(name string) (string, string) {
		gs := t.TempDir()
		old := filepath.Join(gs, name)
		must(t, os.MkdirAll(old, 0o755))
		must(t, os.WriteFile(filepath.Join(old, "f"), []byte("x"), 0o644))
		// Backdate mtime well past any grace floor.
		past := time.Now().Add(-48 * time.Hour)
		must(t, os.Chtimes(old, past, past))
		return gs, old
	}
	now := time.Now()

	// (1) Old ".old.*" generation reclaimed unconditionally (no liveness gate).
	gs, old := mk("nix-build-root.old.123")
	_, dirs := PruneLegacyBuildRoots(gs, time.Hour, true, now)
	if dirs != 1 {
		t.Errorf("old orphan swept %d dirs, want 1", dirs)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("old orphan should be reclaimed")
	}

	// (2) The in-flight "nix-build-tmp-*" prefix is also covered.
	gs, old = mk("nix-build-tmp-abc123")
	_, dirs = PruneLegacyBuildRoots(gs, time.Hour, true, now)
	if dirs != 1 {
		t.Errorf("tmp orphan swept %d dirs, want 1", dirs)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("tmp orphan should be reclaimed")
	}

	// (3) Dry-run reports the dir but leaves it on disk.
	gs, old = mk("nix-build-root.old.777")
	_, dirs = PruneLegacyBuildRoots(gs, time.Hour, false, now)
	if dirs != 1 {
		t.Errorf("dry-run reported %d dirs, want 1", dirs)
	}
	if _, err := os.Stat(old); err != nil {
		t.Error("dry-run must not delete the orphan")
	}

	// (4) Too-recent (within grace) -> spared (mid-upgrade window).
	gs = t.TempDir()
	recent := filepath.Join(gs, "nix-build-root.old.999")
	must(t, os.MkdirAll(recent, 0o755))
	_, dirs = PruneLegacyBuildRoots(gs, time.Hour, true, now)
	if dirs != 0 {
		t.Errorf("recent orphan swept %d dirs, want 0 (grace floor)", dirs)
	}
}

// TestDiskUsageReportRelocated: a relocated subdir is sized at its real target
// and reported separately — never folded into GlobalStorage/Total, because
// those bytes are on another filesystem and a prune here cannot free them. The
// host-side stub keeps its own (0 B) row in CacheBreakdown: that IS what this
// filesystem holds.
func TestDiskUsageReportRelocated(t *testing.T) {
	gs := t.TempDir()
	target := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(gs, "cache", "uv"), 0o755))
	must(t, os.WriteFile(filepath.Join(gs, "cache", "uv", "wheel"), make([]byte, 1024), 0o644))
	// The empty bind mountpoint the relocation lands on.
	must(t, os.MkdirAll(filepath.Join(gs, "cache", "huggingface"), 0o755))
	must(t, os.WriteFile(filepath.Join(target, "model.safetensors"), make([]byte, 8192), 0o644))

	rep := DiskUsageReport(nil, gs, map[string]string{"huggingface": target})
	if rep.GlobalStorage != 1024 || rep.Total != 1024 {
		t.Errorf("GlobalStorage=%d Total=%d, want 1024/1024 — the relocated 8192 B must stay out",
			rep.GlobalStorage, rep.Total)
	}
	if got := rep.CacheBreakdown["huggingface"]; got != 0 {
		t.Errorf("stub row = %d B, want 0 (the honest host-side size)", got)
	}
	if len(rep.CacheRelocated) != 1 {
		t.Fatalf("CacheRelocated = %v, want 1 entry", rep.CacheRelocated)
	}
	r := rep.CacheRelocated[0]
	if r.Subdir != "huggingface" || r.Target != target || r.Bytes != 8192 {
		t.Errorf("relocated entry = %+v, want {huggingface %s 8192 …}", r, target)
	}
	if r.Filesystem == "" || !strings.HasPrefix(target, r.Filesystem) {
		t.Errorf("Filesystem = %q, want a mount point that is an ancestor of %q", r.Filesystem, target)
	}

	// No relocations: byte-identical to the pre-feature report.
	if rep := DiskUsageReport(nil, gs, nil); rep.CacheRelocated != nil || rep.Total != 1024 {
		t.Errorf("nil relocations = %+v, want no CacheRelocated and Total=1024", rep)
	}
}

// TestDiskUsageReportRelocatedMissingTarget: a configured-but-missing target is
// reported at 0 B rather than dropped — silently omitting it would read as
// "nothing is relocated", the exact blindness this accounting exists to fix.
func TestDiskUsageReportRelocatedMissingTarget(t *testing.T) {
	gs := t.TempDir()
	missing := filepath.Join(t.TempDir(), "never-created")
	rep := DiskUsageReport(nil, gs, map[string]string{"huggingface": missing})
	if len(rep.CacheRelocated) != 1 || rep.CacheRelocated[0].Bytes != 0 {
		t.Fatalf("CacheRelocated = %+v, want one 0 B entry", rep.CacheRelocated)
	}
	if fs := rep.CacheRelocated[0].Filesystem; fs != "" {
		t.Errorf("Filesystem = %q, want \"\" for an unstat-able target", fs)
	}
}

// TestMountPointOfClimbs pins the st_dev climb directly. The report-level
// assertions can only say "Filesystem is an ancestor of the target", which "/"
// and the target itself both satisfy — so a mountPointOf that never climbed, or
// always returned the root, would pass them while destroying the one signal the
// relocated section exists to give ("is this really on another device?").
func TestMountPointOfClimbs(t *testing.T) {
	// t.TempDir() is a fresh MkdirTemp under os.TempDir(), so it is never itself
	// a mount point: mountPointOf must climb past it to the enclosing filesystem.
	target := t.TempDir()
	if got := mountPointOf(target); got == target {
		t.Errorf("mountPointOf(%q) = %q; a fresh temp dir is never its own mount point", target, got)
	}
	// Termination: filepath.Dir("/") == "/", so the loop must stop at the root.
	if got := mountPointOf("/"); got != "/" {
		t.Errorf("mountPointOf(\"/\") = %q, want \"/\"", got)
	}
}

// TestPurgeCacheByAgeFollowsRelocation: a relocated subdir is purged at its
// real host target, NOT at the empty stub under cacheRoot. Without this the
// heavy purge reports a successful 0 B sweep of huggingface while the GiB it
// was aimed at sit untouched on the other filesystem.
func TestPurgeCacheByAgeFollowsRelocation(t *testing.T) {
	cacheRoot := t.TempDir()
	target := t.TempDir()
	now := time.Now()
	old := now.Add(-60 * 24 * time.Hour)

	// The host-side stub podman mounts over. It is normally empty; give it a
	// stale file so a walk of the WRONG path would be visibly non-zero.
	stub := filepath.Join(cacheRoot, "huggingface")
	must(t, os.MkdirAll(stub, 0o755))
	stubFile := filepath.Join(stub, "leftover.bin")
	must(t, os.WriteFile(stubFile, make([]byte, 512), 0o644))
	must(t, os.Chtimes(stubFile, old, old))

	// The real bytes, on the relocation target.
	realFile := filepath.Join(target, "model.safetensors")
	must(t, os.WriteFile(realFile, make([]byte, 4096), 0o644))
	must(t, os.Chtimes(realFile, old, old))
	fresh := filepath.Join(target, "recent.bin")
	must(t, os.WriteFile(fresh, make([]byte, 32), 0o644))

	relocations := map[string]string{"huggingface": target}
	bytes, files := PurgeCacheByAge(cacheRoot, []string{"huggingface"}, relocations, 30, true, now)
	if bytes != 4096 || files != 1 {
		t.Errorf("purge = (%d bytes, %d files), want (4096, 1) — the target's stale file only", bytes, files)
	}
	if _, err := os.Stat(realFile); !os.IsNotExist(err) {
		t.Error("stale file on the relocation target should be purged")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("in-cutoff file on the relocation target must be kept")
	}
	if _, err := os.Stat(stubFile); err != nil {
		t.Error("the stub under cacheRoot must not be walked when the subdir is relocated")
	}

	// Same tree, no relocation map: the stub is what gets walked (the
	// pre-feature behavior, unchanged).
	bytes, files = PurgeCacheByAge(cacheRoot, []string{"huggingface"}, nil, 30, false, now)
	if bytes != 512 || files != 1 {
		t.Errorf("unrelocated purge = (%d bytes, %d files), want (512, 1)", bytes, files)
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
