package check

// nixstop_test.go pins what a signal sent to `yolo check` alone does to the nix the check has
// running: it is stopped, and the check ends 128+N. Before, the signal's default action ended the
// check at once and its nix — the image build, the dry-run, a `nix --version` that hangs — ran on
// with no parent. A Ctrl-C at a terminal never showed it, because the terminal signals nix too.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// standInNix puts a `nix` on PATH that runs until interrupted (testsupport.UntilInterrupted),
// marking its start and the interrupt in the directory it returns, and returns that directory and
// the stand-in's path.
func standInNix(t *testing.T) (marks, nix string) {
	t.Helper()
	bin, marks := t.TempDir(), t.TempDir()
	nix = filepath.Join(bin, "nix")
	script := "#!/bin/sh\n" + testsupport.UntilInterrupted("touch "+shquote.Quote(filepath.Join(marks, "interrupted"))+"; echo interrupted >&2",
		filepath.Join(marks, "started")) + "\n"
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
	const childEnv, readyEnv = "YOLO_CHECK_NIX_SIGNAL_CHILD", "YOLO_CHECK_NIX_SIGNAL_READY"
	if os.Getenv(childEnv) != "1" {
		ready := filepath.Join(t.TempDir(), "ready")
		cmd := exec.Command(os.Args[0], "-test.run=^TestASignalToCheckStopsItsNix$")
		cmd.Env = append(os.Environ(), childEnv+"=1", readyEnv+"="+ready)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = cmd.Process.Kill() }()
		waited := make(chan error, 1)
		go func() { waited <- cmd.Wait() }()
		var marks string
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			// The child renames a complete file into place, so a read sees all of it or
			// nothing; an empty read still waits rather than failing (CI run 42314578948).
			if b, err := os.ReadFile(ready); err == nil && len(b) > 0 {
				marks = string(b)
				break
			}
			select {
			case err := <-waited:
				t.Fatalf("signal child exited before readiness (%v): %s", err, out.String())
			default:
			}
		}
		if marks == "" {
			t.Fatalf("signal child did not become ready: %s", out.String())
		}
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-waited:
			if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 128+int(syscall.SIGTERM) {
				t.Fatalf("signal child exit = %v, want 143: %s", err, out.String())
			}
		case <-time.After(20 * time.Second):
			t.Fatal("signal child did not exit after SIGTERM")
		}
		if _, err := os.Stat(filepath.Join(marks, "interrupted")); err != nil {
			t.Fatalf("the check ended without interrupting its nix: %v", err)
		}
		return
	}

	t.Setenv("HOME", t.TempDir())
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
	go Check(opts)
	awaitStarted(t, marks)
	readyTmp := os.Getenv(readyEnv) + ".tmp"
	if err := os.WriteFile(readyTmp, []byte(marks), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(readyTmp, os.Getenv(readyEnv)); err != nil {
		t.Fatal(err)
	}
	select {}
}
