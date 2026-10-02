package integration

// keeperreadywindow_test.go is the end-to-end pin on a fresh launch interrupted after its keeper
// said the jail is ready and before the launch's signal arm switched to the first session's own
// teardown (docs/design/jail-lifetime-last-session-wins.md JL-D74). The arm still ran the pre-ready
// teardown there, whose lifeline the keeper no longer reads, while the launch's own session count
// kept the keeper from draining, so the launch waited out its whole 20 s bound and the jail ended
// only at its exit. The unit tier drives the same moment against a fake runtime
// (internal/cli/run/keeperreadywindow_test.go).
//
// THE WINDOW IS HELD OPEN BY STOPPING THE LAUNCH: SIGSTOP once its keeper has started, until the
// keeper's log says the jail is ready, then SIGINT and SIGCONT together. On resume the launch's own
// goroutine reads the ready frame and retargets the arm while the arm takes the pending signal; the
// arm won every one of 6 tries in a nested jail before the fix, each exiting at 20.02 s. When the
// goroutine wins instead, the arm runs the session's teardown, which is quick on either build, so
// this test can miss the defect on a run but never fails on a fixed build.
//
// THE SAME WINDOW WITH ANOTHER SESSION IN: the teardown's wait is then a session's quit, which the
// launch said and did not wait out (JL-D74's second half). Without that, the launch still waited
// the whole 20 s bound, 2 of 2 tries in a nested jail, for a keeper that was ending nothing. The
// line saying so ("stays up for") is the pre-ready teardown's alone: when the goroutine wins, the
// session's teardown runs, which does not say the jail stays up, as no session's signal teardown does.
// So this test logs which teardown ran and requires only what both do; requiring the line failed a
// fixed build on an arm64 CI runner, where the goroutine won one of two tries. The unit tier pins
// each order on its own (internal/cli/run/keeperreadywindow_test.go).
//
// EACH TRY IS A WORKSPACE OF ITS OWN. A nested podman sometimes cannot remove the jail's stopped
// container after so quick a stop (the `openByHandleAt` failure the design's §4.4 records, seen in
// 1 of 5 tries in one workspace); the keeper then leaves the jail unkept, as it should, and a next
// try in the same workspace would meet the leftover rather than test the window.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestASIGINTBetweenReadyAndTheFirstSessionEndsTheJailPromptly interrupts a fresh launch in that
// window, three times: each time the launch exits 130 well inside the 20 s bound, its keeper ends
// the jail, and no container of the workspace runs.
func TestASIGINTBetweenReadyAndTheFirstSessionEndsTheJailPromptly(t *testing.T) {
	requireJail(t)
	started := regexp.MustCompile(`keeper: started, pid (\d+)`)
	const bound = 10 * time.Second // half the launch's 20 s unwind bound
	for try := 1; try <= 3; try++ {
		dir := writeProject(t, `{}`)
		cname := naming.FromWorkspace(dir)
		logPath := filepath.Join(paths.GlobalStorage(), "logs", "jail-keeper-"+cname+".log")
		ready := "holds " + cname + " until its last session leaves"
		run := startYoloBackground(t, "interrupted", dir, `echo SESSION-IN-$((40+2)); sleep 600`)
		var m []string
		for deadline := time.Now().Add(jailTimeout()); ; time.Sleep(2 * time.Millisecond) {
			if m = started.FindStringSubmatch(run.combined()); m != nil {
				break
			}
			select {
			case err := <-run.done:
				t.Fatalf("try %d: the launch exited (%v) before its keeper started:\n%s", try, err, run.combined())
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("try %d: the launch did not start its keeper within %s:\n%s", try, jailTimeout(), run.combined())
			}
		}
		keeper, _ := strconv.Atoi(m[1])
		if err := syscall.Kill(run.pid, syscall.SIGSTOP); err != nil {
			t.Fatalf("stopping the launch: %v", err)
		}
		for deadline := time.Now().Add(jailTimeout()); ; time.Sleep(20 * time.Millisecond) {
			raw, _ := os.ReadFile(logPath)
			if strings.Contains(string(raw), ready) {
				break
			}
			if time.Now().After(deadline) || syscall.Kill(keeper, 0) != nil {
				_ = syscall.Kill(run.pid, syscall.SIGCONT)
				t.Fatalf("try %d: the keeper never said its jail was ready:\n%s\nthe launch:\n%s", try, raw, run.combined())
			}
		}
		_ = syscall.Kill(run.pid, syscall.SIGINT)
		sent := time.Now()
		if err := syscall.Kill(run.pid, syscall.SIGCONT); err != nil {
			t.Fatalf("resuming the launch: %v", err)
		}
		if rc := run.wait(t, jailTimeout()); rc != 128+int(syscall.SIGINT) {
			t.Errorf("try %d: the interrupted launch exited %d, want %d:\n%s", try, rc, 128+int(syscall.SIGINT), run.combined())
		}
		took := time.Since(sent)
		if took >= bound {
			t.Errorf("try %d: the launch took %s to exit after its SIGINT: it waited out its unwind bound for a "+
				"keeper that could not drain while the launch still counted itself in the jail:\n%s",
				try, took.Round(10*time.Millisecond), run.combined())
		}
		if !awaitProcessGone(keeper, 2*time.Minute) {
			t.Fatalf("try %d: the keeper (pid %d) of the interrupted launch is still running:\n%s", try, keeper, run.combined())
		}
		raw, _ := os.ReadFile(logPath)
		t.Logf("try %d: the launch exited %s after its SIGINT, its keeper %s after it; the keeper's log:\n%s", try,
			took.Round(10*time.Millisecond), time.Since(sent).Round(10*time.Millisecond), raw)
		if n := runningContainers(t, cname); n != 0 {
			t.Fatalf("try %d: %d containers named %s are running after the interrupted launch's keeper ended:\n%s",
				try, n, cname, run.combined())
		}
	}
}

