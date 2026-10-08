package heldchildren

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// reset puts the package back to its production state for one test.
func reset(t *testing.T) {
	t.Helper()
	mu.Lock()
	enabled, held = false, nil
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		enabled, held = false, nil
		mu.Unlock()
	})
}

// start runs argv as a reaped child and returns it with the channel its exit closes.
func start(t *testing.T, argv ...string) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command(argv[0], argv[1:]...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	return cmd, done
}

func gone(done <-chan struct{}, within time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(within):
		return false
	}
}

// TestHoldIsANoOpUntilEnabled: production never enables the package, so a spawn there
// leaves nothing held for anything to stop.
func TestHoldIsANoOpUntilEnabled(t *testing.T) {
	reset(t)
	cmd, done := start(t, "sleep", "60")
	Hold(cmd.Process, done)
	if Stop(cmd.Process.Pid, time.Second) {
		t.Fatal("Stop found a child held before Enable")
	}
	if gone(done, 200*time.Millisecond) {
		t.Fatal("a child held before Enable was stopped")
	}
}

// TestStopSignalsOnlyAHeldPID: a PID this process does not hold is never signalled, even
// one that is this process's own child; a held one is stopped by SIGTERM.
func TestStopSignalsOnlyAHeldPID(t *testing.T) {
	reset(t)
	Enable()
	unheld, unheldDone := start(t, "sleep", "60")
	heldCmd, heldDone := start(t, "sleep", "60")
	Hold(heldCmd.Process, heldDone)

	if Stop(unheld.Process.Pid, time.Second) {
		t.Errorf("Stop reported holding pid %d, which was never held", unheld.Process.Pid)
	}
	if !Stop(heldCmd.Process.Pid, 2*time.Second) {
		t.Errorf("Stop did not find the held pid %d", heldCmd.Process.Pid)
	}
	if !gone(heldDone, 5*time.Second) {
		t.Errorf("the held child survived Stop")
	}
	if gone(unheldDone, 200*time.Millisecond) {
		t.Errorf("the unheld child was stopped")
	}
	if Stop(heldCmd.Process.Pid, time.Second) {
		t.Errorf("a stopped child is still held")
	}
}

// TestStopAllKillsAChildThatIgnoresSIGTERM: past the grace, SIGKILL.
func TestStopAllKillsAChildThatIgnoresSIGTERM(t *testing.T) {
	reset(t)
	Enable()
	// The trap is set before the shell reports ready, so the SIGTERM cannot land first.
	cmd := exec.Command("sh", "-c", "trap '' TERM; echo ready; while :; do sleep 1; done")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := out.Read(make([]byte, 6)); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var status error
	go func() { status = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	Hold(cmd.Process, done)

	StopAll(300 * time.Millisecond)

	if !gone(done, time.Second) {
		t.Fatal("a child ignoring SIGTERM survived StopAll")
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); !ok || ws.Signal() != syscall.SIGKILL {
		t.Errorf("the child ended with %v, want SIGKILL", status)
	}
}
