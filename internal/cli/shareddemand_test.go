package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func TestTask2ProducerCancellationWithSecondFreshWaiter(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	first, _, _ := fx.launch(t, "podman")
	if first.failed {
		t.Fatalf("seed: %+v", first)
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 20: "twenty"})
	f := fx.fork(t)
	after, err := patchedAdvanceStore(true).CheckBoundary(f.Key())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := freshPreparationIdentity(f, patchedTestPlatform)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	childCancelled := make(chan struct{}, 1)
	var calls atomic.Int32
	previous := forkBuildChild
	forkBuildChild = func(ctx context.Context, bound time.Duration, staging string, b forkBuild, s captureStreams, color bool) (int, bool) {
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-ctx.Done():
				childCancelled <- struct{}{}
				return 130, false
			case <-release:
			}
		}
		return previous(ctx, bound, staging, b, s, color)
	}
	defer func() { forkBuildChild = previous }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var secondOut syncBuffer
	options := func(ctx context.Context, out io.Writer) advanceOptions {
		return advanceOptions{platform: patchedTestPlatform, runtime: "podman", launch: true, ctx: ctx, out: out, errw: out, freshCheck: true, freshIdentity: identity, freshBoundary: after}
	}
	firstDone, secondDone := make(chan advanceResult, 1), make(chan advanceResult, 1)
	go func() { firstDone <- advancePatchedFork(f, options(ctx, io.Discard)) }()
	<-entered
	go func() { secondDone <- advancePatchedFork(f, options(context.Background(), &secondOut)) }()
	waitFor(t, "second fresh requester waiting on owned production", func() bool { return strings.Contains(secondOut.String(), "waiting for pid") })
	cancel()
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Error("cancelled waiter did not detach promptly")
	}
	select {
	case <-childCancelled:
		t.Error("producer context cancelled shared build while second fresh waiter remained")
	default:
	}
	close(release)
	second := <-secondDone
	if second.failed {
		t.Errorf("second demand failed: %+v\n%s", second, secondOut.String())
	}
	if calls.Load() != 1 {
		t.Errorf("cancelled shared producer was replaced by %d child builds", calls.Load())
	}
}

// Unit fixtures use the exact worker and lease protocol, with a separately owned worker context.
// Process tests below use self-exec instead and prove that this seam is not lifetime authority.
//
// It returns the stop it also registers as a cleanup (idempotent), for a fixture whose later
// cleanups restore seams the workers read and so must stop them first.
func stubSharedPreparationWorker(t *testing.T) func() {
	t.Helper()
	previous := sharedPreparationSpawn
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	sharedPreparationSpawn = func(_ context.Context, d preparationDemand) error {
		wg.Add(1)
		go func() { defer wg.Done(); _ = runSharedPreparation(ctx, d, io.Discard) }()
		return nil
	}
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); wg.Wait() }) }
	t.Cleanup(func() { stop(); sharedPreparationSpawn = previous })
	return stop
}

