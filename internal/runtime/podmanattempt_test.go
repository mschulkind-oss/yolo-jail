package runtime

import (
	"errors"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// killGroupAtCleanup stops a test's leftover process group once the test ends. The runner
// never kills what it started, which is the property under test, so the test has to.
func killGroupAtCleanup(t *testing.T, pid int) {
	t.Helper()
	if pid > 0 {
		t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	}
}

// A child that exits while a grandchild still holds its stdout and stderr: the runner must
// return when the CHILD exits. With pipes, os/exec's Wait would sit until the grandchild
// closed them — the 22.5 s overrun of podman-reboot-readiness.md, and 100 s in its
// reproduction.
func TestTheAttemptReturnsWhenTheChildExitsThoughAGrandchildHoldsItsOutput(t *testing.T) {
	testsupport.UnsetLCAll(t) // the child shell's output is compared exactly
	start := time.Now()
	a := RunPodmanAttempt([]string{"sh", "-c", `sleep 30 & echo '{"ok":true}'; echo warm >&2; exit 0`},
		time.Now().Add(20*time.Second), nil)
	killGroupAtCleanup(t, a.Pid)
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("the attempt took %s: it waited on the grandchild's copy of its output", took)
	}
	if !a.Exited || a.RC != 0 {
		t.Fatalf("attempt = %+v, want an exit 0", a)
	}
	if strings.TrimSpace(a.Stdout) != `{"ok":true}` || strings.TrimSpace(a.Stderr) != "warm" {
		t.Errorf("output not read back from the start: stdout=%q stderr=%q", a.Stdout, a.Stderr)
	}
}

// An attempt that outlives its deadline is left RUNNING and its pid reported.
func TestAnAttemptThatOutlivesTheDeadlineIsLeftRunning(t *testing.T) {
	a := RunPodmanAttempt([]string{"sh", "-c", "sleep 30"}, time.Now().Add(300*time.Millisecond), nil)
	killGroupAtCleanup(t, a.Pid)
	if a.Exited || a.Pid <= 0 {
		t.Fatalf("attempt = %+v, want a still-running process with its pid", a)
	}
	if err := syscall.Kill(a.Pid, 0); err != nil {
		t.Fatalf("the process was not left running: %v", err)
	}
	// Its own process group, so a terminal's Ctrl-C (sent to yolo's group) does not reach it.
	pgid, err := syscall.Getpgid(a.Pid)
	if err != nil || pgid != a.Pid {
		t.Errorf("pgid = %d (%v), want the attempt to lead its own group (%d)", pgid, err, a.Pid)
	}
}

// An interrupt stops the wait, not the process.
func TestAnInterruptStopsTheWaitNotTheAttempt(t *testing.T) {
	interrupt := make(chan struct{})
	go func() { time.Sleep(200 * time.Millisecond); close(interrupt) }()
	a := RunPodmanAttempt([]string{"sh", "-c", "sleep 30"}, time.Now().Add(20*time.Second), interrupt)
	killGroupAtCleanup(t, a.Pid)
	if !a.Interrupted || a.Exited {
		t.Fatalf("attempt = %+v, want interrupted and still running", a)
	}
	if err := syscall.Kill(a.Pid, 0); err != nil {
		t.Fatalf("the interrupt killed the attempt: %v", err)
	}
}

// A nonzero exit is reported with its code and stderr.
func TestAnAttemptReportsItsExitCodeAndStderr(t *testing.T) {
	a := RunPodmanAttempt([]string{"sh", "-c", "echo 'Error: database is locked' >&2; exit 125"},
		time.Now().Add(20*time.Second), nil)
	if !a.Exited || a.RC != 125 || !strings.Contains(a.Stderr, "database is locked") {
		t.Fatalf("attempt = %+v", a)
	}
}

// A binary that does not exist cannot start, and the error the real runner returns — by path
// and by bare name looked up on PATH — is one the gate refuses at once.
func TestAnAttemptOfAMissingBinaryDoesNotStart(t *testing.T) {
	for _, argv0 := range []string{"/nonexistent/yolo-test-podman", "yolo-test-podman-not-on-any-path"} {
		a := RunPodmanAttempt([]string{argv0, "info"}, time.Now().Add(time.Second), nil)
		if a.StartErr == nil {
			t.Fatalf("%s: attempt = %+v, want a start error", argv0, a)
		}
		if f := ClassifyStartError(a.StartErr); f.Class != FailurePermanent || !strings.Contains(f.Fix, "install podman") {
			t.Errorf("%s: %v classified as %+v, want a refusal naming the install", argv0, a.StartErr, f)
		}
	}
}

// A temporary directory yolo cannot create its scratch files in never ran podman at all: the
// gate says it was yolo's own scratch file, names the directory to fix, and does not tell the
// user to install podman (PR-D23).
func TestAScratchFileYoloCannotCreateIsNamedAsYolos(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "no-such-dir"))
	a := RunPodmanAttempt([]string{"sh", "-c", "echo '{}'"}, time.Now().Add(5*time.Second), nil)
	var scratch *ProbeScratchError
	if !errors.As(a.StartErr, &scratch) {
		t.Fatalf("attempt = %+v, want a scratch-file start error", a)
	}
	calls := 0
	res := WaitForPodman([]string{"sh", "-c", "echo '{}'"}, PodmanReadyBudget, ReadySeams{
		Attempt: func(argv []string, d time.Time, i <-chan struct{}) Attempt {
			calls++
			return RunPodmanAttempt(argv, d, i)
		},
		Sleep: func(time.Duration, <-chan struct{}) bool { return true },
	}, ReadyHooks{})
	refusal := res.Refusal("podman")
	if res.Outcome != PodmanNotStarted || calls != 1 {
		t.Fatalf("outcome=%v after %d attempts: %s", res.Outcome, calls, refusal)
	}
	if !strings.Contains(refusal, "yolo could not create the scratch file for podman's answer") ||
		!strings.Contains(refusal, "TMPDIR") || strings.Contains(refusal, "install podman") {
		t.Errorf("refusal = %q", refusal)
	}
}
