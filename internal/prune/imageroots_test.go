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

// TestPruneOrphanImageRootsIsAgeOnly is the REWRITE of what was
// TestPruneOrphanImageRootsTriState. That test asserted the two guards OQ-LS1
// removed — a fail-safe liveness gate and a protected set read from the load
// sentinel — so it is rewritten to the ruled behavior rather than repaired
// until green: age is now the whole policy, and a pass with no authority to
// consult cannot have a tri-state.
//
// Kept from the old test: the dangling-root, non-symlink and dry-run cases,
// which are about the reaper's mechanics and are untouched by the ruling.
func TestPruneOrphanImageRootsIsAgeOnly(t *testing.T) {
	now := time.Now()

	// (1) Older than the horizon -> reaped, with no liveness input of any kind.
	rd := t.TempDir()
	link := mkRoot(t, rd, "aaaa", "/nix/store/orphan-1", 8*24*time.Hour)
	reaped := PruneOrphanImageRoots(rd, ImageRootRetention, true, now)
	if len(reaped) != 1 {
		t.Fatalf("root unused for 8 days reaped %d, want 1", len(reaped))
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("a root past the retention horizon must be reclaimed")
	}

	// (2) Inside the horizon -> spared. This is the whole of the policy: the
	// mtime is the last launch that USED the image, because nix-store --add-root
	// refreshes the link's own mtime even when the target is unchanged.
	rd = t.TempDir()
	link = mkRoot(t, rd, "bbbb", "/nix/store/orphan-2", 6*24*time.Hour)
	reaped = PruneOrphanImageRoots(rd, ImageRootRetention, true, now)
	if len(reaped) != 0 {
		t.Errorf("root used 6 days ago reaped %d, want 0", len(reaped))
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("a root inside the retention horizon must be spared")
	}

	// (3) A LIVE image's root is NOT special-cased, and that is the ruling
	// rather than an oversight. Losing it costs a rebuild, never a running jail
	// — which is exactly what does not hold for the install prefix (OQ-BF4), so
	// this assertion is what stops someone "fixing" this by reading liveness
	// back in. A jail up for eight days no longer pins its image's closure.
	rd = t.TempDir()
	link = mkRoot(t, rd, "cccc", "/nix/store/an-image-a-live-jail-runs", 8*24*time.Hour)
	reaped = PruneOrphanImageRoots(rd, ImageRootRetention, true, now)
	if len(reaped) != 1 {
		t.Fatalf("a long-running jail's image root reaped %d, want 1 — age is the whole "+
			"policy (OQ-LS1); if you are re-adding a liveness veto here, read that ruling "+
			"first: liveness is a wrong predictor of future want in both directions", len(reaped))
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Error("no root is exempt from age")
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
	reaped := PruneOrphanImageRoots(rd, time.Hour, false /*apply*/, now)
	if len(reaped) != 1 {
		t.Fatalf("dry-run reaped list = %d, want 1", len(reaped))
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("dry-run must not delete the root")
	}
}

// (6) Missing roots dir (nothing ever rooted) -> empty, no error.
func TestPruneOrphanImageRootsNoDir(t *testing.T) {
	reaped := PruneOrphanImageRoots(filepath.Join(t.TempDir(), "roots"), time.Hour, true, time.Now())
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
	reaped := PruneOrphanImageRoots(rd, time.Hour, true, time.Now())
	if len(reaped) != 0 {
		t.Errorf("reaped %d, want 0 (stray non-symlink must be left alone)", len(reaped))
	}
	if _, err := os.Stat(stray); err != nil {
		t.Error("stray regular file under roots/ must not be removed")
	}
}
