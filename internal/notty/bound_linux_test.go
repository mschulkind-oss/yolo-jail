//go:build linux

package notty

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// graceHelperEnv makes this test binary, re-executed, run a command that IGNORES SIGINT through
// RunBounded with a short KillAfter, and end the way `yolo internal no-terminal` ends
// (WrapperExit), recording in the named directory when the command started and what Run said.
const graceHelperEnv = "NOTTY_TEST_GRACE_HELPER_DIR"

func runGraceHelper(dir string) int {
	err := RunBounded(exec.Command("sh", "-c", `trap '' INT; echo > "$1/started"; exec sleep 30`, "sh", dir),
		Bound{KillAfter: 300 * time.Millisecond})
	var st *Stopped
	if errors.As(err, &st) {
		if werr := os.WriteFile(filepath.Join(dir, "stopped"), []byte(st.Signal.String()), 0o644); werr != nil {
			return 5
		}
	}
	return WrapperExit(err)
}

// runWithin runs f and fails the test if it has not returned within limit, so a bound that does
// not hold is reported as that rather than as a package-wide timeout minutes later.
func runWithin(t *testing.T, limit time.Duration, f func() error) (time.Duration, error) {
	t.Helper()
	done := make(chan error, 1)
	begun := time.Now()
	go func() { done <- f() }()
	select {
	case err := <-done:
		return time.Since(begun), err
	case <-time.After(limit):
		t.Fatalf("still running after %s: the bound did not hold", limit)
		return 0, nil
	}
}

// A CHILD THAT IGNORES SIGTERM IS KILLED AFTER THE GRACE. A timeout that only sends SIGTERM and then
// waits is what hung the launcher for good: claude caught the SIGTERM and was stopped again inside
// its own handler, and GNU timeout without -k waited on it forever.
func TestABoundedRunKillsAChildThatIgnoresSIGTERM(t *testing.T) {
	c := exec.Command("sh", "-c", `trap '' TERM; exec sleep 30`)
	took, err := runWithin(t, 10*time.Second, func() error {
		return RunBounded(c, Bound{Timeout: 300 * time.Millisecond, KillAfter: 300 * time.Millisecond})
	})
	var to *TimedOut
	if !errors.As(err, &to) {
		t.Fatalf("want a *TimedOut, got %v", err)
	}
	if !to.Killed {
		t.Errorf("a child that ignored SIGTERM must be reported KILLED after the grace: %+v", to)
	}
	if took < 600*time.Millisecond {
		t.Errorf("returned after %s, before the timeout plus the grace", took)
	}
	if got := ExitCode(err); got != ExitTimedOut {
		t.Errorf("a timed-out run exits %d, want %d (GNU timeout's)", got, ExitTimedOut)
	}
	if got := WrapperExit(err); got != ExitTimedOut {
		t.Errorf("WrapperExit of a timed-out run = %d, want %d", got, ExitTimedOut)
	}
}

// A STOPPED CHILD IS CONTINUED, so the SIGTERM can reach it. A stopped process acts on no signal but
// SIGKILL and SIGCONT; the SIGTERM sits pending (measured on claude install: ShdPnd 0x4000) until
// something continues it. No KillAfter here, so only the SIGCONT can end this run.
func TestABoundedRunContinuesAStoppedChildSoTheTimeoutReachesIt(t *testing.T) {
	c := exec.Command("sh", "-c", `kill -STOP $$; exec sleep 30`)
	_, err := runWithin(t, 10*time.Second, func() error {
		return RunBounded(c, Bound{Timeout: 300 * time.Millisecond})
	})
	var to *TimedOut
	if !errors.As(err, &to) {
		t.Fatalf("want a *TimedOut, got %v", err)
	}
	if to.Killed {
		t.Errorf("SIGTERM was enough here; nothing should have been killed: %+v", to)
	}
}

// A run that finishes inside its bound is not a timeout, and no Bound is no bound.
func TestABoundedRunThatFinishesInTimeIsNoTimeout(t *testing.T) {
	if err := RunBounded(exec.Command("true"), Bound{Timeout: 5 * time.Second, KillAfter: time.Second}); err != nil {
		t.Errorf("true under a 5s bound: %v", err)
	}
	if got := ExitCode(RunBounded(exec.Command("sh", "-c", "exit 7"), Bound{Timeout: 5 * time.Second})); got != 7 {
		t.Errorf("exit 7 under a bound -> %d", got)
	}
	took, err := runWithin(t, 10*time.Second, func() error {
		return RunBounded(exec.Command("sleep", "1"), Bound{})
	})
	if err != nil || took < time.Second {
		t.Errorf("an unbounded run must run to the end: err=%v after %s", err, took)
	}
}

