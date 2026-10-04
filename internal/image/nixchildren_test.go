package image

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
)

// nixchildren_test.go pins this package's nix call sites to the one set internal/nixchildren
// keeps: each test fails if its call site starts its nix without it. What the set does with a
// child is pinned there.

// freshNixChildren swaps in an empty set for one test and returns it (nixchildren.Isolate): a stop
// is permanent for the set it ran on, as it is for the process a signal is ending. The cleanup
// stops the set again, so a red run leaves none of its stand-in nix processes looping after the
// test binary exits.
func freshNixChildren(t *testing.T) *nixchildren.Set {
	t.Helper()
	return nixchildren.Isolate(t)
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

// trapsInterrupt is a stand-in nix that runs until interrupted and says so on stderr. It gives up
// after 30 seconds, so a red run whose nix nothing tracks — and so nothing stops — does not leave
// it looping after the test binary exits.
const trapsInterrupt = `trap 'echo interrupted >&2; exit 130' INT; echo started >&2; ` +
	`i=0; while [ $i -lt 600 ]; do sleep 0.05; i=$((i+1)); done; exit 1`

// standInNix puts a `nix` on PATH that runs script.
func standInNix(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

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
	nixchildren.Stop(nil)
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

// TestARunNixBuildAfterTheStopSaysWhy: a build the stop refused starts nothing and says why in the
// tail its caller reports.
func TestARunNixBuildAfterTheStopSaysWhy(t *testing.T) {
	freshNixChildren(t)
	nixchildren.Stop(nil)
	ran := filepath.Join(t.TempDir(), "ran")
	path, tail := runNixBuild([]string{"sh", "-c", "touch " + ran}, t.TempDir(), os.Environ(),
		filepath.Join(t.TempDir(), "out"), io.Discard)
	if path != "" {
		t.Errorf("a build after the stop returned store path %q", path)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("a nix was started after the stop")
	}
	if !strings.Contains(strings.Join(tail, "\n"), nixchildren.ErrStopped.Error()) {
		t.Errorf("the refused build does not say why: %q", tail)
	}
}

// TestEvalImageIdentityIsTracked: the image-identity eval, the nix an interrupted launch was
// measured leaving behind, is one a stop reaches.
func TestEvalImageIdentityIsTracked(t *testing.T) {
	s := freshNixChildren(t)
	standInNix(t, trapsInterrupt)
	done := make(chan bool, 1)
	go func() {
		_, ok := EvalImageIdentity(t.TempDir())
		done <- ok
	}()
	awaitTracked(t, s, 1)
	nixchildren.Stop(nil)
	select {
	case ok := <-done:
		if ok {
			t.Error("an interrupted identity eval reported an identity")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("EvalImageIdentity did not return after its nix was stopped")
	}
}

// TestBuildOCIImageIsTracked: `yolo check`'s image build is a nix a stop reaches, so a signal sent
// to the check alone stops it rather than leaving it building with no parent.
func TestBuildOCIImageIsTracked(t *testing.T) {
	s := freshNixChildren(t)
	standInNix(t, trapsInterrupt)
	type built struct {
		path string
		tail []string
	}
	done := make(chan built, 1)
	go func() {
		p, tail := BuildOCIImage(OCIBuildRequest{RepoRoot: t.TempDir()})
		done <- built{p, tail}
	}()
	awaitTracked(t, s, 1)
	nixchildren.Stop(nil)
	select {
	case b := <-done:
		if b.path != "" {
			t.Errorf("an interrupted check build returned store path %q", b.path)
		}
		if !strings.Contains(strings.Join(b.tail, "\n"), "interrupted") {
			t.Errorf("the check build's nix was not interrupted; its stderr: %q", b.tail)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("BuildOCIImage did not return after its nix was stopped")
	}
}

// TestAStoppedCheckBuildRemovesItsLinksBeforeTheHandOff: the out-links BuildOCIImage removes are
// GC roots under /tmp, and after a stop its release waits for the signal's teardown to end the
// process. A link still there at that wait is never removed, so it goes first.
func TestAStoppedCheckBuildRemovesItsLinksBeforeTheHandOff(t *testing.T) {
	s := freshNixChildren(t)
	// The stand-in makes its out-link the way nix does, then runs until interrupted.
	standInNix(t, `while [ $# -gt 0 ]; do [ "$1" = --out-link ] && ln -s /nonexistent "$2"; shift; done; `+
		trapsInterrupt)
	links := make(chan []string, 1)
	go BuildOCIImage(OCIBuildRequest{RepoRoot: t.TempDir()})
	awaitTracked(t, s, 1)
	matches := func() []string {
		m, _ := filepath.Glob(filepath.Join(os.TempDir(), "yolo-check-*"))
		var mine []string
		for _, l := range m {
			if target, err := os.Readlink(l); err == nil && target == "/nonexistent" {
				mine = append(mine, l)
			}
		}
		return mine
	}
	for deadline := time.Now().Add(10 * time.Second); len(matches()) == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the stand-in never made its out-link")
		}
	}
	exited := make(chan struct{})
	t.Cleanup(func() { close(exited) })
	go nixchildren.Stop(func() { links <- matches(); <-exited })
	select {
	case left := <-links:
		if len(left) > 0 {
			t.Errorf("the stopped check build handed off with its out-links still in place: %v", left)
			for _, l := range left {
				_ = os.Remove(l)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the stopped check build never reached the stop's hand-off")
	}
}

// TestAStoppedBuildsCallerWaitsForTheExit: runNixBuild releases its nix only on its way out, so
// the stop's hand-off holds runNixBuild itself, and its caller cannot go on to report a failed
// build while the teardown that stopped it exits.
func TestAStoppedBuildsCallerWaitsForTheExit(t *testing.T) {
	s := freshNixChildren(t)
	returned := make(chan []string, 1)
	go func() {
		_, tail := runNixBuild([]string{"sh", "-c", trapsInterrupt}, t.TempDir(), os.Environ(),
			filepath.Join(t.TempDir(), "out"), io.Discard)
		returned <- tail
	}()
	awaitTracked(t, s, 1)
	exited, handedOff := make(chan struct{}), make(chan struct{}, 1)
	go nixchildren.Stop(func() { handedOff <- struct{}{}; <-exited })
	select {
	case <-handedOff:
	case <-time.After(10 * time.Second):
		t.Fatal("the stopped nix's caller never ran the stop's hand-off")
	}
	select {
	case tail := <-returned:
		t.Fatalf("runNixBuild returned before the exit it was handed off to, free to report a "+
			"failed build the signal caused; its tail: %q", tail)
	case <-time.After(200 * time.Millisecond):
	}
	close(exited)
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("runNixBuild did not return once the exit it waited for came")
	}
}
