package image

// confirmloaded_test.go pins OQ-BF5's inspect-and-record bracket (ConfirmLoaded): the inspect runs
// with the housekeeping lock held, and a present image's store path is in the load sentinel before
// the lock is let go, which is what a reap pass's recheck reads (prune.AutoReapOldImagesGuarded).
// It is pinned at AutoLoadImage too, the call site whose bracket it was, so deleting the call
// there fails here.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trackingLock is a LockHousekeeping seam recording whether it is held, and what the load sentinel
// held at each release.
type trackingLock struct {
	sentinel string
	held     bool
	takes    int
	atUnlock []string
}

func (l *trackingLock) lock() func() {
	l.held = true
	l.takes++
	return func() {
		body, _ := os.ReadFile(l.sentinel)
		l.atUnlock = append(l.atUnlock, string(body))
		l.held = false
	}
}

func TestConfirmLoadedRecordsAPresentImageBeforeTheLockIsLetGo(t *testing.T) {
	bd := withBuildDir(t)
	l := &trackingLock{sentinel: filepath.Join(bd, "last-load-podman")}
	var heldAtInspect []bool
	run := func(argv []string) (int, bool) {
		heldAtInspect = append(heldAtInspect, l.held)
		return 0, true
	}
	if !ConfirmLoaded("podman", "localhost/yolo-jail:abc", "/nix/store/abc-image", l.lock, run) {
		t.Fatal("a present image was not confirmed")
	}
	if len(heldAtInspect) != 1 || !heldAtInspect[0] {
		t.Errorf("the inspect ran with the lock held %v, want once, held", heldAtInspect)
	}
	if l.held || l.takes != 1 {
		t.Errorf("the lock was taken %d times and is held %v after the return, want once and released", l.takes, l.held)
	}
	if len(l.atUnlock) != 1 || !strings.Contains(l.atUnlock[0], "/nix/store/abc-image") {
		t.Errorf("the sentinel held %q as the lock was let go, want the confirmed store path", l.atUnlock)
	}
}

func TestConfirmLoadedRecordsNothingForAnAbsentImageOrAnUnknownPath(t *testing.T) {
	bd := withBuildDir(t)
	sentinel := filepath.Join(bd, "last-load-podman")
	l := &trackingLock{sentinel: sentinel}
	absent := func([]string) (int, bool) { return 1, true }
	if ConfirmLoaded("podman", "localhost/yolo-jail:abc", "/nix/store/abc-image", l.lock, absent) {
		t.Error("an absent image was confirmed")
	}
	present := func([]string) (int, bool) { return 0, true }
	if !ConfirmLoaded("podman", "localhost/yolo-jail:latest", "", l.lock, present) {
		t.Error("a present image with no known store path was not confirmed")
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Errorf("the sentinel was written (err %v) for an absent image or an unknown path", err)
	}
	if l.held || l.takes != 2 {
		t.Errorf("the lock was taken %d times and is held %v, want twice and released", l.takes, l.held)
	}
	if !ConfirmLoaded("podman", "localhost/yolo-jail:abc", "/nix/store/abc-image", nil, present) {
		t.Error("a nil lock did not confirm a present image unlocked")
	}
}

// AutoLoadImage decides an already-loaded image is present through ConfirmLoaded: the content
// ref's inspect runs under LockHousekeeping, and the store path is recorded before the release.
func TestAutoLoadImageConfirmsAPresentImageUnderTheLock(t *testing.T) {
	bd := withBuildDir(t)
	path := storeManifest(t, "path-present-image")
	ref := JailImageRef("podman", path)
	f := newFakeRuntime(ref)
	l := &trackingLock{sentinel: filepath.Join(bd, "last-load-podman")}
	var heldAtInspect []bool
	res := AutoLoadImage(AutoLoadOptions{
		Runtime: "podman",
		Out:     &bytes.Buffer{},
		BuildStorePath: func(string, []any, string) (string, []string) {
			return path, nil
		},
		Run: func(argv []string) (int, bool) {
			if len(argv) >= 4 && argv[1] == "image" && argv[2] == "inspect" && argv[3] == ref {
				heldAtInspect = append(heldAtInspect, l.held)
			}
			return f.run(argv)
		},
		LayerCopy:        f.layerCopy,
		BuildCopier:      func(string) (string, []string) { return "/nix/store/fake-skopeo/bin/skopeo", nil },
		PresentDigests:   func() map[string]struct{} { return nil },
		StoreFacts:       rootfulStore,
		LockHousekeeping: l.lock,
	})
	if !res.OK || res.Ref != ref {
		t.Fatalf("AutoLoadImage = %+v, want the present image %q", res, ref)
	}
	if len(f.copiedDests) != 0 {
		t.Fatalf("a present image was copied again (%d copies), so the present-image branch is not under test", len(f.copiedDests))
	}
	if len(heldAtInspect) == 0 || !heldAtInspect[0] {
		t.Errorf("the content ref's first inspect ran with the lock held %v, want held", heldAtInspect)
	}
	if len(l.atUnlock) == 0 || !strings.Contains(l.atUnlock[0], path) {
		t.Errorf("the sentinel held %q as the lock was first let go, want the image's store path", l.atUnlock)
	}
}
