package run

// nixstop_test.go pins that a launch's nix is stopped by whichever of its signal arms ends the
// process, the session's included, and that a nix it runs through its Exec seam is one those arms
// reach. Before, only the launch guard's teardown stopped nix, and the housekeeping slot's `nix
// store delete`, which runs while the session does, started outside the set: a signal sent to
// yolo alone during the session ended it and left that nix running with no parent.

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// standInNix puts a `nix` on PATH that runs until interrupted, marking its start and the interrupt
// in the directory it returns. It gives up after 30 seconds, so a red run whose nix nothing stops
// does not leave it looping after the test binary exits.
func standInNix(t *testing.T) (marks string) {
	t.Helper()
	bin, marks := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte(standInNixScript(marks)), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marks
}

// standInNixScript is standInNix's program, marking in marks.
func standInNixScript(marks string) string {
	return "#!/bin/sh\ntrap 'touch " + filepath.Join(marks, "interrupted") + "; exit 130' INT\n" +
		"touch " + filepath.Join(marks, "started") + "\n" +
		"i=0; while [ $i -lt 600 ]; do sleep 0.05; i=$((i+1)); done; exit 1\n"
}

// marked reports whether the stand-in nix of marks has left the mark named name.
func marked(marks, name string) bool {
	_, err := os.Stat(filepath.Join(marks, name))
	return err == nil
}

// awaitMark waits for the stand-in nix of marks to leave the mark named name. A test that stops
// the stand-in waits for its "started" mark, which it leaves once its interrupt trap is set: the
// set tracks it as soon as it is started, and an interrupt that reaches the shell before its trap
// line ends it by the signal's default action, which marks nothing.
func awaitMark(t *testing.T, marks, name string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !marked(marks, name); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the stand-in nix never marked %q", name)
		}
	}
}

// awaitTracked waits for s to hold n running children.
func awaitTracked(t *testing.T, s *nixchildren.Set, n int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		got := s.Running()
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d nix children tracked, want %d", got, n)
		}
	}
}

// isolateNixSet gives a test that fires a signal arm, its exit faked, a set of tracked nix children
// of its own (nixchildren.Isolate). The arm's teardown stops the set first
// (launchSignalArm.terminate), a stop is permanent, and on the process's own set it refused every
// later test's tracked nix. TestMain fails the package when a test forgets; seeArmExitsWith does
// this for the tests that fake the exit through launchArmExit.
func isolateNixSet(t *testing.T) {
	t.Helper()
	nixchildren.Isolate(t)
}

// TestRealExecTracksItsNix: a nix a launch runs through its Exec seam — the housekeeping slot's
// `nix store delete` — is one a stop reaches.
func TestRealExecTracksItsNix(t *testing.T) {
	s := nixchildren.Isolate(t)
	marks := standInNix(t)
	done := make(chan ExecResult, 1)
	go func() {
		done <- realExec(prune.StoreDeleteCmd("/nix/store/aaaa-yolo-jail-install-prefix"), "", nil, time.Minute)
	}()
	awaitTracked(t, s, 1)
	awaitMark(t, marks, "started")
	nixchildren.Stop(nil)
	select {
	case res := <-done:
		if res.Ran && res.RC == 0 && !res.Timeout {
			t.Errorf("a stopped store delete reported success: %+v", res)
		}
		if !marked(marks, "interrupted") {
			t.Error("the stop did not interrupt the store delete's nix")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("realExec did not return after its nix was stopped")
	}
}

// TestASessionsArmStopsTheNixItsLaunchRuns: a signal sent to yolo alone while its session runs,
// and with it the housekeeping slot's `nix store delete`, interrupts that nix before the arm's exit.
// The session's arm is no launch guard, whose own teardown was the only one to stop nix.
func TestASessionsArmStopsTheNixItsLaunchRuns(t *testing.T) {
	isolateNixSet(t)
	catchSignal(t, syscall.SIGTERM)
	marks := standInNix(t)
	var interruptedAtExit atomic.Bool
	codes := make(chan int, 1)
	arm := armLaunchSignalsWith(func() {}, func(code int) {
		interruptedAtExit.Store(marked(marks, "interrupted"))
		codes <- code
	})
	t.Cleanup(func() { popLaunchArm(arm) })
	arm.attach(&fakeSessionHandle{})
	go realExec(prune.StoreDeleteCmd("/nix/store/aaaa-yolo-jail-install-prefix"), "", nil, time.Minute)
	awaitMark(t, marks, "started")
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-codes:
		if code != 128+int(syscall.SIGTERM) {
			t.Errorf("the session ended %d, want %d", code, 128+int(syscall.SIGTERM))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the session's arm did not exit on SIGTERM")
	}
	if !interruptedAtExit.Load() {
		t.Error("the session's arm exited without interrupting the nix its launch had running")
	}
}

// TestAGuardedLaunchStopsItsNixBeforeItExits: a signal that ends a launch before its keeper ends
// the nix it has running first — here the store-delivered extras build's own nix, a stand-in that
// runs until interrupted. A signal sent to this process alone reaches no child, and the nix of a
// launch that exited without stopping it ran on with no parent (nixchildren.Stop).
func TestAGuardedLaunchStopsItsNixBeforeItExits(t *testing.T) {
	marks := t.TempDir()
	var interruptedAtExit atomic.Bool
	f := newGuardFixtureWith(t, "yolo-guard-stops-nix", func() {
		interruptedAtExit.Store(marked(marks, "interrupted"))
	})
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte(standInNixScript(marks)), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	go func() { _ = nixchildren.Run(exec.Command("nix", "build", ".#yoloImageExtras")) }()
	awaitMark(t, marks, "started")
	e := f.interrupt(t, syscall.SIGINT)
	if e.code != 128+int(syscall.SIGINT) {
		t.Errorf("the launch exited %d, want %d", e.code, 128+int(syscall.SIGINT))
	}
	if !interruptedAtExit.Load() {
		t.Error("the launch a signal ended exited without stopping the nix it had running")
	}
}
