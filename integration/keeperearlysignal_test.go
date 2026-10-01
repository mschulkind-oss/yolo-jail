package integration

// keeperearlysignal_test.go is the end-to-end pin on a fresh launch interrupted in the moment after
// its keeper said it started (docs/design/jail-lifetime-last-session-wins.md §9.5 item 1): a SIGINT
// to the launcher then ends the jail it was starting, and leaves no container running with nothing
// that will end it. Before the fix a nested jail left one behind in 3 of 6 tries, the keeper having
// stopped the jail before the runtime had made its container. The unit tier drives each moment of
// that window against a fake runtime (internal/cli/run/keeperearlysignal_test.go); only a real
// runtime has the lag between a client's start and its container's.

import (
	"regexp"
	"strconv"
	"syscall"
	"testing"
	"time"

	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestASIGINTRightAfterTheKeeperStartedLeavesNoJailRunning interrupts a fresh launch at several
// points just after "keeper: started", while the keeper starts the jail's host services and the
// runtime makes its container: each time the launch exits 130, its keeper ends, and once a container
// still on its way up would have come up, none of the workspace's is running.
func TestASIGINTRightAfterTheKeeperStartedLeavesNoJailRunning(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)
	cname := naming.FromWorkspace(dir)
	started := regexp.MustCompile(`keeper: started, pid (\d+)`)
	for _, delay := range []time.Duration{0, 100 * time.Millisecond, 200 * time.Millisecond} {
		run := startYoloBackground(t, "interrupted", dir, `echo SESSION-IN-$((40+2)); sleep 600`)
		// Polled tightly: the window is the few hundred milliseconds before the container exists.
		var m []string
		for deadline := time.Now().Add(jailTimeout()); ; time.Sleep(5 * time.Millisecond) {
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
		time.Sleep(delay)
		if err := syscall.Kill(run.pid, syscall.SIGINT); err != nil {
			t.Fatalf("interrupting the launch: %v", err)
		}
		if rc := run.wait(t, jailTimeout()); rc != 128+int(syscall.SIGINT) {
			t.Errorf("after %s the interrupted launch exited %d, want %d:\n%s", delay, rc,
				128+int(syscall.SIGINT), run.combined())
		}
		if !awaitProcessGone(keeper, 2*time.Minute) {
			t.Fatalf("after %s the keeper (pid %d) of the interrupted launch is still running:\n%s",
				delay, keeper, run.combined())
		}
		time.Sleep(3 * time.Second)
		if n := runningContainers(t, cname); n != 0 {
			t.Fatalf("after %s, %d containers named %s are running with no keeper, the launch that "+
				"started them interrupted before they were ready:\n%s", delay, n, cname, run.combined())
		}
	}
}
