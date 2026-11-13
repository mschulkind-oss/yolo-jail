package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// A liveness probe is not a worker. demandLeaseHeld and pidlock.Held answer "is worker.lock held?"
// by taking it for an instant, and the requester's own wait loop probes it every poll. A worker
// starting during that instant used to read the hold as "another worker owns this demand" and
// exit, and the requester — never having seen a worker — waited out the 20-minute bound
// (TestSelectedNPMRunFrontdoorActualSharedColdWarmFresh hung in the landing gate, and
// TestSharedFreshDemandCancellationDuringFinalStateLockRetry on CI). Here a probe's hold is
// made to overlap the worker's acquisition, so the overlap is not left to timing.
func TestSharedDemandWorkerStartingDuringALivenessProbeStillAnswers(t *testing.T) {
	withHomeAndCwd(t)
	budget := testsupport.ReadinessBudget(t)
	d := preparationDemand{Key: "nopack/tool", Runtime: "podman", Platform: "linux/amd64", Identity: "identity"}
	dir := sharedDemandDir(d.Key, d.Runtime, d.Platform)

	contended := make(chan struct{}, 1)
	previousHook, previousSpawn := sharedDemandWorkerHooks()
	sharedWorkerLockContendedHook = func() {
		select {
		case contended <- struct{}{}:
		default:
		}
	}
	workerDone := make(chan struct{})
	workerCtx, stopWorker := context.WithCancel(context.Background())
	sharedPreparationSpawn = func(_ context.Context, d preparationDemand) error {
		probe, err := pidlock.Acquire(filepath.Join(dir, "worker.lock"), pidlock.NoWait, nil)
		if err != nil {
			return err
		}
		go func() { defer close(workerDone); _ = runSharedPreparation(workerCtx, d, io.Discard) }()
		// The worker either met the probe's hold or (before the fix) gave up on it.
		select {
		case <-contended:
		case <-workerDone:
		case <-time.After(budget):
			t.Error("the worker never tried worker.lock")
		}
		probe.Release()
		return nil
	}
	t.Cleanup(func() {
		stopWorker()
		<-workerDone
		sharedWorkerLockContendedHook, sharedPreparationSpawn = previousHook, previousSpawn
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result, err := waitSharedDemandResult(ctx, d, false, nil)
	if err != nil {
		t.Fatalf("a worker that started during a liveness probe left its requester waiting: %v", err)
	}
	// No user config selects the key: the worker answers with a refusal, which is an outcome.
	if !result.Failed {
		t.Fatalf("worker answered with %+v, want the unauthorized demand's failure", result)
	}
}

// A requester whose worker never takes worker.lock returns, naming the next step, instead of
// waiting out the whole build bound.
func TestSharedDemandRequesterWithNoWorkerStopsAfterTheStartGrace(t *testing.T) {
	withHomeAndCwd(t)
	previousHook, previousSpawn := sharedDemandWorkerHooks()
	previousGrace := sharedWorkerStartGrace
	sharedWorkerStartGrace = 4 * sharedDemandPoll
	sharedPreparationSpawn = func(context.Context, preparationDemand) error { return nil }
	t.Cleanup(func() {
		sharedWorkerLockContendedHook, sharedPreparationSpawn = previousHook, previousSpawn
		sharedWorkerStartGrace = previousGrace
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	d := preparationDemand{Key: "nopack/tool", Runtime: "podman", Platform: "linux/amd64", Identity: "identity"}
	_, err := waitSharedDemandResult(ctx, d, false, nil)
	if err == nil || !strings.Contains(err.Error(), "no preparation worker took this request") || !strings.Contains(err.Error(), "retry the launch") {
		t.Fatalf("requester with no worker: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("requester waited past its own deadline instead of the start grace")
	}
}

func sharedDemandWorkerHooks() (func(), func(context.Context, preparationDemand) error) {
	return sharedWorkerLockContendedHook, sharedPreparationSpawn
}

// The pid a requester names is the worker that holds worker.lock now. A lock file left by an
// earlier worker that exited still holds that worker's pid; naming it told the user they had
// joined a process that was gone (seen in the 2026-11-13 runtime acceptance: "timer joined
// ordinary shared producer pid 39561" named the first launch's exited producer).
func TestSharedDemandRequesterNamesNoExitedWorker(t *testing.T) {
	withHomeAndCwd(t)
	previousHook, previousSpawn := sharedDemandWorkerHooks()
	previousGrace := sharedWorkerStartGrace
	sharedWorkerStartGrace = 4 * sharedDemandPoll
	sharedPreparationSpawn = func(context.Context, preparationDemand) error { return nil }
	t.Cleanup(func() {
		sharedWorkerLockContendedHook, sharedPreparationSpawn = previousHook, previousSpawn
		sharedWorkerStartGrace = previousGrace
	})
	d := preparationDemand{Key: "nopack/tool", Runtime: "podman", Platform: "linux/amd64", Identity: "identity"}
	dir := sharedDemandDir(d.Key, d.Runtime, d.Platform)
	if err := ensurePreparationRoot(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "worker.lock"), []byte("424242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	named := -1
	_, _ = waitSharedDemandResult(ctx, d, false, func(pid int) { named = pid })
	if named != 0 {
		t.Fatalf("requester named pid %d for a worker.lock nobody holds, want 0 (no live worker yet)", named)
	}
}

func TestSharedProducerPhrase(t *testing.T) {
	if got := sharedProducerPhrase(0); got != "its worker is starting" {
		t.Fatalf("sharedProducerPhrase(0) = %q", got)
	}
	if got := sharedProducerPhrase(17); got != "pid 17" {
		t.Fatalf("sharedProducerPhrase(17) = %q", got)
	}
}