func TestWithDemandStatePreservesHeldLockAndCancellationIdentities(t *testing.T) {
	dir := t.TempDir()
	held, err := pidlock.Acquire(filepath.Join(dir, "state.lock"), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	got := withDemandState(context.Background(), dir, false, func() error { return nil })
	if !errors.Is(got, pidlock.ErrHeld) || !errors.Is(got, packsrc.ErrLockHeld) {
		t.Fatalf("held demand-state lock lost an identity: %v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got = withDemandState(ctx, t.TempDir(), false, func() error { return nil })
	if !errors.Is(got, context.Canceled) || errors.Is(got, pidlock.ErrHeld) || errors.Is(got, packsrc.ErrLockHeld) {
		t.Fatalf("cancellation was changed into lock contention: %v", got)
	}
}

// pauseDemandStateLock takes a demand's state.lock from an admission hook, to hold the admission
// paused, waiting up to the readiness budget. A single try is wrong here: the requester's retry
// wait and the worker's demand monitor each poll that lock every sharedDemandPoll, so a try that
// overlaps one of those brief reads finds the lock held by routine bookkeeping, not by contention
// the test is arranging, and fails the test (CI run 38782226184: "pause real demand
// authorization: lock held").
func pauseDemandStateLock(t *testing.T, path string) (*pidlock.Lock, error) {
	return pidlock.Acquire(path, pidlock.Mode{Wait: true, Bound: testsupport.ReadinessBudget(t)}, nil)
}

func TestSharedFreshDemandRetriesStateLockAtInitialAndFinalAdmissions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		guardCall  int32
		heldBuilds int
		wantBuilds int
	}{
		{name: "initial check start", guardCall: 1, heldBuilds: 0, wantBuilds: 1},
		{name: "capture publication", guardCall: 2, heldBuilds: 1, wantBuilds: 2},
		{name: "Good settlement", guardCall: 3, heldBuilds: 1, wantBuilds: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx, oldGood := forkWithAGoodBuild(t)
			newCommit := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty", 22: "twenty-two"})
			fx.later(2 * time.Hour)
			baseChildren := sharedDemandChildCount(fx)
			stubSharedPreparationWorker(t)

			var calls atomic.Int32
			key := fx.fork(t).Key()
			identity, err := freshPreparationIdentity(fx.fork(t), patchedTestPlatform)
			if err != nil {
				t.Fatal(err)
			}
			lockReady := make(chan *pidlock.Lock, 1)
			lockErr := make(chan error, 1)
			previousHook := sharedDemandAdmissionHook
			sharedDemandAdmissionHook = func(d preparationDemand) {
				if d.Key != key || d.Identity != identity || !d.Fresh || calls.Add(1) != tc.guardCall {
					return
				}
				path := filepath.Join(sharedDemandDir(key, "podman", patchedTestPlatform), "state.lock")
				lock, err := pauseDemandStateLock(t, path)
				if err != nil {
					lockErr <- err
					return
				}
				lockReady <- lock
			}
			t.Cleanup(func() { sharedDemandAdmissionHook = previousHook })

			req := forkRequest(t, fx, "podman")
			req.FreshUpdate = true
			type slotResult struct {
				delivery map[string]entrypoint.ForkDelivery
				output   string
			}
			done := make(chan slotResult, 1)
			var output syncBuffer
			go func() {
				delivery, _ := runBuildSlot(run.BuildSlotRequest{Forks: &req, MaxBuilds: 1}, &output, false)
				done <- slotResult{delivery: delivery, output: output.String()}
			}()

			var held *pidlock.Lock
			select {
			case held = <-lockReady:
			case err := <-lockErr:
				t.Fatalf("pause real demand authorization: %v", err)
			case result := <-done:
				t.Fatalf("fresh demand finished before reaching requested authorization site: %+v\n%s", result.delivery["tool"], result.output)
			case <-time.After(testsupport.ReadinessBudget(t)):
				t.Fatalf("production fresh demand did not reach requested authorization site; output so far:\n%s", output.String())
			}
			defer held.Release()
			time.Sleep(3 * sharedDemandPoll)
			select {
			case result := <-done:
				t.Fatalf("fresh demand published a result while demand state.lock was held: %+v\n%s", result.delivery["tool"], result.output)
			default:
			}
			if got := fx.record(t).Good; got == nil || got.Entry != oldGood {
				t.Fatalf("Good changed while authorization was held: %+v, want %s", got, oldGood)
			}
			for _, outcome := range fx.record(t).Outcomes {
				if outcome.Commit == newCommit && outcome.Kind == packsrc.OutcomeBuildFailed {
					t.Fatalf("transient demand-state contention recorded a build failure: %+v", outcome)
				}
			}
			if got := sharedDemandChildCount(fx) - baseChildren; got != tc.heldBuilds {
				t.Fatalf("candidate children while authorization stayed locked=%d, want exactly %d for %s", got, tc.heldBuilds, tc.name)
			}
			held.Release()
			var result slotResult
			select {
			case result = <-done:
			case <-time.After(testsupport.ReadinessBudget(t)):
				t.Fatal("fresh demand did not retry after demand state.lock was released")
			}
			got := result.delivery["tool"]
			if got.Key == "" || got.FreshFailure != "" || fx.record(t).Good.Commit != newCommit {
				t.Fatalf("released transient lock did not admit the exact requested build: delivery=%+v record=%+v\n%s",
					got, fx.record(t).Good, result.output)
			}
			if builds := sharedDemandChildCount(fx) - baseChildren; builds != tc.wantBuilds {
				t.Fatalf("candidate child runs=%d, want %d for %s", builds, tc.wantBuilds, tc.name)
			}
		})
	}
}

