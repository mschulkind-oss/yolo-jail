package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

func preparationFixture(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "scheduler")
	exe := filepath.Join(t.TempDir(), "yolo")
	if err := os.WriteFile(exe, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, exe
}
func waitPreparationOwner(root string, done <-chan error, timeout time.Duration) (preparationWorker, error) {
	deadline := time.Now().Add(timeout)
	lastStatus := preparationUnknown
	var lastProbeErr error
	for time.Now().Before(deadline) {
		select {
		case err, ok := <-done:
			if !ok || err == nil {
				return preparationWorker{}, errors.New("worker exited before readiness without an error")
			}
			return preparationWorker{}, fmt.Errorf("worker exited before readiness: %w", err)
		default:
		}

		record, status, err := probePreparationWorker(root)
		lastStatus, lastProbeErr = status, err
		if status == preparationAlive {
			return record, nil
		}
		select {
		case workerErr, ok := <-done:
			if !ok || workerErr == nil {
				return preparationWorker{}, errors.New("worker exited before readiness without an error")
			}
			return preparationWorker{}, fmt.Errorf("worker exited before readiness: %w", workerErr)
		default:
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err, ok := <-done:
		if !ok || err == nil {
			return preparationWorker{}, errors.New("worker exited before readiness without an error")
		}
		return preparationWorker{}, fmt.Errorf("worker exited before readiness: %w", err)
	default:
	}
	return preparationWorker{}, fmt.Errorf("worker not ready after %s (last probe status=%s, error=%v)", timeout, lastStatus, lastProbeErr)
}

func TestPreparationReadinessReportsStartupFailure(t *testing.T) {
	root, exe := preparationFixture(t)
	if err := os.Remove(exe); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		defer close(done)
		done <- runPreparationWorker(ctx, root, exe, nil, func(context.Context) error { return nil })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("startup worker did not exit")
		}
	})
	_, err := waitPreparationOwner(root, done, 3*time.Second)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("readiness error %v does not preserve the startup failure", err)
	}
}

func TestPreparationReadinessTimeoutRetainsProbeDiagnostic(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scheduler")
	_, err := waitPreparationOwner(root, make(chan error), 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "last probe status=gone") || !strings.Contains(err.Error(), "error=<nil>") {
		t.Fatalf("timeout did not retain last probe status and error: %v", err)
	}
}

