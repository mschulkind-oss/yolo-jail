package cli

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

// A REQUEST REGISTERED DURING A ZERO-DEMAND TEARDOWN IS NOT ANSWERED WITH THAT CANCEL: the worker's
// monitor cancels production once the last requester leaves, but a launch for the same identity that
// registers while the cancelled child is still tearing down is surviving demand. The worker must
// produce again for it, never write the cancelled run's failure to it. Red if runSharedPreparation
// publishes a zero-demand cancellation's result to a request that was live at the write.
func TestSelectedNPMSharedDemandRegisteredDuringTeardownIsNotAnsweredWithTheCancel(t *testing.T) {
	f := newSelectedNPMFixture(t)
	req := selectedNPMUser(t, f, false)
	workers := selectedNPMSharedWorker(t)
	started := make(chan struct{})
	cancelled := make(chan struct{})
	releaseTeardown := make(chan struct{})
	inner := programCaptureChild
	var calls atomic.Int32
	programCaptureChild = func(ctx context.Context, workspace string, argv []string, w io.Writer) error {
		if calls.Add(1) == 1 {
			// The first producer child holds until the worker cancels it, then holds its teardown
			// open until the test has registered the second launch's demand.
			close(started)
			<-ctx.Done()
			close(cancelled)
			<-releaseTeardown
			return ctx.Err()
		}
		return inner(ctx, workspace, argv, w)
	}
	wait := func(ctx context.Context) error {
		_, err := waitOrdinaryPreparation(ctx, f.in, req.Fingerprint, req.Runtime, req.Platform, false, io.Discard, foregroundOrdinaryGuard(ctx, req, f.in))
		return err
	}
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	firstDone := make(chan error, 1)
	go func() { firstDone <- wait(first) }()
	dir := sharedDemandDir(ordinaryProgramKey(f.in), req.Runtime, req.Platform)
	// Cancel only once production is running: a requester gone before the worker starts its
	// producer leaves nothing to tear down.
	select {
	case <-started:
	case err := <-firstDone:
		t.Fatalf("the first requester returned before its producer started: %v", err)
	case <-time.After(testHangGuard(t)):
		t.Fatal("the worker never started its producer")
	}
	cancelFirst()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first requester: %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(testHangGuard(t)):
		t.Fatal("the worker did not cancel its producer at zero demand")
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- wait(context.Background()) }()
	selectedNPMWaitLeases(t, dir, 1)
	close(releaseTeardown)
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("a launch registered during the teardown was answered with the cancelled run: %v", err)
		}
	case <-time.After(testHangGuard(t)):
		t.Fatal("the second requester was never answered")
	}
	workers.Wait()
	record, err := readNPMCaptureRecord(f.store, f.in)
	if err != nil || record.Entry == "" {
		t.Fatalf("the surviving demand did not admit: %+v %v", record, err)
	}
}

func TestSelectedNPMSharedDemandWithdrawnBetweenMonitorReadsDoesNotAnswerLateDemandWithCancel(t *testing.T) {
	f := newSelectedNPMFixture(t)
	req := selectedNPMUser(t, f, false)
	workers := selectedNPMSharedWorker(t)
	started, snapshot, cancelled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseSnapshot, releaseTeardown := make(chan struct{}), make(chan struct{})
	oldHook := sharedDemandAfterMonitorReadHook
	t.Cleanup(func() { sharedDemandAfterMonitorReadHook = oldHook })
	var paused atomic.Bool
	sharedDemandAfterMonitorReadHook = func(count int) {
		if count == 1 && paused.CompareAndSwap(false, true) {
			close(snapshot)
			<-releaseSnapshot
		}
	}
	inner := programCaptureChild
	var calls atomic.Int32
	programCaptureChild = func(ctx context.Context, workspace string, argv []string, w io.Writer) error {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			close(cancelled)
			<-releaseTeardown
			return ctx.Err()
		}
		return inner(ctx, workspace, argv, w)
	}
	wait := func(ctx context.Context) error {
		_, err := waitOrdinaryPreparation(ctx, f.in, req.Fingerprint, req.Runtime, req.Platform, false, io.Discard, foregroundOrdinaryGuard(ctx, req, f.in))
		return err
	}
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	firstDone := make(chan error, 1)
	go func() { firstDone <- wait(first) }()
	guard := func(event <-chan struct{}, label string) {
		t.Helper()
		select {
		case <-event:
		case <-time.After(testHangGuard(t)):
			t.Fatal("missing controlled event", label)
		}
	}
	guard(started, "first child")
	guard(snapshot, "first nonempty monitor snapshot")
	cancelFirst()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatal("first requester", err)
	}
	dir := sharedDemandDir(ordinaryProgramKey(f.in), req.Runtime, req.Platform)
	selectedNPMWaitLeases(t, dir, 0)
	// Only the monitor's second, authorization read can now cancel the child:
	// its first snapshot is held nonempty, and the parent worker is not cancelled.
	close(releaseSnapshot)
	guard(cancelled, "authorization reread cancellation")
	secondDone := make(chan error, 1)
	go func() { secondDone <- wait(context.Background()) }()
	selectedNPMWaitLeases(t, dir, 1)
	close(releaseTeardown)
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("surviving demand received the cancelled prior batch: %v", err)
		}
	case <-time.After(testHangGuard(t)):
		t.Fatal("late demand was not answered")
	}
	workers.Wait()
	if calls.Load() < 2 {
		t.Fatal("late demand did not get a new producer")
	}
}
