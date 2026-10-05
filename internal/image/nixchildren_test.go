package image

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
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

// trapsInterrupt is a stand-in nix that runs until interrupted (testsupport.UntilInterrupted) and
// says so on stderr, and that creates the file started once its trap is set (awaitStarted).
func trapsInterrupt(started string) string {
	return testsupport.UntilInterrupted("echo interrupted >&2", started)
}

// startMark is where a stand-in of t marks its start: a path in a temp dir of t's own.
func startMark(t *testing.T) string { return filepath.Join(t.TempDir(), "started") }

// awaitStarted waits for the stand-in that marks its start at started to have done so, which it
// does once its interrupt trap is set. Being tracked is not enough: the set tracks the stand-in as
// soon as it is started, and an interrupt that reaches the shell before its trap line ends it by
// the signal's default action, which says nothing.
func awaitStarted(t *testing.T, started string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(started); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the stand-in nix never marked its start")
		}
	}
}

// standInNix puts a `nix` on PATH that runs script.
func standInNix(t *testing.T, script string) {
	t.Helper()
	standIn(t, "nix", script)
}

// standIn puts a program named name on PATH that runs script.
func standIn(t *testing.T, name, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
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
	started := startMark(t)
	go func() {
		p, tail := runNixBuild([]string{"sh", "-c", trapsInterrupt(started)}, t.TempDir(), os.Environ(),
			filepath.Join(t.TempDir(), "out"), io.Discard)
		done <- built{p, tail}
	}()
	awaitTracked(t, s, 1)
	awaitStarted(t, started)
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
	path, tail := runNixBuild([]string{"sh", "-c", "touch " + shquote.Quote(ran)}, t.TempDir(), os.Environ(),
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
	standInNix(t, trapsInterrupt(startMark(t)))
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
	started := startMark(t)
	standInNix(t, trapsInterrupt(started))
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
	awaitStarted(t, started)
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
//
// The build makes its links in a temp dir of this test's own: the machine-wide one holds every
// other run's, a link an earlier red run left and one a run of this package going on beside this
// one is making, and globbing it there failed this test on correct code and deleted that link.
func TestAStoppedCheckBuildRemovesItsLinksBeforeTheHandOff(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	s := freshNixChildren(t)
	// The stand-in makes its out-link the way nix does, then runs until interrupted. Its link comes
	// before its trap, so the link alone does not say the trap is set (awaitStarted below): an
	// interrupt the shell takes while it waits on `ln`, which then exits normally, is one it
	// ignores and goes on, past the stop's grace and this test's bound.
	started := startMark(t)
	standInNix(t, `while [ $# -gt 0 ]; do [ "$1" = --out-link ] && ln -s /nonexistent "$2"; shift; done; `+
		trapsInterrupt(started))
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
	awaitStarted(t, started)
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
	script := trapsInterrupt(startMark(t))
	go func() {
		_, tail := runNixBuild([]string{"sh", "-c", script}, t.TempDir(), os.Environ(),
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

// TestAddRootIsTracked: the GC-root registration a launch makes on the host, `nix-store
// --add-root`, is a nix a stop reaches, so a launch a signal ends does not leave it running.
func TestAddRootIsTracked(t *testing.T) {
	s := freshNixChildren(t)
	started := startMark(t)
	standIn(t, "nix-store", trapsInterrupt(started))
	var out strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- AddRoot(filepath.Join(t.TempDir(), "root"), "/nix/store/aaaa-image.json", &out, "could not root it")
	}()
	awaitTracked(t, s, 1)
	awaitStarted(t, started)
	nixchildren.Stop(nil)
	select {
	case err := <-done:
		if err == nil {
			t.Error("an interrupted registration reported success")
		}
		if !strings.Contains(out.String(), "interrupted") {
			t.Errorf("the registration's nix-store was not interrupted; it said %q", out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("AddRoot did not return after its nix-store was stopped")
	}
}

// TestTheStorePathValidityProbeIsTracked: the store-validity probe a matched launch makes,
// `nix-store --check-validity`, which waits up to 30 seconds on a busy daemon, is a nix a stop
// reaches.
func TestTheStorePathValidityProbeIsTracked(t *testing.T) {
	s := freshNixChildren(t)
	marks := t.TempDir()
	started := filepath.Join(marks, "started")
	standIn(t, "nix-store", testsupport.UntilInterrupted("touch "+shquote.Quote(filepath.Join(marks, "interrupted")), started))
	done := make(chan bool, 1)
	go func() { done <- nixStorePathValid("/nix/store/aaaa-image.json") }()
	awaitTracked(t, s, 1)
	awaitStarted(t, started)
	nixchildren.Stop(nil)
	select {
	case valid := <-done:
		if valid {
			t.Error("an interrupted probe called the path valid")
		}
		if _, err := os.Stat(filepath.Join(marks, "interrupted")); err != nil {
			t.Error("the probe's nix-store was not interrupted")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the validity probe did not return after its nix-store was stopped")
	}
}