func awaitPreparationOwner(t *testing.T, root string, done <-chan error, cancel context.CancelFunc) preparationWorker {
	t.Helper()
	t.Cleanup(func() {
		cancel()
		select {
		case err, ok := <-done:
			if ok && err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("preparation worker cleanup: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("preparation worker did not exit after cancellation")
		}
	})
	record, err := waitPreparationOwner(root, done, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return record
}
func TestPreparationWakeStopAndIncarnation(t *testing.T) {
	root, exe := preparationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	started := make(chan struct{}, 2)
	done := make(chan error, 1)
	go func() {
		defer close(done)
		done <- runPreparationWorker(ctx, root, exe, ticks, func(ctx context.Context) error { started <- struct{}{}; <-ctx.Done(); return ctx.Err() })
	}()
	r := awaitPreparationOwner(t, root, done, cancel)
	if err := controlPreparationWorker(ctx, root, "stale", "stop"); err == nil {
		t.Fatal("stale incarnation accepted")
	}
	if err := controlPreparationWorker(ctx, root, r.Identity, "wake"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("wake did not dispatch")
	}
	if err := controlPreparationWorker(ctx, root, r.Identity, "stop"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel owned work")
	}
	_, s, err := probePreparationWorker(root)
	if err != nil || s != preparationGone {
		t.Fatalf("%s %v", s, err)
	}
	select {
	case <-started:
		t.Fatal("stop restarted")
	default:
	}
}
func TestPreparationReplacementDrainsAndBlocksDispatch(t *testing.T) {
	root, exe := preparationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		defer close(done)
		done <- runPreparationWorker(ctx, root, exe, ticks, func(context.Context) error { started <- struct{}{}; <-release; return nil })
	}()
	r := awaitPreparationOwner(t, root, done, cancel)
	ticks <- time.Now()
	<-started
	if err := controlPreparationWorker(ctx, root, r.Identity, "replace"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		t.Fatal("replacement did not drain")
	default:
	}
	if err := controlPreparationWorker(ctx, root, r.Identity, "wake"); err == nil {
		t.Fatal("draining owner accepted wake")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
		t.Fatal("dispatched after replacement")
	default:
	}
}
func TestPreparationHeldUnknownNeverTakesOver(t *testing.T) {
	root, exe := preparationFixture(t)
	lock, err := pidlock.Acquire(filepath.Join(root, "worker.lock"), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if err := writePreparationJSON(filepath.Join(root, "scheduler.json"), preparationWorker{Identity: "stale", PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	_, s, err := probePreparationWorker(root)
	if s != preparationUnknown || err == nil {
		t.Fatalf("%s %v", s, err)
	}
	if err := runPreparationWorker(context.Background(), root, exe, nil, func(context.Context) error { t.Fatal("unknown dispatched"); return nil }); !errors.Is(err, pidlock.ErrHeld) {
		t.Fatalf("%v", err)
	}
	if err := controlPreparationWorker(context.Background(), root, "stale", "stop"); err == nil {
		t.Fatal("unreachable stop accepted")
	}
}
func TestPreparationTimerChecksSelectedExecutable(t *testing.T) {
	root, exe := preparationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	done := make(chan error, 1)
	calls := make(chan struct{}, 2)
	go func() {
		defer close(done)
		done <- runPreparationWorker(ctx, root, exe, ticks, func(context.Context) error { calls <- struct{}{}; return nil })
	}()
	awaitPreparationOwner(t, root, done, cancel)
	ticks <- time.Now()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("timer callsite missing")
	}
	if err := os.WriteFile(exe, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	ticks <- time.Now()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("missing identity evidence")
		}
	case <-time.After(time.Second):
		t.Fatal("replacement did not exit")
	}
	select {
	case <-calls:
		t.Fatal("replaced binary dispatched")
	default:
	}
}
func TestPreparationStatusReadOnlyStaleOwner(t *testing.T) {
	root, _ := preparationFixture(t)
	lock, err := pidlock.Acquire(filepath.Join(root, "worker.lock"), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	lock.Release()
	record := filepath.Join(root, "scheduler.json")
	if err := writePreparationJSON(record, preparationWorker{Identity: "dead", PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, "worker.lock"))
	owner, _ := os.ReadFile(record)
	_, s, err := probePreparationWorker(root)
	if err != nil || s != preparationGone {
		t.Fatalf("%s %v", s, err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "worker.lock"))
	retained, _ := os.ReadFile(record)
	if string(before) != string(after) || string(owner) != string(retained) {
		t.Fatal("status mutated state")
	}
}

// preparationTestGuard is a failure guard only: it turns a hung worker into a red test, and
// no assertion depends on beating it.
const preparationTestGuard = time.Minute

// generousPreparationControl raises the control exchange's bound to preparationTestGuard for
// the rest of the test, so an answer asserted on is the owner's and never the clock's.
func generousPreparationControl(t *testing.T) {
	t.Helper()
	saved := preparationControlTimeout
	preparationControlTimeout = preparationTestGuard
	t.Cleanup(func() { preparationControlTimeout = saved })
}

// awaitPreparationExit is the worker's exit, guarded against a hang.
func awaitPreparationExit(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(preparationTestGuard):
		t.Fatal("preparation worker did not exit")
		return nil
	}
}

// startReadyPreparationOwner runs a worker and returns once the worker itself says it is
// ready (runPreparationWorkerReady), not once a probe polled against a clock happens to see it.
func startReadyPreparationOwner(t *testing.T, root, exe string) (preparationWorker, <-chan error, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan preparationWorker, 1)
	done := make(chan error, 1)
	go func() {
		defer close(done)
		done <- runPreparationWorkerReady(ctx, root, exe, nil, func(context.Context) error { return nil },
			func(r preparationWorker) { ready <- r })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err, ok := <-done:
			if ok && err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("preparation worker cleanup: %v", err)
			}
		case <-time.After(preparationTestGuard):
			t.Error("preparation worker did not exit after cancellation")
		}
	})
	select {
	case r := <-ready:
		return r, done, cancel
	case err := <-done:
		t.Fatalf("worker exited before readiness: %v", err)
	case <-time.After(preparationTestGuard):
		t.Fatal("worker never reported ready")
	}
	return preparationWorker{}, nil, nil
}

