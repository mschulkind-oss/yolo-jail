package run

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// currentimage_test.go covers WHAT the launch records; currentimagecallsite_test.go
// covers THAT it records it, which is the half a unit test here cannot reach.

// recordOpts is a launch with an isolated HOME (so paths.BuildDir() is a temp
// tree) and a workspace that exists, which is what CurrentImageTags requires
// before it will honour a pointer.
func recordOpts(t *testing.T) (*Options, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	o := &Options{Workspace: ws}
	fillDefaults(o)
	o.Now = func() time.Time { return time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC) }
	return o, ws
}

// TestRecordCurrentImageMakesThisLaunchsImageTheRetentionEvidence is the
// end-to-end of OQ-LS3's launch half: after a successful load, THIS workspace's
// image is what the reaper keeps — and it is keyed the way the reaper reads it,
// through prune.CurrentImageTags rather than through a second spelling here.
func TestRecordCurrentImageMakesThisLaunchsImageTheRetentionEvidence(t *testing.T) {
	o, ws := recordOpts(t)
	const storePath = "/nix/store/aaaa-stream-yolo-jail"
	cname := "yolo-ws-deadbeef"

	o.recordCurrentImage(image.LoadResult{OK: true, StorePath: storePath,
		Ref: image.JailImageRef("podman", storePath)}, cname)

	ptrs, known := prune.ReadCurrentImagePointers(paths.BuildDir())
	if !known {
		t.Fatal("no pointer directory after a successful launch — the reaper would decline for ever")
	}
	if len(ptrs) != 1 {
		t.Fatalf("pointers = %v, want exactly one (one per workspace)", ptrs)
	}
	if ptrs[0].Container != cname || ptrs[0].Workspace != ws || ptrs[0].StorePath != storePath {
		t.Errorf("pointer = %+v, want container=%q workspace=%q storePath=%q",
			ptrs[0], cname, ws, storePath)
	}
	tags, tagsKnown := prune.CurrentImageTags(paths.BuildDir())
	if !tagsKnown {
		t.Fatal("the recorded pointer is not honoured as retention evidence")
	}
	if _, ok := tags[image.ImageStoreKey(storePath)]; !ok {
		t.Errorf("this launch's own image is not protected: %v", tags)
	}
}

// TestRecordCurrentImageRecordsNothingForADegradedLaunch: AutoLoadImage's
// SkipBuild / opted-past-build-failure fallback returns OK with NO store path —
// it runs whatever the flake's legacy `latest` tag names, which
// prune.CurrentImageTags protects unconditionally for exactly that branch. A
// pointer here would have to name something, and there is nothing true to name.
func TestRecordCurrentImageRecordsNothingForADegradedLaunch(t *testing.T) {
	o, _ := recordOpts(t)
	o.recordCurrentImage(image.LoadResult{OK: true, Ref: paths.JailImage}, "yolo-ws-deadbeef")

	if ptrs, known := prune.ReadCurrentImagePointers(paths.BuildDir()); known || len(ptrs) != 0 {
		t.Errorf("a degraded launch wrote %v (dirKnown=%v); it has no store path to point at",
			ptrs, known)
	}
	// And the legacy tag still covers it, so the degraded launch's image is not
	// left unprotected by the absence.
	tags, _ := prune.CurrentImageTags(paths.BuildDir())
	if _, ok := tags["latest"]; !ok {
		t.Errorf("the legacy tag is not protected: %v", tags)
	}
}

