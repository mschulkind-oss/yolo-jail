package check

// nixstop_test.go pins what a signal sent to `yolo check` alone does to the nix the check has
// running: it is stopped, and the check ends 128+N. Before, the signal's default action ended the
// check at once and its nix — the image build, the dry-run, a `nix --version` that hangs — ran on
// with no parent. A Ctrl-C at a terminal never showed it, because the terminal signals nix too.

import (
	"bytes"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// standInNix puts a `nix` on PATH that runs until interrupted, marking its start and the interrupt
// in the directory it returns, and returns that directory and the stand-in's path. It gives up
// after 30 seconds, so a red run whose nix nothing stops does not leave it looping after the test
// binary exits.
func standInNix(t *testing.T) (marks, nix string) {
	t.Helper()
	bin, marks := t.TempDir(), t.TempDir()
	nix = filepath.Join(bin, "nix")
	script := "#!/bin/sh\ntrap " + shquote.Quote("touch "+shquote.Quote(filepath.Join(marks, "interrupted"))+"; echo interrupted >&2; exit 130") + " INT\n" +
		"touch " + shquote.Quote(filepath.Join(marks, "started")) + "\n" +
		"i=0; while [ $i -lt 600 ]; do sleep 0.05; i=$((i+1)); done; exit 1\n"
	must(t, os.WriteFile(nix, []byte(script), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marks, nix
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

// awaitStarted waits for the stand-in nix of marks to mark its start, which it does once its
// interrupt trap is set. Being tracked is not enough: the set tracks the stand-in as soon as it is
// started, and an interrupt that reaches the shell before its trap line ends it by the signal's
// default action, which marks nothing.
func awaitStarted(t *testing.T, marks string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(marks, "started")); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the stand-in nix never marked its start")
		}
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

// TestRealExecTracksItsNix: a nix check runs through its Exec seam — `nix --version`, the
// dry-run, `nix config show` — is one a stop reaches.
func TestRealExecTracksItsNix(t *testing.T) {
	s := nixchildren.Isolate(t)
	marks, _ := standInNix(t)
	done := make(chan ExecResult, 1)
	go func() { done <- realExec([]string{"nix", "--version"}, "", nil, time.Minute) }()
	awaitTracked(t, s, 1)
	awaitStarted(t, marks)
	nixchildren.Stop(nil)
	select {
	case res := <-done:
		if !strings.Contains(res.Stderr, "interrupted") {
			t.Errorf("the probe's nix was not interrupted: %+v", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("realExec did not return after its nix was stopped")
	}
}

// TestASignalToCheckStopsItsNix: a SIGTERM sent to the check's process alone, while the check's
// own `nix --version` (a stand-in that runs until interrupted) is running, interrupts that nix and
// ends the check 143.
func TestASignalToCheckStopsItsNix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := nixchildren.Isolate(t)
	catchSignal(t, syscall.SIGTERM)
	marks, nix := standInNix(t)
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	opts.LookPath = func(name string) (string, bool) { return nix, name == "nix" }
	opts.Exec = func(argv []string, dir string, env []string, timeout time.Duration) ExecResult {
		if argv[0] == "nix" {
			return realExec(argv, dir, env, timeout)
		}
		return ExecResult{Ran: false}
	}
	returned := make(chan int, 1)
	go func() { returned <- Check(opts) }()
	awaitTracked(t, s, 1)
	awaitStarted(t, marks)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-s.Exited():
		if code != 128+int(syscall.SIGTERM) {
			t.Errorf("the signaled check ended %d, want %d", code, 128+int(syscall.SIGTERM))
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a SIGTERM sent to the check alone did not end it")
	}
	if _, err := os.Stat(filepath.Join(marks, "interrupted")); err != nil {
		t.Error("the check ended without interrupting the nix it had running")
	}
	select {
	case <-returned:
	case <-time.After(30 * time.Second):
		t.Fatal("Check never returned after its faked exit")
	}
}
