package run

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// realExecWithin runs realExec in a goroutine and fails the test when the call has not
// returned by limit, the bound its timeout promises. A failed call is left running: what it
// waits on is a sleep the test's cleanup kills.
func realExecWithin(t *testing.T, limit time.Duration, argv []string, timeout time.Duration) (ExecResult, time.Duration) {
	t.Helper()
	start := time.Now()
	got := make(chan ExecResult, 1)
	go func() { got <- realExec(argv, "", nil, timeout) }()
	select {
	case res := <-got:
		return res, time.Since(start)
	case <-time.After(limit):
		t.Fatalf("realExec(%q, timeout %s) had not returned after %s: its timeout does not bound the call",
			argv, timeout, limit)
		return ExecResult{}, 0
	}
}

// killRecordedPids kills, at the test's end, every pid a fake command appended to pidFile: the
// grandchildren a timed-out call leaves running.
func killRecordedPids(t *testing.T, pidFile string) {
	t.Cleanup(func() {
		b, _ := os.ReadFile(pidFile)
		for _, f := range strings.Fields(string(b)) {
			if pid, err := strconv.Atoi(f); err == nil && pid > 1 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
}

// TestRealExecTimeoutBoundsAChildWhoseGrandchildHoldsItsOutput is the bound itself. The fake
// command forks a sleeping grandchild that inherits its stdout and stderr, then wedges. The
// kill at the deadline reaches the direct child alone, and the grandchild keeps both pipes
// open, so a call that reads them to their end lasts as long as the grandchild does. The call
// must return within its timeout and the drain grace, report Timeout, and keep what the child
// wrote before it.
func TestRealExecTimeoutBoundsAChildWhoseGrandchildHoldsItsOutput(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	killRecordedPids(t, pidFile)
	script := "printf 'partial out\\n'; printf 'partial err\\n' >&2; " +
		"sleep 60 & echo $! >> " + shquote.Quote(pidFile) + "; exec sleep 60"
	timeout := 300 * time.Millisecond

	res, took := realExecWithin(t, timeout+execDrainGrace+5*time.Second, []string{"sh", "-c", script}, timeout)

	if !res.Ran || !res.Timeout || res.RC != 0 {
		t.Errorf("result = %+v, want Ran and Timeout, with no exit code", res)
	}
	if res.Stdout != "partial out\n" || res.Stderr != "partial err\n" {
		t.Errorf("output = %q / %q, want what the child wrote before the deadline", res.Stdout, res.Stderr)
	}
	if limit := timeout + execDrainGrace + time.Second; took > limit {
		t.Errorf("the call took %s, want at most its timeout and the drain grace (%s, with a second's slack)",
			took, timeout+execDrainGrace)
	}
}

// TestRealExecTimeoutBoundsAGrandchildThatOutlivesAFinishedChild: the child exits at once and
// its grandchild holds the pipes past the deadline. The call read the pipes to their end and so
// lasted as long as the grandchild; it is still a timeout, as it always was, because its output
// had not ended by the deadline, and a cut-short read must never pass for a clean success.
func TestRealExecTimeoutBoundsAGrandchildThatOutlivesAFinishedChild(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	killRecordedPids(t, pidFile)
	script := "printf 'listed\\n'; sleep 60 & echo $! >> " + shquote.Quote(pidFile)
	timeout := 300 * time.Millisecond

	res, took := realExecWithin(t, timeout+execDrainGrace+5*time.Second, []string{"sh", "-c", script}, timeout)

	if !res.Ran || !res.Timeout || res.RC != 0 {
		t.Errorf("result = %+v, want Ran and Timeout, with no exit code", res)
	}
	if res.Stdout != "listed\n" {
		t.Errorf("stdout = %q, want what the child wrote", res.Stdout)
	}
	if limit := timeout + execDrainGrace + time.Second; took > limit {
		t.Errorf("the call took %s, want at most %s", took, timeout+execDrainGrace)
	}
}

// TestRealExecLeavesAGrandchildThatWritesAfterTheCall: the kill reaches the direct child alone,
// so a grandchild the call gives up on must run on as it did when the call waited for it, and
// not be stopped by its next write. A read end closed at the return makes that write fail, and
// the SIGPIPE it raises kills a program that does not handle it, such as a shell: here the
// grandchild writes to both streams after the call returned and then records that it lived.
// The rootless podman docs/design/podman-reboot-readiness.md finds finishing the post-boot
// refresh after its parent was killed is such a grandchild.
func TestRealExecLeavesAGrandchildThatWritesAfterTheCall(t *testing.T) {
	survived := filepath.Join(t.TempDir(), "survived")
	script := "(sleep 1.5; printf 'late\\n'; printf 'late err\\n' >&2; : > " + shquote.Quote(survived) +
		") & printf 'early\\n'"
	timeout := 300 * time.Millisecond

	res, _ := realExecWithin(t, timeout+execDrainGrace+5*time.Second, []string{"sh", "-c", script}, timeout)

	if !res.Ran || !res.Timeout || res.RC != 0 {
		t.Errorf("result = %+v, want Ran and Timeout, with no exit code", res)
	}
	if res.Stdout != "early\n" || res.Stderr != "" {
		t.Errorf("output = %q / %q, want what was written before the call returned", res.Stdout, res.Stderr)
	}
	for give := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(survived); err == nil {
			return
		}
		if time.Now().After(give) {
			t.Fatal("the grandchild did not live past its writes after the call returned: " +
				"the call stopped reading its pipes, and the write killed it")
		}
	}
}

// TestRealExecTimeoutOfAChildAloneKeepsItsShape: the common timeout, a child with no
// grandchild that outlives its deadline, reports what it always did: Ran and Timeout with no
// exit code, and both streams as far as the child wrote them.
func TestRealExecTimeoutOfAChildAloneKeepsItsShape(t *testing.T) {
	script := "printf 'so far\\n'; printf 'err so far\\n' >&2; exec sleep 60"
	timeout := 300 * time.Millisecond

	res, took := realExecWithin(t, timeout+execDrainGrace+5*time.Second, []string{"sh", "-c", script}, timeout)

	if res != (ExecResult{Stdout: "so far\n", Stderr: "err so far\n", Ran: true, Timeout: true}) {
		t.Errorf("result = %+v, want Ran and Timeout with no exit code, and both streams so far", res)
	}
	if limit := timeout + execDrainGrace + time.Second; took > limit {
		t.Errorf("the call took %s, want at most %s", took, timeout+execDrainGrace)
	}
}

// TestRealExecReadsAGrandchildsOutputUntilTheDeadline: the bound starts at the deadline, not
// at the child's exit. A grandchild that writes after its parent exits, and closes the pipes
// well before the deadline, is read to the end and the call is a success with the child's
// exit code, as it was before the bound.
func TestRealExecReadsAGrandchildsOutputUntilTheDeadline(t *testing.T) {
	late := 2 * execDrainGrace
	script := "(sleep " + strconv.FormatFloat(late.Seconds(), 'f', 3, 64) +
		"; printf 'late\\n'; printf 'late err\\n' >&2) & printf 'early\\n'; exit 4"
	res, _ := realExecWithin(t, 30*time.Second, []string{"sh", "-c", script}, 20*time.Second)

	if !res.Ran || res.Timeout || res.RC != 4 {
		t.Errorf("result = %+v, want Ran, no Timeout, RC 4", res)
	}
	if res.Stdout != "early\nlate\n" || res.Stderr != "late err\n" {
		t.Errorf("output = %q / %q, want the child's and then the grandchild's", res.Stdout, res.Stderr)
	}
}

// TestRealExecResultShape pins what every caller reads off a call that ends on its own: the
// exit code, both streams, the working directory and the extra environment, and Ran=false for
// a program that is not there. A zero timeout is no deadline.
func TestRealExecResultShape(t *testing.T) {
	// Resolved where it is made: on macOS t.TempDir() is under a symlink, and `pwd -P` is not.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := `printf '%s|%s\n' "$(pwd -P)" "$REAL_EXEC_PROBE"; printf 'to err\n' >&2; exit 3`
	for _, timeout := range []time.Duration{0, 20 * time.Second} {
		res := realExec([]string{"sh", "-c", script}, dir, []string{"REAL_EXEC_PROBE=set"}, timeout)
		if !res.Ran || res.Timeout || res.RC != 3 {
			t.Errorf("timeout %s: result = %+v, want Ran, no Timeout, RC 3", timeout, res)
		}
		if want := dir + "|set\n"; res.Stdout != want || res.Stderr != "to err\n" {
			t.Errorf("timeout %s: output = %q / %q, want %q / %q", timeout, res.Stdout, res.Stderr, want, "to err\n")
		}
	}
	if res := realExec([]string{filepath.Join(dir, "no-such-program")}, "", nil, time.Second); res.Ran || res.Timeout {
		t.Errorf("a missing program: result = %+v, want Ran=false", res)
	}
	if res := realExec(nil, "", nil, time.Second); res != (ExecResult{}) {
		t.Errorf("an empty argv: result = %+v, want the zero result", res)
	}
}