// TestASIGINTInTheReadyWindowLeavesTheJailUpForAnotherSession is the same window with a second
// session already in the jail, attached while the first launch was held stopped after its keeper's
// ready. The interrupted launch is then one session leaving a jail another is in: it must exit well
// inside its bound, the other session must go on in the running jail, and once that session quits
// the keeper ends the jail. It says the jail stays up only when its pre-ready teardown took the
// signal, which is a race (the file's header).
func TestASIGINTInTheReadyWindowLeavesTheJailUpForAnotherSession(t *testing.T) {
	requireJail(t)
	started := regexp.MustCompile(`keeper: started, pid (\d+)`)
	const bound = 10 * time.Second // half the launch's 20 s unwind bound
	for try := 1; try <= 2; try++ {
		dir := writeProject(t, `{}`)
		cname := naming.FromWorkspace(dir)
		logPath := filepath.Join(paths.GlobalStorage(), "logs", "jail-keeper-"+cname+".log")
		ready := "holds " + cname + " until its last session leaves"
		first := startYoloBackground(t, "interrupted", dir, `echo SESSION-IN-$((40+2)); sleep 600`)
		var m []string
		for deadline := time.Now().Add(jailTimeout()); ; time.Sleep(2 * time.Millisecond) {
			if m = started.FindStringSubmatch(first.combined()); m != nil {
				break
			}
			select {
			case err := <-first.done:
				t.Fatalf("try %d: the launch exited (%v) before its keeper started:\n%s", try, err, first.combined())
			default:
			}
			if time.Now().After(deadline) {
				t.Fatalf("try %d: the launch did not start its keeper within %s:\n%s", try, jailTimeout(), first.combined())
			}
		}
		keeper, _ := strconv.Atoi(m[1])
		if err := syscall.Kill(first.pid, syscall.SIGSTOP); err != nil {
			t.Fatalf("stopping the launch: %v", err)
		}
		for deadline := time.Now().Add(jailTimeout()); ; time.Sleep(20 * time.Millisecond) {
			raw, _ := os.ReadFile(logPath)
			if strings.Contains(string(raw), ready) {
				break
			}
			if time.Now().After(deadline) || syscall.Kill(keeper, 0) != nil {
				_ = syscall.Kill(first.pid, syscall.SIGCONT)
				t.Fatalf("try %d: the keeper never said its jail was ready:\n%s\nthe launch:\n%s", try, raw, first.combined())
			}
		}
		// The second session attaches to the ready jail and waits there for the first session's
		// provisioning, which the held launch has not begun.
		other := startYoloBackground(t, "other", dir, `echo OTHER-IN-$((1+1)); sleep 600`)
		for deadline := time.Now().Add(jailTimeout()); !strings.Contains(other.combined(), "first session to begin provisioning"); time.Sleep(20 * time.Millisecond) {
			select {
			case err := <-other.done:
				_ = syscall.Kill(first.pid, syscall.SIGCONT)
				t.Fatalf("try %d: the second session exited (%v) before it was in the jail:\n%s", try, err, other.combined())
			default:
			}
			if time.Now().After(deadline) {
				_ = syscall.Kill(first.pid, syscall.SIGCONT)
				t.Fatalf("try %d: the second session never got into the jail:\n%s", try, other.combined())
			}
		}
		_ = syscall.Kill(first.pid, syscall.SIGINT)
		sent := time.Now()
		if err := syscall.Kill(first.pid, syscall.SIGCONT); err != nil {
			t.Fatalf("resuming the launch: %v", err)
		}
		if rc := first.wait(t, jailTimeout()); rc != 128+int(syscall.SIGINT) {
			t.Errorf("try %d: the interrupted launch exited %d, want %d:\n%s", try, rc, 128+int(syscall.SIGINT), first.combined())
		}
		took := time.Since(sent)
		t.Logf("try %d: the launch exited %s after its SIGINT", try, took.Round(10*time.Millisecond))
		if took >= bound {
			t.Errorf("try %d: the launch took %s to exit after its SIGINT: it waited out its unwind bound for a "+
				"keeper that keeps the jail up for its other session:\n%s", try, took.Round(10*time.Millisecond), first.combined())
		}
		if processGone(other.pid) {
			t.Fatalf("try %d: the other session ended with the interrupted launch:\n%s", try, other.combined())
		}
		if processGone(keeper) {
			t.Fatalf("try %d: the keeper ended a jail its other session is still in:\n%s", try, other.combined())
		}
		if n := runningContainers(t, cname); n != 1 {
			t.Fatalf("try %d: %d containers named %s run with a session still in the jail", try, n, cname)
		}
		if strings.Contains(first.combined(), "stays up for") {
			t.Logf("try %d: the arm took the signal before the launch retargeted it at ready; its "+
				"pre-ready teardown said the jail stays up for the other session", try)
		} else {
			t.Logf("try %d: the launch retargeted its arm at ready first; the session's teardown ran, "+
				"which does not say the jail stays up", try)
		}
		_ = syscall.Kill(other.pid, syscall.SIGINT)
		_ = other.wait(t, jailTimeout())
		if !awaitProcessGone(keeper, 2*time.Minute) {
			t.Fatalf("try %d: the keeper (pid %d) outlived the jail's last session:\n%s", try, keeper, other.combined())
		}
		if n := runningContainers(t, cname); n != 0 {
			t.Fatalf("try %d: %d containers named %s are running after the last session left", try, n, cname)
		}
	}
}
