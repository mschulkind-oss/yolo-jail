package runtime

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A podman binary still open for writing — a package upgrade replacing it in place — cannot be
// executed (ETXTBSY), and that clears the moment the writer is done. On the REAL runner: the
// first attempt fails to start with ETXTBSY, the gate retries it instead of refusing, and the
// attempt after the writer closes answers (PR-D23). Linux only: other kernels execute a file
// open for writing, so there is no ETXTBSY to reproduce.
func TestABinaryBusyBeingWrittenIsRetriedNotRefused(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "podman")
	w, err := os.OpenFile(bin, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString("#!/bin/sh\necho '{\"host\":{}}'\n"); err != nil {
		t.Fatal(err)
	}
	var first Attempt
	calls := 0
	res := WaitForPodman([]string{bin, "info", "--format", "json"}, PodmanReadyBudget, ReadySeams{
		Attempt: func(argv []string, d time.Time, i <-chan struct{}) Attempt {
			calls++
			a := RunPodmanAttempt(argv, d, i)
			if calls == 1 {
				first = a
			}
			return a
		},
		// The backoff is where the upgrade finishes: the writer closes the binary.
		Sleep: func(time.Duration, <-chan struct{}) bool {
			_ = w.Close()
			return true
		},
	}, ReadyHooks{})
	_ = w.Close()
	if !errors.Is(first.StartErr, syscall.ETXTBSY) {
		t.Fatalf("the first attempt was %+v, want a start error of ETXTBSY", first)
	}
	if res.Outcome != PodmanReady {
		t.Fatalf("outcome=%v after %d attempts: a binary busy being written refused the launch "+
			"(%s)", res.Outcome, calls, res.Refusal("podman"))
	}
}
