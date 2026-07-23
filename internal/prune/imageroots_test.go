package prune

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// mkRoot creates roots/<name> as a symlink to target (target need not exist —
// a dangling root is the reap-me case) and back-dates the SYMLINK's own mtime by
// `age`, so it clears the reaper's grace floor by default. The reaper reads
// os.Lstat().ModTime() (the link's own time), so aging must be no-follow:
// os.Chtimes follows the link, hence unix.Lutimes here. Returns the link path.
func mkRoot(t *testing.T, rootsDir, name, target string, age time.Duration) string {
	t.Helper()
	if err := os.MkdirAll(rootsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(rootsDir, name)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	tv := []unix.Timeval{unix.NsecToTimeval(when.UnixNano()), unix.NsecToTimeval(when.UnixNano())}
	if err := unix.Lutimes(link, tv); err != nil {
		t.Fatal(err)
	}
	return link
}

func TestPruneOrphanImageRootsTriState(t *testing.T) {
	now := time.Now()
	old := 48 * time.Hour

	// (1) Liveness UNKNOWN -> decline; dangling root survives.
	rd := t.TempDir()
	link := mkRoot(t, rd, "aaaa", "/nix/store/gone-1", old)
	reaped := PruneOrphanImageRoots(rd, map[string]struct{}{}, false, time.Hour, true, now)
	if len(reaped) != 0 {
		t.Errorf("unknown-liveness reaped %d roots, want 0 (fail-safe)", len(reaped))
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("root deleted under unknown liveness — tri-state violated")
	}

	// (2) Known + protected -> spared (target is a loaded image path).
	rd = t.TempDir()
	target := "/nix/store/live-image-2"
	link = mkRoot(t, rd, "bbbb", target, old)
	protected := map[string]struct{}{target: {}}
	reaped = PruneOrphanImageRoots(rd, protected, true, time.Hour, true, now)
	if len(reaped) != 0 {
		t.Errorf("protected root reaped %d, want 0", len(reaped))
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("protected root (loaded image) must be spared")
	}

	// (3) Known + unreferenced + dangling + old -> reaped.
	rd = t.TempDir()
	link = mkRoot(t, rd, "cccc", "/nix/store/orphan-3", old)
	reaped = PruneOrphanImageRoots(rd, map[string]struct{}{}, true, time.Hour, true, now)
	if len(reaped) != 1 {
		t.Fatalf("orphan root reaped %d, want 1", len(reaped))
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("orphan root should be reclaimed when known + unreferenced + old")
	}

	// (4) Too-recent (within grace) -> spared even when unreferenced.
	rd = t.TempDir()
	link = mkRoot(t, rd, "dddd", "/nix/store/orphan-4", 0)
	reaped = PruneOrphanImageRoots(rd, map[string]struct{}{}, true, time.Hour, true, now)
	if len(reaped) != 0 {
		t.Errorf("recent root reaped %d, want 0 (grace floor)", len(reaped))
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("recent root must be spared by the grace floor")
	}
}

// (5) Dry-run reports the reap set but touches nothing.
func TestPruneOrphanImageRootsDryRun(t *testing.T) {
	now := time.Now()
	rd := t.TempDir()
	link := mkRoot(t, rd, "eeee", "/nix/store/orphan-5", 48*time.Hour)
	reaped := PruneOrphanImageRoots(rd, map[string]struct{}{}, true, time.Hour, false /*apply*/, now)
	if len(reaped) != 1 {
		t.Fatalf("dry-run reaped list = %d, want 1", len(reaped))
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("dry-run must not delete the root")
	}
}

// (6) Missing roots dir (nothing ever rooted) -> empty, no error.
func TestPruneOrphanImageRootsNoDir(t *testing.T) {
	reaped := PruneOrphanImageRoots(filepath.Join(t.TempDir(), "roots"), map[string]struct{}{}, true, time.Hour, true, time.Now())
	if len(reaped) != 0 {
		t.Errorf("missing roots dir reaped %d, want 0", len(reaped))
	}
}

// (7) A non-symlink stray under roots/ is never touched.
func TestPruneOrphanImageRootsSkipsNonSymlink(t *testing.T) {
	rd := t.TempDir()
	if err := os.MkdirAll(rd, 0o755); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(rd, "README")
	if err := os.WriteFile(stray, []byte("not a root"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(stray, old, old)
	reaped := PruneOrphanImageRoots(rd, map[string]struct{}{}, true, time.Hour, true, time.Now())
	if len(reaped) != 0 {
		t.Errorf("reaped %d, want 0 (stray non-symlink must be left alone)", len(reaped))
	}
	if _, err := os.Stat(stray); err != nil {
		t.Error("stray regular file under roots/ must not be removed")
	}
}