// shrinkSharedDemandCleanup shrinks a released request's cleanup bound, for a test that holds
// state.lock across the release: the cleanup then waits for a lock it cannot get, and would wait
// out the production second.
func shrinkSharedDemandCleanup(t *testing.T) {
	t.Helper()
	saved := sharedDemandCleanupBound
	sharedDemandCleanupBound = 3 * sharedDemandPoll
	t.Cleanup(func() { sharedDemandCleanupBound = saved })
}

func TestSharedFreshDemandCancellationDuringFinalStateLockRetry(t *testing.T) {
	for _, tc := range []struct {
		name      string
		guardCall int32
	}{
		{name: "capture publication", guardCall: 2},
		{name: "Good settlement", guardCall: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shrinkSharedDemandCleanup(t)
			fx, oldGood := forkWithAGoodBuild(t)
			newCommit := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty", 22: "twenty-two"})
			fx.later(2 * time.Hour)
			key := fx.fork(t).Key()
			identity, err := freshPreparationIdentity(fx.fork(t), patchedTestPlatform)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			lockReady := make(chan *pidlock.Lock, 1)
			lockErr := make(chan error, 1)
			previousHook := sharedDemandAdmissionHook
			sharedDemandAdmissionHook = func(d preparationDemand) {
				if d.Key != key || d.Identity != identity || !d.Fresh || calls.Add(1) != tc.guardCall {
					return
				}
				lock, lockErrAcquire := pauseDemandStateLock(t, filepath.Join(sharedDemandDir(key, "podman", patchedTestPlatform), "state.lock"))
				if lockErrAcquire != nil {
					lockErr <- lockErrAcquire
					return
				}
				lockReady <- lock
			}
			t.Cleanup(func() { sharedDemandAdmissionHook = previousHook })

			req := forkRequest(t, fx, "podman")
			req.FreshUpdate = true
			var output syncBuffer
			done := make(chan map[string]entrypoint.ForkDelivery, 1)
			go func() {
				delivery, _ := runBuildSlot(run.BuildSlotRequest{Forks: &req, MaxBuilds: 1}, &output, false)
				done <- delivery
			}()
			var held *pidlock.Lock
			select {
			case held = <-lockReady:
			case lockErr := <-lockErr:
				t.Fatalf("pause %s authorization: %v", tc.name, lockErr)
			case delivery := <-done:
				t.Fatalf("fresh request finished before %s authorization: %+v\n%s", tc.name, delivery["tool"], output.String())
			case <-time.After(testsupport.ReadinessBudget(t)):
				t.Fatalf("fresh request did not reach %s authorization; output:\n%s", tc.name, output.String())
			}
			t.Cleanup(func() { held.Release() })
			demandDir := sharedDemandDir(key, "podman", patchedTestPlatform)
			lockPath := filepath.Join(demandDir, "state.lock")
			lockBytes, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatal(err)
			}
			workerLock := filepath.Join(demandDir, "worker.lock")
			if !pidlock.Held(workerLock) {
				t.Fatal("authorization pause was not owned by the preparation producer")
			}
			time.Sleep(3 * sharedDemandPoll)
			select {
			case delivery := <-done:
				t.Fatalf("request settled while its owned state.lock remained held: %+v\n%s", delivery["tool"], output.String())
			default:
			}
			if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
				t.Fatal(err)
			}
			var delivery map[string]entrypoint.ForkDelivery
			select {
			case delivery = <-done:
			case <-time.After(testsupport.ReadinessBudget(t)):
				t.Fatalf("cancelled requester did not refuse while state.lock stayed held; output:\n%s", output.String())
			}
			got := delivery["tool"]
			if got.Key != "" || got.FreshFailure == "" {
				t.Fatalf("cancelled requester delivered or omitted its refusal: %+v\n%s", got, output.String())
			}
			if !pidlock.Held(lockPath) {
				t.Fatal("request cancellation released a state.lock it did not own")
			}
			if after, err := os.ReadFile(lockPath); err != nil || !bytes.Equal(after, lockBytes) {
				t.Fatalf("request cancellation changed owned state.lock bytes: before=%q after=%q err=%v", lockBytes, after, err)
			}
			if record := fx.record(t); record.Good == nil || record.Good.Entry != oldGood {
				t.Fatalf("cancelled request changed Good: %+v, want %s", record.Good, oldGood)
			}
			for _, outcome := range fx.record(t).Outcomes {
				if outcome.Commit == newCommit && outcome.Kind == packsrc.OutcomeBuildFailed {
					t.Fatalf("cancelled transient contention recorded a build failure: %+v", outcome)
				}
			}
			childrenAtRefusal := sharedDemandChildCount(fx)
			held.Release()
			waitFor(t, "cancelled producer teardown after owned state.lock release", func() bool { return !pidlock.Held(workerLock) })
			if after, err := os.ReadFile(lockPath); err != nil || !bytes.Equal(after, lockBytes) {
				t.Fatalf("producer teardown changed state.lock bytes: before=%q after=%q err=%v", lockBytes, after, err)
			}
			time.Sleep(3 * sharedDemandPoll)
			if got := fx.record(t); got.Good == nil || got.Good.Entry != oldGood {
				t.Fatalf("producer admitted work after requester cancellation: Good=%+v want entry %s", got.Good, oldGood)
			}
			for _, outcome := range fx.record(t).Outcomes {
				if outcome.Commit == newCommit && outcome.Kind == packsrc.OutcomeBuildFailed {
					t.Fatalf("producer teardown stamped a build failure after cancellation: %+v", outcome)
				}
			}
			if count := sharedDemandChildCount(fx); count != childrenAtRefusal {
				t.Fatalf("producer started another candidate after cancellation: children %d -> %d", childrenAtRefusal, count)
			}
		})
	}
}

