//go:build linux

package callbroker

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The injected EPERM models a failed signal, not an OS privilege claim. Kill
// only this test's still-identical leader/group after observing the diagnostic.
func injectFailedOwnedGroupSignal(t *testing.T) func() {
	t.Helper()
	original := signalOwnedGroup
	pid, birth := 0, ""
	signalOwnedGroup = func(pgid int, sig syscall.Signal) error {
		pid = -pgid
		var err error
		birth, err = ownedLeaderIdentity(pid)
		if err != nil {
			t.Errorf("test-owned leader birth: %v", err)
		}
		return syscall.EPERM
	}
	return func() {
		signalOwnedGroup = original
		if pid == 0 || birth == "" {
			t.Error("fixture did not reach group signal")
			return
		}
		current, err := ownedLeaderIdentity(pid)
		if err != nil || current != birth {
			t.Errorf("cannot safely stop test-owned group PID %d: birth=%q now=%q err=%v", pid, birth, current, err)
			return
		}
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
			t.Errorf("stop test-owned group PID %d: %v", pid, err)
		}
	}
}

func TestCanonicalCLIExecutorKeepsFailedGroupSignalDiagnosticBeforeOutputCap(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "overflowed")
	binary := filepath.Join(t.TempDir(), "fixture")
	body := fmt.Sprintf("#!/bin/sh\nhead -c 33554433 /dev/zero | tr '\\000' x\nprintf done > %q\nwhile :; do printf synthetic-secret; sleep 0.01; done\n", marker)
	if err := os.WriteFile(binary, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := pinCLI(binary, nil)
	if err != nil {
		t.Fatal(err)
	}
	stop := injectFailedOwnedGroupSignal(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	type outcome struct {
		result CLIResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := p.run(ctx, Classification{Argv: []string{"write"}, StdinSHA256: fmt.Sprintf("%x", sha256.Sum256(nil))}, nil)
		finished <- outcome{result, err}
	}()
	// The fixture wrote more than 32 MiB before the signal fails; this makes
	// the ordering assertion independent of machine output throughput.
waitForOverflow:
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Error(err)
			break
		}
		select {
		case <-ctx.Done():
			t.Error("fixture did not finish overflowing before deadline")
			break waitForOverflow
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	got := <-finished
	if err := got.err; err == nil || !strings.Contains(err.Error(), "group signal failed (operation not permitted)") ||
		!strings.Contains(err.Error(), "without confirmed termination") || strings.Contains(err.Error(), "32 MiB") ||
		strings.Contains(err.Error(), "synthetic-secret") || len(got.result.Stdout) != 0 || len(got.result.Stderr) != 0 {
		t.Fatalf("failed signal must precede response cap/redaction: stdout=%d stderr=%d err=%v", len(got.result.Stdout), len(got.result.Stderr), err)
	}
}

// TestCanonicalCLIExecutorDoesNotReadOutputWhenLeaderObservationAndSignalFail: when the
// leader's exit cannot be observed and its group cannot be signalled, the call fails closed
// without waiting on the leader or reading its output. The leader blocks on hold, which
// nothing writes, so a call that waited for it would never return: returning at all is the
// proof, and the guard only turns that hang into a red test. Observation fails only once the
// fixture has written its secret output, the started FIFO's event.
func TestCanonicalCLIExecutorDoesNotReadOutputWhenLeaderObservationAndSignalFail(t *testing.T) {
	started := fixtureFIFO(t, "started")
	hold := fixtureFIFO(t, "hold")
	fixtureStarted := awaitFIFO(started)
	binary := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(binary, []byte(fmt.Sprintf("#!/bin/sh\nprintf synthetic-secret\nprintf started > %q\nread x < %q\n", started, hold)), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := pinCLI(binary, nil)
	if err != nil {
		t.Fatal(err)
	}
	stop := injectFailedOwnedGroupSignal(t)
	defer stop()
	originalObserve := observeOwnedLeader
	observeOwnedLeader = func(pid int) (bool, error) {
		select {
		case <-fixtureStarted:
		case <-time.After(testEventGuard):
			t.Error("fixture did not write before malformed process observation")
		}
		return false, errors.New("malformed process observation")
	}
	defer func() { observeOwnedLeader = originalObserve }()
	type outcome struct {
		result CLIResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := p.run(context.Background(), Classification{Argv: []string{"write"}, StdinSHA256: fmt.Sprintf("%x", sha256.Sum256(nil))}, nil)
		finished <- outcome{result, err}
	}()
	var got outcome
	select {
	case got = <-finished:
	case <-time.After(testEventGuard):
		t.Fatal("malformed observation did not fail closed: the call waited on its live leader")
	}
	if err := got.err; err == nil || !strings.Contains(err.Error(), "leader exit could not be observed; process handle released") ||
		!strings.Contains(err.Error(), "group signal failed (operation not permitted)") ||
		strings.Contains(err.Error(), "synthetic-secret") || len(got.result.Stdout) != 0 || len(got.result.Stderr) != 0 {
		t.Fatalf("malformed observation did not fail closed: stdout=%d stderr=%d err=%v", len(got.result.Stdout), len(got.result.Stderr), err)
	}
}

func TestCanonicalCLIExecutorRetainsUncertainDescendantAfterDeadLeaderSignalFailure(t *testing.T) {
	childFile := filepath.Join(t.TempDir(), "child-pid")
	binary := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(binary, []byte(fmt.Sprintf("#!/bin/sh\nsleep 5 &\necho $! > %q\n", childFile)), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := pinCLI(binary, nil)
	if err != nil {
		t.Fatal(err)
	}
	childPID, childBirth := 0, ""
	original := signalOwnedGroup
	signalOwnedGroup = func(_ int, _ syscall.Signal) error {
		b, err := os.ReadFile(childFile)
		if err == nil {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		if err == nil {
			childBirth, err = ownedLeaderIdentity(childPID)
		}
		if err != nil {
			t.Errorf("record test-owned descendant identity: %v", err)
		}
		return syscall.EPERM
	}
	defer func() {
		signalOwnedGroup = original
		if childPID == 0 || childBirth == "" {
			t.Error("test-owned descendant identity missing")
			return
		}
		current, err := ownedLeaderIdentity(childPID)
		if err != nil || current != childBirth {
			t.Errorf("cannot safely stop test-owned descendant PID %d: birth=%q now=%q err=%v", childPID, childBirth, current, err)
			return
		}
		if err := syscall.Kill(childPID, syscall.SIGKILL); err != nil {
			t.Errorf("stop test-owned descendant: %v", err)
		}
	}()
	result, err := p.run(context.Background(), Classification{Argv: []string{"write"}, StdinSHA256: fmt.Sprintf("%x", sha256.Sum256(nil))}, nil)
	if err == nil || !strings.Contains(err.Error(), "leader exited and was reaped; descendants may remain") ||
		!strings.Contains(err.Error(), "group signal failed (operation not permitted)") || len(result.Stdout) != 0 {
		t.Fatalf("dead-leader signal failure lost warning: stdout=%d err=%v", len(result.Stdout), err)
	}
}

func TestPackCLIClassifierKeepsFailedGroupSignalDiagnosticOnCancellation(t *testing.T) {
	classifier := fixtureClassifier(t, "while :; do printf x; sleep 0.01; done")
	stop := injectFailedOwnedGroupSignal(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, err := classifier.ClassifyCLI(ctx, []string{"write"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "group signal failed (operation not permitted)") ||
		!strings.Contains(err.Error(), "without confirmed termination") {
		t.Fatalf("classifier hid failed signal: %v", err)
	}
}
