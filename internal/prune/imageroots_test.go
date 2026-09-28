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
// `age`, so it clears the reaper's retention horizon by default. The reaper reads
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

// TestPruneOrphanImageRootsAgesOutEveryRootNoContainerRuns is OQ-LS1's half of
// the policy: for a root no container is running on, age is the whole test.
//
// It was TestPruneOrphanImageRootsIsAgeOnly, whose third case asserted that a
// live image's root is reaped on age like any other. OQ-LS4 (2026-09-28)
// inverted exactly that case, so it moved to
// TestPruneOrphanImageRootsNeverReapsARunningImagesRoot below rather than being
// deleted; the two age cases stay here unchanged.
func TestPruneOrphanImageRootsAgesOutEveryRootNoContainerRuns(t *testing.T) {
	now := time.Now()

	// (1) Older than the horizon, nothing running on it -> reaped.
	rd := t.TempDir()
	link := mkRoot(t, rd, "aaaa", "/nix/store/orphan-1", 8*24*time.Hour)
	reaped := PruneOrphanImageRoots(rd, map[string]bool{}, true, ImageRootRetention, true, now)
	if len(reaped) != 1 {
		t.Fatalf("root unused for 8 days reaped %d, want 1", len(reaped))
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("a root past the retention horizon must be reclaimed")
	}

	// (2) Inside the horizon -> spared. nix-store --add-root refreshes the link's
	// own mtime even when the target is unchanged, so the mtime is the last fresh
	// launch of the image.
	rd = t.TempDir()
	link = mkRoot(t, rd, "bbbb", "/nix/store/orphan-2", 6*24*time.Hour)
	reaped = PruneOrphanImageRoots(rd, map[string]bool{}, true, ImageRootRetention, true, now)
	if len(reaped) != 0 {
		t.Errorf("root used 6 days ago reaped %d, want 0", len(reaped))
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("a root inside the retention horizon must be spared")
	}
}

// TestPruneOrphanImageRootsNeverReapsARunningImagesRoot is OQ-LS4, ruled
// 2026-09-28: the root of an image a container is running on is held by
// liveness, however old, because on podman/Linux the jail executes from the
// host store that root pins. A jail up for eight days keeps its closure; the
// same root with its jail stopped goes.
func TestPruneOrphanImageRootsNeverReapsARunningImagesRoot(t *testing.T) {
	now := time.Now()
	target := filepath.Join(t.TempDir(), "image.json") // must exist: a dangling root holds nothing
	if err := os.WriteFile(target, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	rd := t.TempDir()
	running := mkRoot(t, rd, "cccccccccccccccc", target, 8*24*time.Hour)
	stopped := mkRoot(t, rd, "dddddddddddddddd", target, 8*24*time.Hour)
	reaped := PruneOrphanImageRoots(rd, map[string]bool{"cccccccccccccccc": true}, true,
		ImageRootRetention, true, now)
	if len(reaped) != 1 || reaped[0] != stopped {
		t.Fatalf("reaped %v, want only the stopped image's root %s", reaped, stopped)
	}
	if _, err := os.Lstat(running); err != nil {
		t.Fatal("a running image's root was reaped on age — the next nix GC deletes the " +
			"closure that jail's /bin/* resolve through (OQ-LS4)")
	}

	// A running image whose root already dangles pins nothing and still goes.
	rd = t.TempDir()
	dangling := mkRoot(t, rd, "eeeeeeeeeeeeeeee", "/nix/store/gone", 8*24*time.Hour)
	reaped = PruneOrphanImageRoots(rd, map[string]bool{"eeeeeeeeeeeeeeee": true}, true,
		ImageRootRetention, true, now)
	if len(reaped) != 1 || reaped[0] != dangling {
		t.Errorf("reaped %v, want the dangling root", reaped)
	}
}

// TestPruneOrphanImageRootsReapsNothingWhenLivenessIsUnknown: P3 applies to this
// pass again. Reaping is the dangerous direction, so an unanswerable runtime (or
// a running jail yolo cannot map) spares every root.
func TestPruneOrphanImageRootsReapsNothingWhenLivenessIsUnknown(t *testing.T) {
	rd := t.TempDir()
	link := mkRoot(t, rd, "ffffffffffffffff", "/nix/store/orphan", 30*24*time.Hour)
	for _, apply := range []bool{false, true} {
		if reaped := PruneOrphanImageRoots(rd, nil, false, ImageRootRetention, apply, time.Now()); len(reaped) != 0 {
			t.Errorf("apply=%v: reaped %v with liveness unknown", apply, reaped)
		}
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("a root was removed with liveness unknown")
	}
}

// TestImageRootRetentionIsAPolicyNotAGraceWindow pins the number against the
// value it replaced. 3600 s was a race guard for a root a launch had just
// created; a week is a horizon over which "will I want this closure again" is
// decided. A change back to hours silently turns the pass into the old guard
// while every other test stays green.
func TestImageRootRetentionIsAPolicyNotAGraceWindow(t *testing.T) {
	if ImageRootRetention < 24*time.Hour {
		t.Fatalf("ImageRootRetention = %s — under a day is a startup grace window, not the "+
			"retention policy OQ-LS1 ruled", ImageRootRetention)
	}
}

// (5) Dry-run reports the reap set but touches nothing.
func TestPruneOrphanImageRootsDryRun(t *testing.T) {
	now := time.Now()
	rd := t.TempDir()
	link := mkRoot(t, rd, "eeee", "/nix/store/orphan-5", 48*time.Hour)
	reaped := PruneOrphanImageRoots(rd, map[string]bool{}, true, time.Hour, false /*apply*/, now)
	if len(reaped) != 1 {
		t.Fatalf("dry-run reaped list = %d, want 1", len(reaped))
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("dry-run must not delete the root")
	}
}

// (6) Missing roots dir (nothing ever rooted) -> empty, no error.
func TestPruneOrphanImageRootsNoDir(t *testing.T) {
	reaped := PruneOrphanImageRoots(filepath.Join(t.TempDir(), "roots"), map[string]bool{}, true, time.Hour, true, time.Now())
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
	reaped := PruneOrphanImageRoots(rd, map[string]bool{}, true, time.Hour, true, time.Now())
	if len(reaped) != 0 {
		t.Errorf("reaped %d, want 0 (stray non-symlink must be left alone)", len(reaped))
	}
	if _, err := os.Stat(stray); err != nil {
		t.Error("stray regular file under roots/ must not be removed")
	}
}