// A FORWARDED SIGNAL IS ESCALATED AFTER THE GRACE TOO. A Ctrl-C the child ignores must still stop
// it: the launcher that ran it has a terminal user waiting for the agent. The wrapper then dies of
// the signal it received, as it does when the child died of it, so the shell sees a Ctrl-C.
func TestAForwardedSignalTheChildIgnoresIsEscalatedAfterTheGrace(t *testing.T) {
	dir := t.TempDir()
	helper := exec.Command(os.Args[0], "-test.run=^$")
	helper.Env = append(os.Environ(), graceHelperEnv+"="+dir)
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = helper.Process.Kill()
			_ = helper.Wait()
			t.Fatal("the command never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := helper.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	took, err := runWithin(t, 10*time.Second, helper.Wait)
	if took > 5*time.Second {
		t.Errorf("the child outlived the 300ms grace by %s", took)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "stopped")); string(got) != syscall.SIGINT.String() {
		t.Errorf("a child killed after ignoring a forwarded SIGINT must be reported Stopped by it (recorded %q)", got)
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("the wrapper exited cleanly: %v", err)
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Errorf("the wrapper must die of SIGINT itself, got %v", err)
	}
}

// Main is the verb: its flags are the bound, and misuse is 2 with the usage on stderr.
func TestMainReadsTheBoundFromItsFlags(t *testing.T) {
	code, took := 0, time.Duration(0)
	took, _ = runWithin(t, 10*time.Second, func() error {
		code = Main("no-terminal", []string{"--timeout=0.3", "--kill-after=0.3", "--",
			"sh", "-c", "trap '' TERM; exec sleep 30"})
		return nil
	})
	if code != ExitTimedOut {
		t.Errorf("a timed-out command exits %d, want %d", code, ExitTimedOut)
	}
	if took < 600*time.Millisecond {
		t.Errorf("returned after %s: the flags were not read as the bound", took)
	}
	for _, tc := range []struct {
		args []string
		want int
	}{
		{[]string{"--", "true"}, 0},
		{[]string{"--", "sh", "-c", "exit 7"}, 7},
		{[]string{"--timeout=5", "--kill-after=1", "--", "true"}, 0},
		{[]string{"--", filepath.Join(t.TempDir(), "absent")}, 127},
		{nil, 2},
		{[]string{"--"}, 2},
		{[]string{"true"}, 2},
		{[]string{"--timeout=soon", "--", "true"}, 2},
		{[]string{"--timeout=-1", "--", "true"}, 2},
		{[]string{"--kill-after=", "--", "true"}, 2},
		{[]string{"--bogus", "--", "true"}, 2},
	} {
		if got := Main("no-terminal", tc.args); got != tc.want {
			t.Errorf("Main(%s) = %d, want %d", strings.Join(tc.args, " "), got, tc.want)
		}
	}
}

// procGone says whether pid has exited: no /proc entry, or a zombie its new parent has not reaped.
func procGone(pid string) bool {
	b, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return true
	}
	// The state is the field after the parenthesized command name.
	if i := strings.LastIndexByte(string(b), ')'); i >= 0 && i+2 < len(b) {
		return b[i+2] == 'Z'
	}
	return false
}

// readPid waits for the command to have written a pid to path.
func readPid(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
			return strings.TrimSpace(string(b))
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pid was written to %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// THE BOUND IS THE GROUP'S. A command that dies of its SIGTERM can leave behind a grandchild that
// ignores it, still in the command's process group and still writing where the update writes once
// the launcher has dropped its lock. The grace covers that grandchild too: it is killed when the
// grace runs out, and the run has not returned while it lived.
func TestABoundedRunKillsAGrandchildThatOutlivesTheCommand(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild")
	c := exec.Command("sh", "-c", `(trap '' TERM; exec sleep 30) & echo $! > "$1"; wait`, "sh", pidFile)
	var grandchild string
	t.Cleanup(func() {
		if grandchild != "" && !procGone(grandchild) {
			if pid, err := strconv.Atoi(grandchild); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	took, err := runWithin(t, 10*time.Second, func() error {
		return RunBounded(c, Bound{Timeout: 300 * time.Millisecond, KillAfter: 300 * time.Millisecond})
	})
	grandchild = readPid(t, pidFile)
	var to *TimedOut
	if !errors.As(err, &to) {
		t.Fatalf("want a *TimedOut, got %v", err)
	}
	if !procGone(grandchild) {
		t.Errorf("the grandchild %s that ignored SIGTERM outlived the bound", grandchild)
	}
	if took < 600*time.Millisecond {
		t.Errorf("returned after %s, while the grandchild still had its grace to run", took)
	}
}

// What the command leaves behind after a run NO SIGNAL ended is not the bound's: a vendor's own
// background process keeps running, and the run returns when the command does.
func TestABoundedRunLeavesTheGroupAloneWhenNoSignalEndedIt(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "background")
	c := exec.Command("sh", "-c", `sleep 30 & echo $! > "$1"`, "sh", pidFile)
	took, err := runWithin(t, 10*time.Second, func() error {
		return RunBounded(c, Bound{Timeout: 5 * time.Second, KillAfter: time.Second})
	})
	background := readPid(t, pidFile)
	t.Cleanup(func() {
		if pid, err := strconv.Atoi(background); err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	if err != nil {
		t.Fatalf("a command that exited 0 in time: %v", err)
	}
	if took > 2*time.Second {
		t.Errorf("returned after %s: it waited on a process no signal was sent to", took)
	}
	if procGone(background) {
		t.Errorf("a background process of a run no signal ended was killed")
	}
}