func TestPreparationNewLeaseRejectsPriorIdentity(t *testing.T) {
	generousPreparationControl(t)
	root, exe := preparationFixture(t)
	first, done, cancel := startReadyPreparationOwner(t, root, exe)
	cancel()
	if err := awaitPreparationExit(t, done); err != nil {
		t.Fatal(err)
	}
	second, done, cancel := startReadyPreparationOwner(t, root, exe)
	defer cancel()
	if first.Identity == second.Identity {
		t.Fatal("lease incarnation reused")
	}
	if err := controlPreparationWorker(context.Background(), root, first.Identity, "stop"); err == nil {
		t.Fatal("stale owner stopped replacement")
	}
	if err := controlPreparationWorker(context.Background(), root, second.Identity, "status"); err != nil {
		t.Fatal(err)
	}
	if err := controlPreparationWorker(context.Background(), root, second.Identity, "stop"); err != nil {
		t.Fatal(err)
	}
	if err := awaitPreparationExit(t, done); err != nil {
		t.Fatal(err)
	}
}
func TestPreparationConcurrentLeaseDoesNotDispatch(t *testing.T) {
	root, exe := preparationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		defer close(done)
		done <- runPreparationWorker(ctx, root, exe, nil, func(context.Context) error { return nil })
	}()
	r := awaitPreparationOwner(t, root, done, cancel)
	if err := runPreparationWorker(ctx, root, exe, nil, func(context.Context) error { t.Fatal("duplicate dispatch"); return nil }); !errors.Is(err, pidlock.ErrHeld) {
		t.Fatalf("%v", err)
	}
	if err := controlPreparationWorker(ctx, root, r.Identity, "stop"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestPreparationUnreachableControlIsBoundedAndRetainsEvidence(t *testing.T) {
	root, _ := preparationFixture(t)
	lock, err := pidlock.Acquire(filepath.Join(root, "worker.lock"), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	record := filepath.Join(root, "scheduler.json")
	if err := writePreparationJSON(record, preparationWorker{Identity: "unknown", PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "control.endpoint"), []byte("malformed"), 0600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, s, err := probePreparationWorker(root)
	if err == nil || s != preparationUnknown || time.Since(started) > 2*time.Second {
		t.Fatalf("%s %v", s, err)
	}
	if _, err := os.Stat(record); err != nil {
		t.Fatal("owner evidence removed")
	}
}

func TestPreparationUnreadableExecutableStopsBeforeDispatch(t *testing.T) {
	root, exe := preparationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	done := make(chan error, 1)
	go func() {
		defer close(done)
		done <- runPreparationWorker(ctx, root, exe, ticks, func(context.Context) error { t.Error("unproven executable dispatched"); return nil })
	}()
	awaitPreparationOwner(t, root, done, cancel)
	if err := os.Remove(exe); err != nil {
		t.Fatal(err)
	}
	ticks <- time.Now()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("unreadability not reported")
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}

func TestPreparationSlowAuthenticatedClientDoesNotDelayStop(t *testing.T) {
	root, exe := preparationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	done := make(chan error, 1)
	go func() {
		defer close(done)
		done <- runPreparationWorker(ctx, root, exe, ticks, func(context.Context) error { return nil })
	}()
	owner := awaitPreparationOwner(t, root, done, cancel)
	slow, err := svcendpoint.DialLocal(filepath.Join(root, "control.endpoint"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	// Authenticate, then hold the application request open without a complete JSON command.
	if _, err = slow.Write([]byte(`{"Identity":`)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := controlPreparationWorker(ctx, root, owner.Identity, "stop"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > preparationControlBound/2 {
		t.Fatalf("slow client serialized stop: %v", elapsed)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func startAuthenticatedPreparationOwner(t *testing.T, root string) preparationWorker {
	t.Helper()
	_, exe := preparationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ticks := make(chan time.Time)
	go func() {
		defer close(done)
		done <- runPreparationWorker(ctx, root, exe, ticks, func(context.Context) error { return nil })
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	})
	return awaitPreparationOwner(t, root, done, cancel)
}
