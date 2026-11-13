package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
)

// A timer plain-fork admission that waits on a content-address lock must not hold the scheduler
// state lock while it waits: a launch's registration takes that lock and would stall behind a CAS
// wait of up to twenty minutes. Only the short rename runs under the subscription guard.
func TestPlainForkTimerAdmissionWaitsOnCASLockOutsideSchedulerStateLock(t *testing.T) {
	forkBuildHome(t)
	f := packload.Forks(selectConfiguredHostPacks().packs)[0]
	setForkNextLaunch(t)
	stubPreparationWorkerStart(t)
	previousChild := forkBuildChild
	forkBuildChild = func(ctx context.Context, bound time.Duration, staging string, b forkBuild, streams captureStreams,
		color bool) (int, bool) {
		return forkBuildRunJail(staging, b, streams, color), false
	}
	t.Cleanup(func() { forkBuildChild = previousChild })
	var seen run.Options
	withFakeCaptureJail(t, fakeBuildJail(t, &seen, probetoolBuilt))

	req := preparationForkRequest(f, forkTestCommit, "podman")
	req.Platform = "linux/arm64"
	var launchLog strings.Builder
	deliveries, _ := runBuildSlot(run.BuildSlotRequest{Forks: &req, MaxBuilds: 1}, &launchLog, false)
	key := deliveries[f.Bin].Key
	if key == "" {
		t.Fatalf("fixture launch built nothing:\n%s", launchLog.String())
	}
	store := &capture.Store{Dir: paths.CapturesDir()}
	if err := os.RemoveAll(store.EntryDir(key)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := readPreparationSnapshot(f.Key())
	if err != nil {
		t.Fatal(err)
	}

	entryLock := filepath.Join(store.Dir, "locks", "entry-"+key+".lock")
	held, err := pidlock.Acquire(entryLock, pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	releaseEntry := func() { held.Release() }
	defer releaseEntry()

	// The admission reaching the CAS wait is the event: a nonblocking flock on the entry lock that
	// finds it held.
	waiting := make(chan struct{}, 1)
	previousFlock := pidlock.Flock
	pidlock.Flock = func(fd int, how int) error {
		err := previousFlock(fd, how)
		if errors.Is(err, syscall.EWOULDBLOCK) {
			if sameFileAsPath(fd, entryLock) {
				select {
				case waiting <- struct{}{}:
				default:
				}
			}
		}
		return err
	}
	t.Cleanup(func() { pidlock.Flock = previousFlock })

	record := preparationRecord{Key: f.Key(), Runtime: "podman", Platform: req.Platform,
		Kind: preparationPlainProgramKind, Generation: snapshot.Generation, PinCommit: forkTestCommit}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	plainDone := make(chan error, 1)
	go func() { plainDone <- runPlainForkPreparation(ctx, record, f, io.Discard) }()
	select {
	case <-waiting:
	case err := <-plainDone:
		t.Fatalf("timer admission finished (%v) without waiting on the held CAS lock", err)
	case <-time.After(30 * time.Second):
		t.Fatal("timer admission never reached the held CAS lock")
	}

	registered := make(chan error, 1)
	go func() {
		registered <- registerForkPreparation([]packload.ForkPin{{Fork: f, Commit: forkTestCommit, Ref: "main"}}, req, io.Discard, false)
	}()
	select {
	case err := <-registered:
		if err != nil {
			t.Errorf("launch registration: %v", err)
		}
	case <-time.After(10 * time.Second):
		releaseEntry()
		<-registered
		<-plainDone
		t.Fatal("a launch's registration stalled behind the timer's CAS-lock wait: the scheduler state lock was held across it")
	}
	releaseEntry()
	if err := <-plainDone; err != nil {
		t.Fatalf("timer admission after the CAS lock was released: %v", err)
	}
	if outcome := outcomeOf(t, f.Key()); outcome.State != bgMoved || outcome.To != key {
		t.Errorf("timer outcome = %+v, want the CAS entry %s admitted", outcome, key)
	}
}

// Resolver lock contention in an ordinary-program timer act is a busy skip, as on the plain and
// tree paths: it neither stamps a failure nor pushes the next attempt out by the hour-long retry
// throttle.
func TestOrdinaryTimerResolverContentionIsBusyNotFailed(t *testing.T) {
	forkBuildHome(t)
	stubPreparationWorkerStart(t)
	const key = "ordinary-fixture"
	platform := captureJailPlatform()
	if err := withPreparationState(func(state *preparationState) error {
		state.Subscriptions = append(state.Subscriptions, preparationRecord{Key: key, Runtime: "podman", Platform: platform,
			Kind: preparationOrdinaryProgramKind, InputIdentity: "identity", Generation: 1, Active: true, Phase: "pending"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	oldRefresh, oldDue, oldRun := preparationRefreshOrdinary, preparationOrdinaryDue, preparationRunOrdinary
	t.Cleanup(func() {
		preparationRefreshOrdinary, preparationOrdinaryDue, preparationRunOrdinary = oldRefresh, oldDue, oldRun
	})
	preparationRefreshOrdinary = func(_ context.Context, records []preparationRecord) []preparationRecord { return records }
	preparationOrdinaryDue = func(context.Context, preparationRecord) bool { return true }
	var runs atomic.Int32
	preparationRunOrdinary = func(context.Context, preparationRecord, io.Writer) (*npmCaptureRecord, error) {
		runs.Add(1)
		return nil, fmt.Errorf("resolving the selected pack: %w", packsrc.ErrLockHeld)
	}
	settled := make(chan string, 4)
	previousSettled := preparationTaskSettled
	preparationTaskSettled = func(k string) { settled <- k }
	t.Cleanup(func() { preparationTaskSettled = previousSettled })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	done := make(chan error, 1)
	go func() { done <- runPreparationScheduler(ctx, ticks, io.Discard) }()
	ticks <- time.Now()
	select {
	case <-settled:
	case <-time.After(30 * time.Second):
		t.Fatal("the ordinary timer act never settled")
	}
	snapshot, err := readPreparationSnapshot(key)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Phase != "busy" || !snapshot.RetryAt.IsZero() {
		t.Errorf("contended ordinary act = phase %q retry %v (%s), want busy with no retry throttle", snapshot.Phase, snapshot.RetryAt, snapshot.Reason)
	}
	// The next real tick retries at once: no hour-long backoff was stamped.
	ticks <- time.Now().Add(time.Second)
	select {
	case <-settled:
	case <-time.After(30 * time.Second):
		t.Fatal("the next tick did not retry the busy ordinary act")
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if runs.Load() != 2 {
		t.Errorf("ordinary act ran %d times, want one per tick", runs.Load())
	}
}

// sameFileAsPath reports whether fd is open on path, by device and inode: macOS has no
// /proc/self/fd to read a descriptor's path from.
func sameFileAsPath(fd int, path string) bool {
	var byFD, byPath syscall.Stat_t
	if syscall.Fstat(fd, &byFD) != nil || syscall.Stat(path, &byPath) != nil {
		return false
	}
	return byFD.Dev == byPath.Dev && byFD.Ino == byPath.Ino
}