// TestRecordCurrentImageSurvivesAnUnwritablePointerDir pins the failure
// disposition, which is the only thing that keeps this off the launch's critical
// path: a pointer that cannot be written costs ONE image re-stream later (the
// jail is protected by `podman ps` while it runs, and its closure by OQ-LS1's
// week-long GC-root policy), so it must never fail or delay a launch. It is
// still recorded — in the housekeeping log, never on a terminal that by then may
// belong to the jail (the OQ-LS2 correction of 2026-09-09).
func TestRecordCurrentImageSurvivesAnUnwritablePointerDir(t *testing.T) {
	o, ws := recordOpts(t)
	// A FILE where the pointer directory has to go: MkdirAll fails with ENOTDIR,
	// which is the shape a real failure takes (a full disk, a read-only store).
	if err := os.MkdirAll(paths.BuildDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prune.CurrentImagesDir(paths.BuildDir()), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	o.recordCurrentImage(image.LoadResult{OK: true, StorePath: "/nix/store/aaaa-img"}, "yolo-ws-deadbeef")

	note, err := os.ReadFile(filepath.Join(paths.WorkspaceStateDir(ws), "housekeeping.log"))
	if err != nil {
		t.Fatalf("a failed pointer write said nothing anywhere: %v — silence is the shape of "+
			"defect this whole effort started from", err)
	}
	if !strings.Contains(string(note), "current image") {
		t.Errorf("housekeeping.log = %q, want it to name what could not be recorded", note)
	}
}

// TestRecordCurrentImageTakesTheHousekeepingLock pins the SERIALISATION, which
// is invisible in the write's result: without it this write can land in the
// middle of another launch's sweep, which has already read the pointer set and
// is deciding what to remove (OQ-BF5's window, one file over).
//
// It is measured rather than asserted from the shape of the code: the test HOLDS
// the machine-wide lock, and the record must not land until it is released.
// flock is per open-file-description, so a second fd in this same process blocks
// exactly as another launch would. DELETE THE LOCK AND THIS FAILS — the pointer
// appears while the lock is held.
func TestRecordCurrentImageTakesTheHousekeepingLock(t *testing.T) {
	o, _ := recordOpts(t)
	// Host-side: the lock only exists there. In-jail a nested launch reaps only
	// its own podman, so there is nothing to serialise against and the lock file
	// is not even on a shared filesystem (lockHousekeepingFn returns nil) — the
	// branch every test in this package runs under by default, pinned below.
	t.Setenv("YOLO_VERSION", "")
	if o.inJail() {
		t.Fatal("fixture failed to put the launch on the host side")
	}

	path := HousekeepingLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	held, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("could not take the lock for the test: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		o.recordCurrentImage(image.LoadResult{OK: true, StorePath: "/nix/store/aaaa-img"},
			"yolo-ws-deadbeef")
	}()

	// While the lock is held nothing may be recorded. A record here means the
	// write is not serialised against a sweep at all.
	time.Sleep(100 * time.Millisecond)
	if ptrs, _ := prune.ReadCurrentImagePointers(paths.BuildDir()); len(ptrs) != 0 {
		t.Errorf("recorded %v while another holder had the machine-wide lock — a sweep "+
			"reading the pointer set can now be interleaved with this write", ptrs)
	}

	_ = syscall.Flock(int(held.Fd()), syscall.LOCK_UN)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the record never completed after the lock was released — it must BLOCK " +
			"(a launch cannot skip its own evidence), not skip like the slot does")
	}
	if ptrs, _ := prune.ReadCurrentImagePointers(paths.BuildDir()); len(ptrs) != 1 {
		t.Errorf("pointers = %v, want one once the lock is free — taking the lock must "+
			"never become skipping the work", ptrs)
	}
}

// TestRecordCurrentImageNeedsNoLockInJail is the carve-out, stated so a future
// reader does not "fix" it: a nested jail's reaper only ever touches that jail's
// own podman and its own build dir, so there is no other launch to serialise
// against — and HousekeepingLockPath is not on a filesystem shared with the host
// anyway.
func TestRecordCurrentImageNeedsNoLockInJail(t *testing.T) {
	o, _ := recordOpts(t)
	t.Setenv("YOLO_VERSION", "test")
	if !o.inJail() {
		t.Fatal("fixture failed to put the launch in a jail")
	}
	if o.lockHousekeepingFn() != nil {
		t.Error("in-jail must have no host lock to take")
	}
	o.recordCurrentImage(image.LoadResult{OK: true, StorePath: "/nix/store/aaaa-img"},
		"yolo-ws-deadbeef")
	if ptrs, _ := prune.ReadCurrentImagePointers(paths.BuildDir()); len(ptrs) != 1 {
		t.Errorf("pointers = %v, want one — a nested jail records its own evidence too, "+
			"or its own reaper declines for ever", ptrs)
	}
}