func TestSharedFreshDemandRechecksSelectionAfterStateLockContention(t *testing.T) {
	fx, oldGood := forkWithAGoodBuild(t)
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty", 22: "twenty-two"})
	fx.later(2 * time.Hour)
	baseChildren := sharedDemandChildCount(fx)
	stubSharedPreparationWorker(t)
	key := fx.fork(t).Key()
	lockReady := make(chan *pidlock.Lock, 1)
	var lockOnce atomic.Bool
	previousHook := sharedDemandAdmissionHook
	sharedDemandAdmissionHook = func(_ preparationDemand) {
		if !lockOnce.CompareAndSwap(false, true) {
			return
		}
		lock, err := pauseDemandStateLock(t, filepath.Join(sharedDemandDir(key, "podman", patchedTestPlatform), "state.lock"))
		if err != nil {
			t.Errorf("pause initial demand authorization: %v", err)
			return
		}
		lockReady <- lock
	}
	t.Cleanup(func() { sharedDemandAdmissionHook = previousHook })

	req := forkRequest(t, fx, "podman")
	req.FreshUpdate = true
	done := make(chan map[string]entrypoint.ForkDelivery, 1)
	go func() {
		delivery, _ := runBuildSlot(run.BuildSlotRequest{Forks: &req, MaxBuilds: 1}, io.Discard, false)
		done <- delivery
	}()
	var held *pidlock.Lock
	select {
	case held = <-lockReady:
	case <-time.After(testsupport.ReadinessBudget(t)):
		t.Fatal("fresh request did not reach its initial authorization")
	}
	defer held.Release()
	fx.writeUserConfig(t, `{"packs":[]}`)
	held.Release()
	select {
	case delivery := <-done:
		if delivery["tool"].Key != "" || delivery["tool"].FreshFailure == "" {
			t.Fatalf("revoked selection was admitted after lock release: %+v", delivery["tool"])
		}
	case <-time.After(testsupport.ReadinessBudget(t)):
		t.Fatal("revoked fresh request did not settle after lock release")
	}
	if got := fx.record(t).Good; got == nil || got.Entry != oldGood || sharedDemandChildCount(fx) != baseChildren {
		t.Fatalf("revoked request changed current success: Good=%+v children=%d want %s/%d", got, sharedDemandChildCount(fx), oldGood, baseChildren)
	}
}

func sharedDemandChildCount(fx *patchedAdvanceFixture) int {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return fx.child
}

