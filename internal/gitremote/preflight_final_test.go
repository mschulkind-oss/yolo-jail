package gitremote

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Cancellation after completion was committed must not be reported as a
// cancellation of a group that has already passed its last signaling point.
func TestGitPreflightCompletedExitDoesNotResampleDeadlineAfterReap(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexec 1>&- 2>&-\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals, committed := 0, false
	_, err := gitOutputWithReapHook(ctx, bin, func(pid int, sig syscall.Signal) error {
		signals++
		return syscall.Kill(pid, sig)
	}, func() {
		committed = true
		cancel()
	})
	var exit *exec.ExitError
	if !committed || !errors.As(err, &exit) || exit.ExitCode() != 1 || signals != 0 {
		t.Fatalf("completed Git result changed after reap: committed=%v signals=%d error=%v", committed, signals, err)
	}
}

// The injected refusal must never turn a deadline into an unbounded Wait or
// discard the actual signal failure. Teardown alone kills and reaps this owned
// child; production must disclose, not pretend to have terminated it.
func TestGitPreflightFailedGroupSignalReturnsWithLiveLeader(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexec 1>&- 2>&-\nsleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	pid := 0
	t.Cleanup(func() {
		if pid == 0 {
			return
		}
		// The direct child is still alive, retaining the test-owned PGID. Never
		// signal a group after its leader is reaped or its identity has changed.
		if exited, err := gitExitedUnreaped(pid); err == nil && !exited {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		} else if err != nil {
			// The leader is still our unreaped direct child, even if the
			// nonreaping observation itself failed.
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		var status syscall.WaitStatus
		_, _ = syscall.Wait4(pid, &status, 0, nil)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := gitOutputWithGroupSignal(ctx, bin, func(group int, sig syscall.Signal) error {
		pid = -group
		return syscall.EPERM
	})
	elapsed := time.Since(start)
	var denied *GitGroupTerminationError
	if !errors.As(err, &denied) || denied.PID != pid || denied.BirthIdentity == "" || denied.SignalErr != syscall.EPERM {
		t.Fatalf("missing typed denied group termination: pid=%d err=%v", pid, err)
	}
	if pid == 0 || elapsed > time.Second || !errors.Is(err, syscall.EPERM) || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "last observed live") || !strings.Contains(err.Error(), "birth identity ") || !strings.Contains(err.Error(), "unreaped zombie") {
		t.Fatalf("denied termination must return bounded, truthful error: pid=%d elapsed=%s err=%v", pid, elapsed, err)
	}
	if exited, probeErr := gitExitedUnreaped(pid); probeErr != nil || exited {
		t.Fatalf("fixture Git must still be live after denied signal: exited=%v err=%v", exited, probeErr)
	}
}
