package image

import (
	"bytes"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// copyStallWindow is how long the first stubbed copy holds the store write open
// when no SECOND copy turns up to relieve it.
//
// THE ASYMMETRY IS THE TEST. A real copy of the measured image takes ~4 minutes;
// the stub stands in for that window, and the only question the test asks is
// whether a second launch can get INTO it. With the lock in place nobody else
// ever does, so this timeout is what ends the first copy and it is paid once per
// run. Without the lock the second copy arrives in microseconds — every step
// between the two inspects is stubbed — closes the relief channel and both
// copies finish immediately, which is why the failing direction is fast and
// deterministic rather than timing-dependent.
const copyStallWindow = 500 * time.Millisecond

// sharedStore is ONE MACHINE'S IMAGE STORE, shared by both concurrent launches.
//
// It has to be its own fixture rather than fakeRuntime: that one is a plain map
// and slices touched from one goroutine, and this test drives it from two. The
// behaviours modelled are the two the lock's correctness rests on — an inspect
// answers for what the store HOLDS, and a copy publishes its ref only when the
// store write COMPLETES. A fake that published the ref on entry would report one
// copy no matter what the code did, because the second launch's re-inspect would
// always find the image.
type sharedStore struct {
	mu      sync.Mutex
	present map[string]bool
	copies  int
	// secondCopy is closed the moment a second copy starts, releasing the first
	// one. See copyStallWindow.
	secondCopy chan struct{}
	closeOnce  sync.Once
}

func newSharedStore() *sharedStore {
	return &sharedStore{present: map[string]bool{}, secondCopy: make(chan struct{})}
}

// run is the AutoLoadOptions.Run seam: inspect answers from the store, tag adds
// a name to an image that is already in it.
func (s *sharedStore) run(argv []string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case len(argv) >= 4 && argv[1] == "image" && argv[2] == "inspect":
		if s.present[argv[3]] {
			return 0, true
		}
		return 1, true
	case len(argv) >= 4 && argv[1] == "tag":
		if !s.present[argv[2]] {
			return 1, true
		}
		s.present[argv[3]] = true
		return 0, true
	}
	return 1, true
}

// layerCopy is the AutoLoadOptions.LayerCopy seam, stalled as described above.
func (s *sharedStore) layerCopy(_, dest string, _ []string) (CopyReport, bool) {
	s.mu.Lock()
	s.copies++
	n := s.copies
	s.mu.Unlock()
	if n >= 2 {
		s.closeOnce.Do(func() { close(s.secondCopy) })
	}
	select {
	case <-s.secondCopy:
	case <-time.After(copyStallWindow):
	}
	ref := strings.TrimPrefix(dest, "containers-storage:")
	s.mu.Lock()
	s.present[ref] = true
	s.mu.Unlock()
	return CopyReport{}, true
}

func (s *sharedStore) copyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.copies
}

// TestConcurrentLoadsOfOneRefCopyOnce is roadmap row 9's acceptance test: two
// launches that want the same image at the same moment must produce ONE copy.
//
// The bug it pins was measured on 2026-09-14 — a reboot launched 11 jails and 5
// of them copied one identical 3.45 GB image simultaneously, ~4 minutes each —
// and it is invisible to layer-aware delivery by construction, because
// containers-storage can only skip a layer it has already committed.
//
// WHAT MAKES IT A TEST OF THE CALL SITE AND NOT OF THE LOCK. LockImageCopy is
// deliberately NOT stubbed, so the flock the production default takes is the one
// under test, and both halves of the change are pinned independently:
//
//   - Delete the lock (keep the re-inspect): the second launch re-inspects while
//     the first is still inside its copy, sees nothing, and copies. Two copies.
//   - Delete the re-inspect (keep the lock): the second launch waits, acquires,
//     and copies an image that is already there. Two copies.
//   - Delete both: two copies.
//
// Only both together give one, which is the number asserted.
func TestConcurrentLoadsOfOneRefCopyOnce(t *testing.T) {
	// The lock path derives from HOME, so this also keeps the machine-wide lock
	// inside the test's own temp dir rather than the developer's state dir.
	withBuildDir(t)
	manifest := storeManifest(t, "concurrent-image")
	contentRef := JailImageRef("podman", manifest)
	store := newSharedStore()

	const launches = 2
	var outs [launches]bytes.Buffer
	var results [launches]LoadResult
	var wg sync.WaitGroup
	for i := range launches {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = AutoLoadImage(AutoLoadOptions{
				Runtime: "podman",
				Out:     &outs[i],
				// Distinct pids because the nix out-link is per-pid: two launches in
				// one process would otherwise create and remove each other's.
				Getpid:         func() int { return 9000 + i },
				BuildStorePath: func(string, []any, string) (string, []string) { return manifest, nil },
				Run:            store.run,
				LayerCopy:      store.layerCopy,
				BuildCopier:    func(string) (string, []string) { return "/nix/store/fake-skopeo/bin/skopeo", nil },
				PresentDigests: func() map[string]struct{} { return nil },
				// Stubbed so neither goroutine shells out to `podman info`; the
				// namespace decision is not what this test is about.
				Rootless: func() PodmanRootless { return RootlessNo },
			})
		}(i)
	}
	wg.Wait()

	if got := store.copyCount(); got != 1 {
		t.Errorf("two concurrent loads of one ref made %d copies, want exactly 1", got)
	}
	for i := range launches {
		if !results[i].OK {
			t.Errorf("launch %d failed; out=%q", i, outs[i].String())
		}
		if results[i].Ref != contentRef {
			t.Errorf("launch %d ran ref %q, want %q", i, results[i].Ref, contentRef)
		}
	}

	// BOTH LAUNCHES MUST SAY WHAT HAPPENED, which is the other half of "one
	// copy": a launch that silently blocked for four minutes on a lock reads as a
	// hang, and a launch that silently skipped the copy leaves a human unable to
	// account for an image nobody in this terminal delivered (report-tiers.md,
	// OQ-RO3 — a launch has no quiet mode).
	waited, skipped := 0, 0
	for i := range launches {
		if strings.Contains(outs[i].String(), "Waiting for another launch to finish copying") {
			waited++
		}
		if strings.Contains(outs[i].String(), "delivered by a concurrent launch") {
			skipped++
		}
	}
	if waited != 1 {
		t.Errorf("%d launches announced the wait, want exactly 1; outs=%q, %q",
			waited, outs[0].String(), outs[1].String())
	}
	if skipped != 1 {
		t.Errorf("%d launches reported skipping the copy, want exactly 1; outs=%q, %q",
			skipped, outs[0].String(), outs[1].String())
	}
}