func TestSharedBackgroundDemandStateLockStaysBusyAndRetriesNextTick(t *testing.T) {
	pf := readyForABackgroundAdvance(t)
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	stubSpawn(t)
	pf.launch(t, "podman", &run.ActInterrupt{})
	if err := withPreparationState(func(state *preparationState) error { state.Worker.Identity = "timer-fixture"; return nil }); err != nil {
		t.Fatal(err)
	}
	oldGood := outcomeOfRecord(t, poolKeyA)
	baseline := uint64(0)
	if old, err := readBackgroundOutcome(backgroundOutcomePath(poolKeyA)); err == nil {
		baseline = old.Generation
	}
	stubSharedPreparationWorker(t)
	var lockOnce atomic.Bool
	lockReady := make(chan *pidlock.Lock, 1)
	previousHook := sharedDemandAdmissionHook
	sharedDemandAdmissionHook = func(d preparationDemand) {
		if d.Key != poolKeyA || !lockOnce.CompareAndSwap(false, true) {
			return
		}
		lock, err := pauseDemandStateLock(t, filepath.Join(sharedDemandDir(d.Key, d.Runtime, d.Platform), "state.lock"))
		if err != nil {
			t.Errorf("pause background demand authorization: %v", err)
			return
		}
		lockReady <- lock
	}
	t.Cleanup(func() { sharedDemandAdmissionHook = previousHook })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	drain := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- runPreparationScheduler(ctx, ticks, io.Discard, nil, drain) }()
	ticks <- pf.now
	var held *pidlock.Lock
	select {
	case held = <-lockReady:
	case <-time.After(testsupport.ReadinessBudget(t)):
		t.Fatal("background worker did not reach shared-demand authorization")
	}
	defer held.Release()
	time.Sleep(3 * sharedDemandPoll)
	if got := outcomeOfRecord(t, poolKeyA); got != oldGood {
		t.Fatalf("background state-lock contention moved Good: %s -> %s", oldGood, got)
	}
	held.Release()
	waitFor(t, "state-lock contention to settle as a busy retry", func() bool {
		snapshot, err := readPreparationSnapshot(poolKeyA)
		outcome, outcomeErr := readBackgroundOutcome(backgroundOutcomePath(poolKeyA))
		return err == nil && outcomeErr == nil && snapshot.Phase == "busy" && snapshot.RetryAt.IsZero() &&
			outcome.Generation > baseline && outcome.State == bgSkipped && outcome.RetryAt == 0
	})
	ticks <- pf.now.Add(time.Minute)
	waitFor(t, "background retry to admit after state-lock release", func() bool {
		snapshot, err := readPreparationSnapshot(poolKeyA)
		return err == nil && snapshot.Phase == "admitted" && snapshot.ServingEntry != ""
	})
	if outcomeOfRecord(t, poolKeyA) == oldGood {
		t.Fatal("background retry did not move Good after contention ended")
	}
	close(drain)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(testsupport.ReadinessBudget(t)):
		cancel()
		<-done
		t.Fatal("background scheduler did not drain")
	}
}

