package nixchildren

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
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

// catchSignal keeps sig from ending the test binary when no arm is installed to catch it, which is
// the defect's own shape: without this a red run would kill the whole package's run.
func catchSignal(t *testing.T, sig os.Signal) {
	t.Helper()
	catch := make(chan os.Signal, 4)
	signal.Notify(catch, sig)
	t.Cleanup(func() { signal.Stop(catch) })
}

// awaitExit reads the status StopOnSignal's teardown ended the process with, or fails.
func awaitExit(t *testing.T, s *Set) int {
	t.Helper()
	select {
	case code := <-s.Exited():
		return code
	case <-time.After(15 * time.Second):
		t.Fatal("the signal did not end the process")
		return 0
	}
}

// TestASignalStopsTheNixAndEndsTheProcess: a signal sent to this process alone, under the arm,
// interrupts the nix it has running and then ends the process 128+N. Without the arm the signal's
// default action ended the process at once, and the nix ran on with no parent.
func TestASignalStopsTheNixAndEndsTheProcess(t *testing.T) {
	s := Isolate(t)
	catchSignal(t, syscall.SIGTERM)
	disarm := StopOnSignal()
	defer disarm()
	var stderr strings.Builder
	returned := make(chan error, 1)
	go func() {
		cmd := exec.Command("sh", "-c", trapsInterrupt)
		cmd.Stderr = &stderr
		returned <- Run(cmd)
	}()
	awaitRunning(t, s, 1)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code := awaitExit(t, s); code != 128+int(syscall.SIGTERM) {
		t.Errorf("the process ended %d, want %d", code, 128+int(syscall.SIGTERM))
	}
	select {
	case <-returned:
		if !strings.Contains(stderr.String(), "interrupted") {
			t.Errorf("the nix was not interrupted; its stderr: %q", stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the stopped nix's Run never returned")
	}
}

// TestADisarmedArmLeavesTheSignalAlone: once disarmed the arm neither stops nix nor ends the
// process, so a command's steps after its nix keep the signal behavior they had.
func TestADisarmedArmLeavesTheSignalAlone(t *testing.T) {
	s := Isolate(t)
	catchSignal(t, syscall.SIGTERM)
	disarm := StopOnSignal()
	disarm()
	disarm() // idempotent
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-s.Exited():
		t.Fatalf("a disarmed arm ended the process %d", code)
	case <-time.After(300 * time.Millisecond):
	}
	if err := Run(exec.Command("true")); err != nil {
		t.Errorf("a disarmed arm stopped the set: %v", err)
	}
}

// TestADisarmDuringTheTeardownWaitsForItsExit: a command that reaches its disarm while the arm's
// teardown is stopping its nix waits there for the teardown's exit, so it never races that exit to
// the end of the process with a status of its own.
func TestADisarmDuringTheTeardownWaitsForItsExit(t *testing.T) {
	s := Isolate(t)
	catchSignal(t, syscall.SIGTERM)
	marker := filepath.Join(t.TempDir(), "interrupted")
	disarm := StopOnSignal()
	go func() {
		_ = Run(exec.Command("sh", "-c",
			"trap 'touch "+marker+"; sleep 0.3; exit 130' INT; while :; do sleep 0.05; done"))
	}()
	awaitRunning(t, s, 1)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the signal never reached the nix")
		}
	}
	disarmed := make(chan struct{})
	go func() { disarm(); close(disarmed) }()
	select {
	case <-disarmed:
		t.Fatal("the disarm returned while the teardown that owns the exit was still stopping nix")
	case code := <-s.Exited():
		if code != 128+int(syscall.SIGTERM) {
			t.Errorf("the process ended %d, want %d", code, 128+int(syscall.SIGTERM))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the signal did not end the process")
	}
	select {
	case <-disarmed:
	case <-time.After(10 * time.Second):
		t.Fatal("the disarm never returned after the exit")
	}
}
