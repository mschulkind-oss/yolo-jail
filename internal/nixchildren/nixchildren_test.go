package nixchildren

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// awaitRunning waits for s to hold n running children.
func awaitRunning(t *testing.T, s *Set, n int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if got := s.Running(); got == n {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("%d nix children tracked, want %d", got, n)
		}
	}
}

// trapsInterrupt is a stand-in nix that runs until interrupted and says so on stderr.
const trapsInterrupt = `trap 'echo interrupted >&2; exit 130' INT; echo started >&2; while :; do sleep 0.05; done`

// TestStopKillsANixThatIgnoresTheInterrupt: past the grace, a nix still running is killed rather
// than left behind.
func TestStopKillsANixThatIgnoresTheInterrupt(t *testing.T) {
	s := Isolate(t)
	done := make(chan error, 1)
	go func() { done <- Run(exec.Command("sh", "-c", `trap '' INT; while :; do sleep 0.05; done`)) }()
	awaitRunning(t, s, 1)
	s.stop(200*time.Millisecond, nil)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a nix that ignored its interrupt was still running ten seconds after the stop")
	}
}

// TestNoNixStartsAfterTheStop: a stop is not only for what runs when it is called. The launch's
// main goroutine goes on while the signal's teardown runs, and an eval the stop cut short falls
// through to a build, so that build must not start at all.
func TestNoNixStartsAfterTheStop(t *testing.T) {
	s := Isolate(t)
	s.stop(time.Second, nil)
	ran := filepath.Join(t.TempDir(), "ran")
	if err := Run(exec.Command("sh", "-c", "touch "+ran)); !errors.Is(err, ErrStopped) {
		t.Errorf("Run after the stop returned %v, want ErrStopped", err)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("Run started a nix after the stop")
	}
}

// TestAStoppedNixsCallerWaitsForTheExit: the goroutine whose nix a stop cut short runs the stop's
// hand-off before its Run returns, so it cannot go on to report a failed build while the teardown
// that stopped it exits. The stop itself does not wait on that hand-off.
func TestAStoppedNixsCallerWaitsForTheExit(t *testing.T) {
	s := Isolate(t)
	returned := make(chan error, 1)
	go func() { returned <- Run(exec.Command("sh", "-c", trapsInterrupt)) }()
	awaitRunning(t, s, 1)
	exited, handedOff := make(chan struct{}), make(chan struct{}, 1)
	stopped := make(chan struct{})
	go func() {
		s.stop(10*time.Second, func() { handedOff <- struct{}{}; <-exited })
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("the stop waited on its own hand-off: the teardown would never reach its exit")
	}
	select {
	case <-handedOff:
	case <-time.After(10 * time.Second):
		t.Fatal("the stopped nix's caller never ran the stop's hand-off")
	}
	select {
	case err := <-returned:
		t.Fatalf("Run returned (%v) before the exit it was handed off to, free to report a "+
			"failed build the signal caused", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(exited)
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return once the exit it waited for came")
	}
}

// TestANixRefusedAfterTheStopIsNotHandedOff: a start the stop refuses returns at once. The stop is
// the teardown's first act, so the teardown's goroutine holds no nix to release, but it is the one
// goroutine that could still ask to start one, and waiting there for its own exit would never end.
func TestANixRefusedAfterTheStopIsNotHandedOff(t *testing.T) {
	s := Isolate(t)
	s.stop(time.Second, func() { select {} })
	done := make(chan error, 1)
	go func() { done <- Run(exec.Command("true")) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrStopped) {
			t.Errorf("Run after the stop returned %v, want ErrStopped", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a start the stop refused waited on the hand-off")
	}
}
