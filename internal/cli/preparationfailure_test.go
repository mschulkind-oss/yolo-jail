package cli

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

func TestPreparationActualFailureReasonAndBackoffSurviveIdleTicks(t *testing.T) {
	pf := readyForABackgroundAdvance(t)
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	stubPreparationWorkerStart(t)
	var builds atomic.Int32
	pf.child = func(context.Context, forkBuild) int { builds.Add(1); return 1 }
	_, _ = pf.launch(t, "podman", nil)
	ctx, stop := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	done := make(chan error, 1)
	go func() { done <- runPreparationScheduler(ctx, ticks, io.Discard) }()
	ticks <- pf.now
	// The wait is on both keys' failure records; the guard only turns a hung scheduler into a red
	// test. A fixed five seconds failed two healthy failing builds under load.
	deadline := time.After(testHangGuard(t))
	for {
		a, _ := readPreparationSnapshot(poolKeyA)
		b, _ := readPreparationSnapshot(poolKeyB)
		if a.Phase == "failed" && b.Phase == "failed" && a.Reason != "" && b.Reason != "" && !a.RetryAt.IsZero() && !b.RetryAt.IsZero() {
			break
		}
		select {
		case <-deadline:
			stop()
			<-done
			t.Fatalf("actual failure/backoff absent: a=%+v b=%+v", a, b)
		case <-time.After(10 * time.Millisecond):
		}
	}
	before, _ := readPreparationSnapshot(poolKeyA)
	ticks <- pf.now
	ticks <- pf.now
	ticks <- pf.now
	stop()
	<-done
	after, _ := readPreparationSnapshot(poolKeyA)
	if builds.Load() != 2 || before.Reason != after.Reason || before.Generation != after.Generation || !before.RetryAt.Equal(after.RetryAt) {
		t.Fatalf("idle refresh lost failure/backoff or retried early: builds=%d before=%+v after=%+v", builds.Load(), before, after)
	}
}
