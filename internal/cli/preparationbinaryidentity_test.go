package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// preparationIdentityFixture records a worker executable and its digest, injects the scheduler's
// control ticks, counts the hashes, and starts the production loop.
func preparationIdentityFixture(t *testing.T) (executable string, control chan time.Time, hashes *atomic.Int32, done chan error, cancel context.CancelFunc) {
	t.Helper()
	newPoolFixture(t)
	executable = filepath.Join(t.TempDir(), "yolo")
	if err := os.WriteFile(executable, []byte("selected binary v1"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := preparationBinaryDigest(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := withPreparationState(func(state *preparationState) error {
		state.Worker = preparationWorker{Executable: executable, BinaryDigest: digest}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	hashes = &atomic.Int32{}
	previousDigest := preparationBinaryDigest
	preparationBinaryDigest = func(path string) (string, error) {
		hashes.Add(1)
		return previousDigest(path)
	}
	t.Cleanup(func() { preparationBinaryDigest = previousDigest })
	control = make(chan time.Time)
	previousTicker := preparationControlTicker
	preparationControlTicker = func() (<-chan time.Time, func()) { return control, func() {} }
	t.Cleanup(func() { preparationControlTicker = previousTicker })

	ctx, cancelCtx := context.WithCancel(context.Background())
	t.Cleanup(cancelCtx)
	done = make(chan error, 1)
	go func() { done <- runPreparationScheduler(ctx, make(chan time.Time), io.Discard) }()
	return executable, control, hashes, done, cancelCtx
}

// tickControl delivers one control tick; the loop is single-threaded, so a later delivery returns
// only once every earlier tick was fully handled.
func tickControl(t *testing.T, control chan time.Time, done chan error) {
	t.Helper()
	select {
	case control <- time.Now():
	case err := <-done:
		t.Fatalf("the scheduler stopped before a control tick: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the scheduler never took a control tick")
	}
}

// The every-second control check must not hash the whole executable on every tick: an
// unchanged stat identity (device, inode, size, mtime) skips the digest.
func TestPreparationControlTicksHashAnUnchangedBinaryOnce(t *testing.T) {
	_, control, hashes, done, cancel := preparationIdentityFixture(t)
	for i := 0; i < 6; i++ {
		tickControl(t, control, done)
	}
	tickControl(t, control, done) // the sixth tick is handled once this one is taken
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if got := hashes.Load(); got != 1 {
		t.Errorf("seven control ticks over an unchanged binary hashed it %d times, want once", got)
	}
}

// A worker that stops while a plain build is in flight relabels it as it does a check: status
// must not keep showing "building" for an act nothing runs.
func TestPreparationSchedulerFinishInterruptsABuildInFlight(t *testing.T) {
	state := preparationState{Subscriptions: []preparationRecord{
		{Key: "plain", Kind: preparationPlainProgramKind, Active: true, Phase: "building"},
		{Key: "tree", Active: true, Phase: "checking"},
		{Key: "done", Active: true, Phase: "admitted"},
	}}
	finishPreparationSchedulerState(&state, context.Canceled)
	for i, want := range []string{"interrupted", "interrupted", "admitted"} {
		if got := state.Subscriptions[i].Phase; got != want {
			t.Errorf("%s after the worker stopped = %q, want %q", state.Subscriptions[i].Key, got, want)
		}
	}
}

// A real replacement (a new inode renamed over the path, with the same size and mtime) is still
// detected: the stat identity changed, so the digest — the authority — is consulted again.
func TestPreparationControlTickDetectsAReplacedBinary(t *testing.T) {
	executable, control, hashes, done, _ := preparationIdentityFixture(t)
	tickControl(t, control, done)
	tickControl(t, control, done)
	before, err := os.Stat(executable)
	if err != nil {
		t.Fatal(err)
	}
	replacement := executable + ".new"
	if err := os.WriteFile(replacement, []byte("selected binary v2"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, executable); err != nil {
		t.Fatal(err)
	}
	tickControl(t, control, done)
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "binary changed") {
			t.Fatalf("a replaced binary ended the scheduler with %v, want the binary-changed refusal", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the scheduler kept running on a replaced binary")
	}
	if hashes.Load() < 2 {
		t.Errorf("the replacement was not re-hashed: %d hashes", hashes.Load())
	}
}
