package image

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// freshNixChildren swaps in an empty set for one test and returns it: a stop is permanent for the
// set it ran on, as it is for the process a signal is ending. The cleanup stops the set again, so
// a red run leaves none of its stand-in nix processes looping after the test binary exits.
func freshNixChildren(t *testing.T) *nixChildSet {
	t.Helper()
	saved, s := nixChildren, newNixChildSet()
	nixChildren = s
	t.Cleanup(func() {
		s.stop(time.Second)
		nixChildren = saved
	})
	return s
}

// awaitTracked waits for s to hold n running children.
func awaitTracked(t *testing.T, s *nixChildSet, n int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		s.mu.Lock()
		got := len(s.running)
		s.mu.Unlock()
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d nix children tracked, want %d", got, n)
		}
	}
}

// trapsInterrupt is a stand-in nix that runs until interrupted and says so on stderr.
const trapsInterrupt = `trap 'echo interrupted >&2; exit 130' INT; echo started >&2; while :; do sleep 0.05; done`

// TestStopNixChildrenInterruptsARunningBuild: runNixBuild's nix is tracked, and a stop interrupts
// it, as a terminal's Ctrl-C would, and waits for it to end.
func TestStopNixChildrenInterruptsARunningBuild(t *testing.T) {
	s := freshNixChildren(t)
	type built struct {
		path string
		tail []string
	}
	done := make(chan built, 1)
	go func() {
		p, tail := runNixBuild([]string{"sh", "-c", trapsInterrupt}, t.TempDir(), os.Environ(),
			filepath.Join(t.TempDir(), "out"), io.Discard)
		done <- built{p, tail}
	}()
	awaitTracked(t, s, 1)
	start := time.Now()
	s.stop(10 * time.Second)
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("stop took %s to end a nix that answers its interrupt at once", took)
	}
	select {
	case b := <-done:
		if b.path != "" {
			t.Errorf("an interrupted build returned store path %q", b.path)
		}
		if !strings.Contains(strings.Join(b.tail, "\n"), "interrupted") {
			t.Errorf("the build's nix was not interrupted; its stderr: %q", b.tail)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runNixBuild did not return after its nix was stopped")
	}
	awaitTracked(t, s, 0)
}

// TestStopNixChildrenKillsANixThatIgnoresTheInterrupt: past the grace, a nix still running is
// killed rather than left behind.
func TestStopNixChildrenKillsANixThatIgnoresTheInterrupt(t *testing.T) {
	s := freshNixChildren(t)
	done := make(chan struct{})
	go func() {
		runNixBuild([]string{"sh", "-c", `trap '' INT; while :; do sleep 0.05; done`}, t.TempDir(),
			os.Environ(), filepath.Join(t.TempDir(), "out"), io.Discard)
		close(done)
	}()
	awaitTracked(t, s, 1)
	s.stop(200 * time.Millisecond)
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
	s := freshNixChildren(t)
	s.stop(time.Second)
	ran := filepath.Join(t.TempDir(), "ran")
	path, tail := runNixBuild([]string{"sh", "-c", "touch " + ran}, t.TempDir(), os.Environ(),
		filepath.Join(t.TempDir(), "out"), io.Discard)
	if path != "" {
		t.Errorf("a build after the stop returned store path %q", path)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("a nix was started after the stop")
	}
	if !strings.Contains(strings.Join(tail, "\n"), "a signal is ending this launch") {
		t.Errorf("the refused build does not say why: %q", tail)
	}
	if err := RunNix(exec.Command("sh", "-c", "touch "+ran)); !errors.Is(err, errNixStopped) {
		t.Errorf("RunNix after the stop returned %v, want errNixStopped", err)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("RunNix started a nix after the stop")
	}
}

// TestEvalImageIdentityIsTracked: the image-identity eval, the nix an interrupted launch was
// measured leaving behind, is one a stop reaches.
func TestEvalImageIdentityIsTracked(t *testing.T) {
	s := freshNixChildren(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte("#!/bin/sh\n"+trapsInterrupt+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	done := make(chan bool, 1)
	go func() {
		_, ok := EvalImageIdentity(t.TempDir())
		done <- ok
	}()
	awaitTracked(t, s, 1)
	s.stop(10 * time.Second)
	select {
	case ok := <-done:
		if ok {
			t.Error("an interrupted identity eval reported an identity")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("EvalImageIdentity did not return after its nix was stopped")
	}
}
