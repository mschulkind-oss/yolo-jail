package run

// imageloadmemo_test.go pins memoizedImageLoad at its call site, autoLoadImage: the real loader runs
// once per image per process, so the build jails a launch runs in-process (a fork's or a patched
// extension's, under the seal) do not each evaluate the flake again for the image the first one
// made ready — and a remembered image the runtime no longer holds is loaded again.

import (
	"errors"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// countingImageLoads swaps the real loader for one answering ref and counting its calls, over an
// empty memo, both restored when t ends.
func countingImageLoads(t *testing.T, ref string) *int {
	t.Helper()
	calls := 0
	prevLoad := imageAutoLoad
	imageLoads.Lock()
	prevReady := imageLoads.ready
	imageLoads.ready = map[imageLoadKey]image.LoadResult{}
	imageLoads.Unlock()
	imageAutoLoad = func(image.AutoLoadOptions) image.LoadResult {
		calls++
		return image.LoadResult{OK: ref != "", Ref: ref, StorePath: "/nix/store/sealtest-image"}
	}
	t.Cleanup(func() {
		imageAutoLoad = prevLoad
		imageLoads.Lock()
		imageLoads.ready = prevReady
		imageLoads.Unlock()
	})
	return &calls
}

// memoLaunch is one launch's Options for the memo tests: the real loader (no autoLoad), and a
// runtime whose `image inspect` answers present.
func memoLaunch(t *testing.T, present *bool, inspected *[]string) *Options {
	t.Helper()
	o := goldenOptions(t.TempDir(), t.TempDir())
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if slices.Equal(argv[:min(3, len(argv))], []string{"podman", "image", "inspect"}) && inspected != nil {
			*inspected = append(*inspected, argv[len(argv)-1])
		}
		rc := 1
		if *present {
			rc = 0
		}
		return ExecResult{Ran: true, RC: rc}
	}
	return o
}

func TestLaunchesInOneProcessLoadOneImageOnce(t *testing.T) {
	calls := countingImageLoads(t, "localhost/yolo-jail:sealtest")
	repo := t.TempDir()
	present := true
	var inspected []string
	// Two launches in this process — a build jail and the launch that ran it — asking for one image.
	first := memoLaunch(t, &present, &inspected).autoLoadImage(jsonx.NewOrderedMap(), "podman", repo, storePackagesPlan{})
	second := memoLaunch(t, &present, &inspected).autoLoadImage(jsonx.NewOrderedMap(), "podman", repo, storePackagesPlan{})
	if *calls != 1 {
		t.Errorf("two launches of one image in one process ran the real loader %d times, want once", *calls)
	}
	if !second.OK || second != first {
		t.Errorf("the second launch got %+v, want the first one's answer %+v", second, first)
	}
	if !slices.Equal(inspected, []string{"localhost/yolo-jail:sealtest"}) {
		t.Errorf("the remembered image was confirmed by inspecting %q, want the one ref, once", inspected)
	}
	// Another image is another load: the packages select the derivation.
	cfg := jsonx.NewOrderedMap()
	cfg.Set("packages", []any{"ripgrep"})
	memoLaunch(t, &present, nil).autoLoadImage(cfg, "podman", repo, storePackagesPlan{})
	if *calls != 2 {
		t.Errorf("a launch asking for another image (other packages) ran the real loader %d times in all, want 2", *calls)
	}
	// So is another flake.
	memoLaunch(t, &present, nil).autoLoadImage(jsonx.NewOrderedMap(), "podman", t.TempDir(), storePackagesPlan{})
	if *calls != 3 {
		t.Errorf("a launch from another flake ran the real loader %d times in all, want 3", *calls)
	}
}