// TestImageCopyLockIsNotTheHousekeepingLock pins the one structural fact roadmap
// row 9 is explicit about: this is a SECOND lock file, not a reuse of the
// housekeeping one.
//
// The name is asserted against a literal rather than against
// run.HousekeepingLockPath because internal/cli/run imports this package, so the
// comparison cannot be written the other way round. The literal is the point
// anyway — the failure this guards against is somebody "simplifying" the two
// into one file, and that edit changes this constant.
func TestImageCopyLockIsNotTheHousekeepingLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := ImageCopyLockPath()
	if base := filepath.Base(path); base == "housekeeping.lock" {
		t.Fatalf("the image-copy lock must be its own file, not the housekeeping lock (%s)", path)
	}
	if base := filepath.Base(path); base != imageCopyLockName {
		t.Errorf("ImageCopyLockPath base = %q, want %q", base, imageCopyLockName)
	}
	// Beside every other flock in the tree, which is where `yolo prune` and a
	// human looking for what is holding them up expect to find one.
	if dir := filepath.Base(filepath.Dir(path)); dir != "locks" {
		t.Errorf("ImageCopyLockPath = %q, want it under locks/", path)
	}
}

// TestImageCopyLockHeldSerialisesASecondTaker is the lock's own unit, separate
// from the load-path test above so that a failure says which half broke.
func TestImageCopyLockHeldSerialisesASecondTaker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := ImageCopyLockPath()

	var notice string
	release := lockImageCopy(path, copyLockNotices{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		second := lockImageCopy(path, copyLockNotices{waiting: func(s string) { notice = s }})
		second()
	}()
	select {
	case <-done:
		t.Fatal("a second taker acquired the image-copy lock while the first held it")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the second taker never acquired the lock after it was released")
	}
	if !strings.Contains(notice, "Waiting for another launch") {
		t.Errorf("a blocked taker must say so; notice = %q", notice)
	}
	// Idempotent, because the call site hands the release around several return
	// paths.
	release()
}

// TestImageCopyLockDegradesWhenFlockFails pins the failure disposition: a lock
// that cannot be taken costs a duplicate copy, which is slow, and must never
// cost a launch. Escalating here would refuse a jail over a filesystem whose
// flock is a no-op.
func TestImageCopyLockDegradesWhenFlockFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	orig := copyFlockSyscall
	t.Cleanup(func() { copyFlockSyscall = orig })
	copyFlockSyscall = func(int, int) error { return syscall.EBADF }

	var warned string
	release := lockImageCopy(ImageCopyLockPath(), copyLockNotices{warn: func(s string) { warned = s }})
	if release == nil {
		t.Fatal("lockImageCopy returned a nil release; the call site defers it unconditionally")
	}
	release()
	if !strings.Contains(warned, "image-copy lock") {
		t.Errorf("a failed lock must say the serialisation is off; warn = %q", warned)
	}
}
