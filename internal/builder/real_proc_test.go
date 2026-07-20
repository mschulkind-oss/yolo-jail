package builder

import (
	"os/exec"
	"testing"
	"time"
)

// TestRealProcPollReportsExit guards the reaping fix: a realProc wrapping a
// short-lived child must eventually report done=true once the child exits.
// Before the fix, Poll() never called Wait(), so ProcessState stayed nil and
// Poll returned done=false forever — making pollUntilReachable's "builder
// process exited early" fast-fail branch dead code.
func TestRealProcPollReportsExit(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 3")
	p, err := newRealProc(cmd)
	if err != nil {
		t.Fatalf("newRealProc: %v", err)
	}

	// Poll until done (bounded) — the reaper goroutine records exit state async.
	deadline := time.Now().Add(5 * time.Second)
	var code int
	var done bool
	for time.Now().Before(deadline) {
		code, done = p.Poll()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !done {
		t.Fatal("Poll never reported done=true after the child exited")
	}
	if code != 3 {
		t.Errorf("Poll exit code = %d, want 3", code)
	}
}

// TestRealProcPollLiveWhileRunning guards that a still-running child reports
// done=false (we must not fast-fail a healthy, booting VM).
func TestRealProcPollLiveWhileRunning(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30")
	p, err := newRealProc(cmd)
	if err != nil {
		t.Fatalf("newRealProc: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	if _, done := p.Poll(); done {
		t.Error("Poll reported done=true for a still-running child")
	}
}