// A remembered image the runtime no longer holds — reaped between the build jail's end and the
// launch that ran it — is loaded again rather than handed to a run that would fail.
func TestARememberedImageTheRuntimeLostIsLoadedAgain(t *testing.T) {
	calls := countingImageLoads(t, "localhost/yolo-jail:sealtest")
	repo := t.TempDir()
	present := true
	memoLaunch(t, &present, nil).autoLoadImage(jsonx.NewOrderedMap(), "podman", repo, storePackagesPlan{})
	present = false
	if res := memoLaunch(t, &present, nil).autoLoadImage(jsonx.NewOrderedMap(), "podman", repo, storePackagesPlan{}); !res.OK {
		t.Fatalf("the reload failed: %+v", res)
	}
	if *calls != 2 {
		t.Errorf("a remembered image the runtime lost ran the real loader %d times in all, want 2", *calls)
	}
}

// A REMEMBERED IMAGE IS CONFIRMED AND RECORDED UNDER THE HOUSEKEEPING LOCK, as the real loader's own
// present-image branch is (OQ-BF5, image.AutoLoadImage): a reap pass's recheck keeps an image only
// if a workspace's pointer names it or a launch recorded it in the load sentinel since the pass
// began (prune.AutoReapOldImagesGuarded). The memo serves exactly the launch whose image nothing
// else protects — the build's scratch workspace is gone and the parent has not written its own
// pointer — so a confirmation outside the lock, recording nothing, let a pass remove the image
// between this launch's inspect and its `podman run`.
func TestARememberedImageIsRecordedUnderTheHousekeepingLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// Every launch makes the build dir first (storage.EnsureGlobalStorage).
	if err := os.MkdirAll(paths.BuildDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	calls := countingImageLoads(t, "localhost/yolo-jail:sealtest")
	repo := t.TempDir()
	present := true
	memoLaunch(t, &present, nil).autoLoadImage(jsonx.NewOrderedMap(), "podman", repo, storePackagesPlan{})
	sentinel := image.LoadSentinelPath(paths.BuildDir(), "podman")
	if _, ok := image.CurrentLoadedPath(sentinel); ok {
		t.Fatal("the fixture's counting loader recorded a load itself, so the memo's record cannot be told apart")
	}

	o := memoLaunch(t, &present, nil)
	answer := o.Exec
	var lockedAtInspect []bool
	o.Exec = func(argv []string, dir string, env []string, d time.Duration) ExecResult {
		if slices.Equal(argv[:min(3, len(argv))], []string{"podman", "image", "inspect"}) {
			lockedAtInspect = append(lockedAtInspect, housekeepingLockHeld(t))
		}
		return answer(argv, dir, env, d)
	}
	if res := o.autoLoadImage(jsonx.NewOrderedMap(), "podman", repo, storePackagesPlan{}); !res.OK {
		t.Fatalf("the remembered image came back failed: %+v", res)
	}
	if *calls != 1 {
		t.Fatalf("the second launch ran the real loader (%d calls in all), so the memo is not under test", *calls)
	}
	if !slices.Equal(lockedAtInspect, []bool{true}) {
		t.Errorf("the remembered image was confirmed with the housekeeping lock held %v, want once, held", lockedAtInspect)
	}
	if got, ok := image.CurrentLoadedPath(sentinel); !ok || got != "/nix/store/sealtest-image" {
		t.Errorf("the remembered image's store path is not recorded in the load sentinel (got %q, %v), so a "+
			"reap pass's recheck does not keep it", got, ok)
	}
}

// housekeepingLockHeld reports whether some open file holds the housekeeping lock: a second open of
// the lock file cannot take it without blocking.
func housekeepingLockHeld(t *testing.T) bool {
	t.Helper()
	f, err := os.OpenFile(HousekeepingLockPath(), os.O_RDONLY, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// A failed load is not remembered: it ended its launch, and the next asker tries again.
func TestAFailedImageLoadIsNotRemembered(t *testing.T) {
	calls := countingImageLoads(t, "")
	repo := t.TempDir()
	present := true
	for range 2 {
		if res := memoLaunch(t, &present, nil).autoLoadImage(jsonx.NewOrderedMap(), "podman", repo, storePackagesPlan{}); res.OK {
			t.Fatalf("the failing loader's answer came back OK: %+v", res)
		}
	}
	if *calls != 2 {
		t.Errorf("two launches after a failed load ran the real loader %d times, want 2", *calls)
	}
}