func TestSharedDemandSchedulerStopDetachesWhileFreshTreeRequestRemains(t *testing.T) {
	pf := readyForABackgroundAdvance(t)
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	stubPreparationWorkerStart(t)
	entered, release, cancelled := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	var calls atomic.Int32
	pf.child = func(ctx context.Context, b forkBuild) int {
		if b.Fork.Key() != poolKeyA {
			return 0
		}
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-ctx.Done():
				cancelled <- struct{}{}
				return 130
			case <-release:
			}
		}
		return 0
	}
	_, _ = pf.launch(t, "podman", nil)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	ticks := make(chan time.Time)
	schedulerDone := make(chan error, 1)
	go func() { schedulerDone <- runPreparationScheduler(ctx, ticks, io.Discard) }()
	ticks <- pf.now
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timer did not start owned production")
	}
	f := packload.PatchedTrees(selectConfiguredHostPacks().packs)[0]
	req := run.TreeBuildRequest{Trees: []packload.Fork{f}, Build: true, Runtime: "podman", Platform: patchedTestPlatform, FreshUpdate: true, CopyRoot: filepath.Join(t.TempDir(), "tree.patched")}
	var output syncBuffer
	freshDone := make(chan map[string]run.TreeDelivery, 1)
	go func() {
		_, trees := runBuildSlot(run.BuildSlotRequest{Trees: &req}, &output, false)
		freshDone <- trees
	}()
	waitFor(t, "fresh tree requester attached", func() bool {
		dir := sharedDemandDir(poolKeyA, "podman", patchedTestPlatform)
		var live []livePreparationDemand
		err := withDemandState(context.Background(), dir, false, func() error { var e error; live, e = livePreparationDemands(dir); return e })
		return err == nil && len(live) == 2
	})
	stop()
	select {
	case <-schedulerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler tried to own another requester's production")
	}
	select {
	case <-cancelled:
		t.Fatal("scheduler exit killed production still owned by fresh demand")
	default:
	}
	close(release)
	select {
	case result := <-freshDone:
		if result[poolKeyA].Dir == "" || result[poolKeyA].FreshFailure != "" {
			t.Fatalf("fresh tree failed: %+v\n%s", result, output.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fresh tree request did not finish")
	}
	if calls.Load() != 1 {
		t.Fatalf("timer/fresh demand rebuilt %d children", calls.Load())
	}
}

func TestSharedDemandSchedulerStopDetachesBeforeDemandStateUnlock(t *testing.T) {
	pf := readyForABackgroundAdvance(t)
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	stubPreparationWorkerStart(t)
	entered, release, cancelled := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	var calls atomic.Int32
	pf.child = func(ctx context.Context, b forkBuild) int {
		if b.Fork.Key() != poolKeyA {
			return 0
		}
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-ctx.Done():
				cancelled <- struct{}{}
				return 130
			case <-release:
			}
		}
		return 0
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	_, _ = pf.launch(t, "podman", nil)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	ticks := make(chan time.Time)
	schedulerDone := make(chan error, 1)
	go func() { schedulerDone <- runPreparationScheduler(ctx, ticks, io.Discard) }()
	ticks <- pf.now
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timer did not start owned production")
	}
	f := packload.PatchedTrees(selectConfiguredHostPacks().packs)[0]
	req := run.TreeBuildRequest{Trees: []packload.Fork{f}, Build: true, Runtime: "podman", Platform: patchedTestPlatform, FreshUpdate: true, CopyRoot: filepath.Join(t.TempDir(), "tree.patched")}
	var output syncBuffer
	freshDone := make(chan map[string]run.TreeDelivery, 1)
	go func() {
		_, trees := runBuildSlot(run.BuildSlotRequest{Trees: &req}, &output, false)
		freshDone <- trees
	}()
	dir := sharedDemandDir(poolKeyA, "podman", patchedTestPlatform)
	waitFor(t, "fresh requester attached before scheduler stop", func() bool {
		var live []livePreparationDemand
		err := withDemandState(context.Background(), dir, false, func() error { var e error; live, e = livePreparationDemands(dir); return e })
		return err == nil && len(live) == 2
	})
	state, err := pidlock.Acquire(filepath.Join(dir, "state.lock"), pidlock.Mode{Wait: true, Bound: 5 * time.Second}, nil)
	if err != nil {
		t.Fatalf("hold demand state across cleanup deadline: %v", err)
	}
	defer state.Release()
	stop()
	select {
	case err := <-schedulerDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("scheduler stop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not detach before demand state unlocked after the one-second cleanup deadline")
	}
	select {
	case <-cancelled:
		t.Fatal("scheduler exit cancelled the producer still owned by the fresh requester")
	default:
	}
	state.Release()
	close(release)
	select {
	case result := <-freshDone:
		if result[poolKeyA].Dir == "" || result[poolKeyA].FreshFailure != "" {
			t.Fatalf("fresh tree failed after valid demand became readable: %+v\n%s", result, output.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fresh requester did not settle after the demand state unlocked")
	}
	if calls.Load() != 1 {
		t.Errorf("scheduler/fresh demand ran %d child acts, want the same child", calls.Load())
	}
}

func TestSharedDemandWorkspaceOnlyFreshBuildSlotKeepsForegroundAuthority(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled=%v", cancelled), func(t *testing.T) {
			fx := newPatchedAdvanceFixture(t, "")
			fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
			seeded, _, _ := fx.launch(t, "podman")
			if seeded.failed {
				t.Fatal("seed failed")
			}
			fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 20: "twenty"})
			req := forkRequest(t, fx, "podman")
			req.FreshUpdate = true
			// This declaration is still in the launch's selected vector, as a workspace-only pack is,
			// but is no longer authorized by the independent detached user resolver.
			writeFile(t, filepath.Join(fx.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[]}`)
			sharedPreparationSpawn = func(context.Context, preparationDemand) error {
				t.Error("workspace manifest reached detached execution")
				return errBackgroundFromTest
			}
			entered := make(chan struct{})
			originalChild := forkBuildChild
			if cancelled {
				forkBuildChild = func(ctx context.Context, _ time.Duration, _ string, _ forkBuild, _ captureStreams, _ bool) (int, bool) {
					close(entered)
					<-ctx.Done()
					return 130, false
				}
			}
			defer func() { forkBuildChild = originalChild }()
			var output syncBuffer
			done := make(chan map[string]entrypoint.ForkDelivery, 1)
			go func() { result, _ := runBuildSlot(run.BuildSlotRequest{Forks: &req}, &output, false); done <- result }()
			if cancelled {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("foreground child did not start")
				}
				if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case result := <-done:
				if cancelled {
					if result["tool"].FreshFailure == "" || result["tool"].Key != "" {
						t.Fatalf("cancelled foreground request claimed freshness: %+v", result)
					}
				} else {
					if result["tool"].Key == "" || result["tool"].FreshFailure != "" || result["tool"].Key == seeded.delivery.Key {
						t.Fatalf("workspace foreground route failed: %+v\n%s", result, output.String())
					}
				}
			case <-time.After(5 * time.Second):
				t.Fatal("foreground-only preparation did not complete")
			}
			if !strings.Contains(output.String(), "foreground-only preparation") {
				t.Fatalf("foreground limitation was not disclosed: %s", output.String())
			}
		})
	}
}

func TestSharedDemandUnknownLeaseKeepsChildButRefusesAdmission(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	seeded, _, _ := fx.launch(t, "podman")
	if seeded.failed {
		t.Fatal("seed failed")
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 20: "twenty"})
	f := fx.fork(t)
	identity, err := freshPreparationIdentity(f, patchedTestPlatform)
	if err != nil {
		t.Fatal(err)
	}
	after, err := patchedAdvanceStore(true).CheckBoundary(f.Key())
	if err != nil {
		t.Fatal(err)
	}
	dir := sharedDemandDir(f.Key(), "podman", patchedTestPlatform)
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "request-unknown.json")
	lease, err := pidlock.Acquire(path+".lease", pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	d := preparationDemand{Key: f.Key(), Runtime: "podman", Platform: patchedTestPlatform, Identity: identity, Fresh: true, CheckBoundary: after}
	if err = writeDemandJSON(path, d); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	previousChild := forkBuildChild
	forkBuildChild = func(ctx context.Context, bound time.Duration, staging string, b forkBuild, s captureStreams, color bool) (int, bool) {
		close(entered)
		<-release
		if ctx.Err() != nil {
			t.Error("unknown demand liveness was treated as zero demand")
		}
		return previousChild(ctx, bound, staging, b, s, color)
	}
	defer func() { forkBuildChild = previousChild }()
	done := make(chan error, 1)
	go func() { done <- runSharedPreparation(context.Background(), d, io.Discard) }()
	<-entered
	if err = os.Rename(path+".lease", path+".lease-held"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path+".lease", 0700); err != nil {
		t.Fatal(err)
	}
	// The guard observes the unreadable lease even if the periodic watcher has not ticked.
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("unknown lease dispatch/publication was not refused")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not finish unknown-liveness act")
	}
	if got := fx.record(t).Good.Entry; got != seeded.delivery.Key {
		t.Fatalf("unknown demand admitted %s", got)
	}
	if _, err = os.Stat(path + ".result"); !os.IsNotExist(err) {
		t.Fatalf("unknown demand got a published result: %v", err)
	}
}

func TestSharedDemandSleepingSchedulerLeaseKeepsProducerAfterLastLaunchDetaches(t *testing.T) {
	pf := readyForABackgroundAdvance(t)
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	stubPreparationWorkerStart(t)
	_, _ = pf.launch(t, "podman", nil)
	startAuthenticatedPreparationOwner(t, preparationRoot())
	entered, release, cancelled := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	pf.child = func(ctx context.Context, b forkBuild) int {
		if b.Fork.Key() == poolKeyA {
			close(entered)
			select {
			case <-ctx.Done():
				cancelled <- struct{}{}
				return 130
			case <-release:
			}
		}
		return 0
	}
	f := packload.PatchedTrees(selectConfiguredHostPacks().packs)[0]
	identity, err := freshPreparationIdentity(f, patchedTestPlatform)
	if err != nil {
		t.Fatal(err)
	}
	after, err := patchedAdvanceStore(true).CheckBoundary(f.Key())
	if err != nil {
		t.Fatal(err)
	}
	ctx, detach := context.WithCancel(context.Background())
	defer detach()
	var output syncBuffer
	done := make(chan advanceResult, 1)
	go func() {
		done <- advancePatchedFork(f, advanceOptions{launch: true, runtime: "podman", platform: patchedTestPlatform, ctx: ctx, out: &output, errw: &output, freshCheck: true, freshIdentity: identity, freshBoundary: after})
	}()
	<-entered
	detach()
	if result := <-done; !result.failed {
		t.Fatal("detached launch claimed freshness")
	}
	// No launch remains; the positive scheduler lease and current authorized subscription suffice.
	automatic := preparationDemand{Key: f.Key(), Runtime: "podman", Platform: patchedTestPlatform, Identity: identity}
	if record, err := sharedTimerDemand(context.Background(), automatic); err != nil || record == nil {
		t.Fatalf("timer demand not witnessed: %+v %v", record, err)
	}
	close(release)
	dir := sharedDemandDir(f.Key(), "podman", patchedTestPlatform)
	waitFor(t, "timer-owned producer settled", func() bool { return !pidlock.Held(filepath.Join(dir, "worker.lock")) })
	select {
	case <-cancelled:
		t.Fatal("last launch detach killed timer-owned production")
	default:
	}
	record, err := patchedAdvanceStore(true).LoadCheckRecord(f.Key())
	if err != nil || record.Good == nil || record.Good.Commit != upstreamGit(t, pf.repo, "rev-parse", "HEAD") {
		t.Fatalf("timer-owned admission: good=%+v want=%s err=%v", record.Good, upstreamGit(t, pf.repo, "rev-parse", "HEAD"), err)
	}
}

func TestSharedDemandCancelledRequesterDetachesDuringKeyedBuildLockWait(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	seeded, _, _ := fx.launch(t, "podman")
	if seeded.failed {
		t.Fatal("seed failed")
	}
	tip := fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 20: "twenty"})
	f := fx.fork(t)
	identity, err := freshPreparationIdentity(f, patchedTestPlatform)
	if err != nil {
		t.Fatal(err)
	}
	after, err := patchedAdvanceStore(true).CheckBoundary(f.Key())
	if err != nil {
		t.Fatal(err)
	}
	build := forkBuild{Fork: f, Commit: tip, Platform: patchedTestPlatform, Series: mustSeries(t, fx)}
	lock, err := pidlock.Acquire(build.lockPath(), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	var workerOutput syncBuffer
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	workerDone := make(chan error, 1)
	sharedPreparationSpawn = func(_ context.Context, d preparationDemand) error {
		go func() { workerDone <- runSharedPreparation(workerCtx, d, &workerOutput) }()
		return nil
	}
	ctx, detach := context.WithCancel(context.Background())
	defer detach()
	done := make(chan advanceResult, 1)
	go func() {
		done <- advancePatchedFork(f, advanceOptions{launch: true, runtime: "podman", platform: patchedTestPlatform, ctx: ctx, out: io.Discard, errw: io.Discard, freshCheck: true, freshIdentity: identity, freshBoundary: after})
	}()
	waitFor(t, "owned worker waiting on build lock", func() bool { return strings.Contains(workerOutput.String(), "waiting for pid") })
	detach()
	select {
	case result := <-done:
		if !result.failed || result.delivery.Key != "" {
			t.Fatalf("cancelled lock waiter claimed freshness: %+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("keyed lock blocked requester detach")
	}
	select {
	case <-workerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("zero-demand did not cancel owned keyed lock wait")
	}
	if got := fx.record(t).Good.Entry; got != seeded.delivery.Key {
		t.Fatalf("cancelled keyed lock wait changed admission: %s", got)
	}
	if len(fx.builds) != 1 {
		t.Fatalf("cancelled lock wait started %d builds", len(fx.builds))
	}
}

func TestSharedDemandHostFreshCheckKeepsUsableFloorWithoutRebuildingMissingStoreCopy(t *testing.T) {
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if rc, _, out := fx.hostLaunch(t); rc != 0 {
		t.Fatalf("seed rc=%d\n%s", rc, out)
	}
	good := fx.record(t).Good.Entry
	removeStoreEntry(t, good)
	before := *fx.record(t).Fresh
	builds := len(fx.builds)
	got := captureHostExec(t)
	var output syncBuffer
	if rc := hostMain([]string{"--update", "--", "tool"}, io.Discard, &output, false, nil); rc != 0 || got.target == "" {
		t.Fatalf("fresh installed-copy route rc=%d target=%s\n%s", rc, got.target, output.String())
	}
	if record := fx.record(t); !record.Fresh.Qualifies(before, record.Fresh.InputIdentity) || record.Good.Entry != good || len(fx.builds) != builds {
		t.Fatalf("freshness rebuilt unchanged installed bytes: start=%+v before=%+v builds=%d before=%d good=%+v", record.Fresh, before, len(fx.builds), builds, record.Good)
	}
}
