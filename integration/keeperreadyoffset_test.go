package integration

// keeperreadyoffset_test.go is the end-to-end pin on the boundary between what a fresh launch's
// relay prints of its keeper and what its first session's quit replays from the keeper's log
// (docs/design/jail-lifetime-last-session-wins.md JL-D78). The relay stops at the keeper's ready
// frame, and the quit replays the log from an offset the launch used to take by a stat once its
// relay was done: a host service that died between the two was in neither, so the first terminal
// was never told. The keeper's ready frame now carries that offset. The unit tier lands the death
// at the launch's own clock read in that window (internal/cli/run/keeperreadyoffset_test.go).
//
// THE WINDOW IS HELD OPEN BY STOPPING THE LAUNCH, as keeperreadywindow_test.go does: SIGSTOP once
// its keeper has started, until the keeper's log says the jail is ready, which it logs just before
// its frame. The daemon is killed a moment after that line, and the launch is resumed only once the
// keeper has logged the death, so the death is after the frame and before the launch could take
// any offset of its own.

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestADeathInTheReadyWindowIsToldOnceAtTheFirstSessionsQuit: a jail's keeper runs a host daemon the
// user config declares; the daemon is killed after the keeper's ready and before the first session
// starts; that session, the jail's only one, is told once, by its quit's stream of the keeper's
// teardown.
func TestADeathInTheReadyWindowIsToldOnceAtTheFirstSessionsQuit(t *testing.T) {
	requireJail(t)
	if goruntime.GOOS != "linux" {
		t.Skip("has run only on Linux: the keeper's record of a host service that goes down is " +
			"unmeasured on either Mac backend (docs/design/jail-lifetime-last-session-wins.md JL-D71)")
	}
	socat, err := exec.LookPath("socat")
	if err != nil {
		t.Skip("the stand-in host daemon is socat, which is not on this host's PATH")
	}
	const service = "ro-down-probe"
	// Through `sh -c`, as keeperwatch_test.go's stand-in: the placement rule would refuse socat's
	// "UNIX-LISTEN:<socket>,fork" as a workspace path.
	packHome(t, `{"loopholes": {"`+service+`": {"command": ["sh", "-c", "exec `+socat+
		` UNIX-LISTEN:\"$0\",fork EXEC:cat", "{socket}"]}}}`)
	dir := writeProject(t, `{}`)
	cname := naming.FromWorkspace(dir)
	logPath := filepath.Join(paths.GlobalStorage(), "logs", "jail-keeper-"+cname+".log")
	ready := "holds " + cname + " until its last session leaves"
	want := "host service '" + service + "' went down"
	started := regexp.MustCompile(`keeper: started, pid (\d+)`)

	run := startYoloBackground(t, "first", dir, `echo SESSION-IN-$((40+2))`)
	var m []string
	for deadline := time.Now().Add(jailTimeout()); ; time.Sleep(2 * time.Millisecond) {
		if m = started.FindStringSubmatch(run.combined()); m != nil {
			break
		}
		select {
		case err := <-run.done:
			t.Fatalf("the launch exited (%v) before its keeper started:\n%s", err, run.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the launch did not start its keeper within %s:\n%s", jailTimeout(), run.combined())
		}
	}
	keeper, _ := strconv.Atoi(m[1])
	if err := syscall.Kill(run.pid, syscall.SIGSTOP); err != nil {
		t.Fatalf("stopping the launch: %v", err)
	}
	resumed := false
	resume := func() {
		if !resumed {
			resumed = true
			_ = syscall.Kill(run.pid, syscall.SIGCONT)
		}
	}
	t.Cleanup(resume)
	for deadline := time.Now().Add(jailTimeout()); ; time.Sleep(20 * time.Millisecond) {
		raw, _ := os.ReadFile(logPath)
		if strings.Contains(string(raw), ready) {
			break
		}
		if time.Now().After(deadline) || syscall.Kill(keeper, 0) != nil {
			resume()
			t.Fatalf("the keeper never said its jail was ready:\n%s\nthe launch:\n%s", raw, run.combined())
		}
	}
	// The frame follows that line at once; a moment's margin past it all the same.
	time.Sleep(200 * time.Millisecond)

	// THE DAEMON GOES DOWN in the window, by a kill from outside.
	daemons := childrenMatching(keeper, "UNIX-LISTEN:", service)
	if len(daemons) == 0 {
		raw, _ := os.ReadFile(logPath)
		resume()
		t.Fatalf("no %s daemon is running under the keeper; the keeper's log:\n%s", service, raw)
	}
	for _, pid := range daemons {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	for deadline := time.Now().Add(jailTimeout()); ; time.Sleep(20 * time.Millisecond) {
		raw, _ := os.ReadFile(logPath)
		if log := string(raw); strings.Contains(log, want) {
			if strings.Index(log, want) < strings.Index(log, ready) {
				resume()
				t.Fatalf("the keeper logged the death before its ready, so this run says nothing about the window:\n%s", log)
			}
			break
		}
		if time.Now().After(deadline) {
			resume()
			t.Fatalf("the keeper never recorded the daemon's death in %s:\n%s", logPath, raw)
		}
	}
	resume()

	if rc := run.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("the session ended rc %d:\n%s", rc, run.combined())
	}
	out := run.combined()
	if n := strings.Count(out, want); n != 1 {
		t.Errorf("a daemon that died between the keeper's ready and the first session was named %d times "+
			"to that session's terminal, want once, by its quit:\n%s", n, out)
	} else if strings.Index(out, want) < strings.Index(out, "SESSION-IN-42") {
		t.Errorf("the death was named before the session ran, so not by its quit:\n%s", out)
	}
	if !awaitProcessGone(keeper, 2*time.Minute) {
		t.Errorf("the keeper (pid %d) is still running after its last session quit", keeper)
	}
	if n := runningContainers(t, cname); n != 0 {
		t.Errorf("%d containers named %s remain after the last session quit", n, cname)
	}
}
