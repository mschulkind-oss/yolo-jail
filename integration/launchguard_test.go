package integration

// launchguard_test.go is the end-to-end pin on a fresh launch interrupted before its keeper exists
// (docs/design/jail-lifetime-last-session-wins.md JL-D75): a SIGINT while the image builds ends the
// launch through its launch guard, which removes the pack tree it staged. Before the guard nothing
// caught a signal there, so the process died by the default action, ran no cleanup, and left the
// tree under AGENTS_DIR/<cname>/pack-trees, used by no container (a nested jail showed three). The
// unit tier drives the same moment through Run with a faked image build
// (internal/cli/run/launchguard_test.go); this one runs the real build and the real runtime.

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestASIGINTBeforeTheKeeperLeavesNoPackTree interrupts a fresh launch the moment it names the jail
// binaries it mounts, the line before its image build. The launch exits 130, and once it and any
// keeper it got to spawn are gone, no pack tree of the workspace is left and no container runs.
func TestASIGINTBeforeTheKeeperLeavesNoPackTree(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)
	cname := naming.FromWorkspace(dir)
	run := startYoloBackground(t, "interrupted", dir, `echo SESSION-IN-$((40+2)); sleep 600`)
	// Polled tightly: the image build after this line is the window under test.
	for deadline := time.Now().Add(jailTimeout()); !strings.Contains(run.combined(), "Jail binaries:"); time.Sleep(5 * time.Millisecond) {
		select {
		case err := <-run.done:
			t.Fatalf("the launch exited (%v) before it named its jail binaries:\n%s", err, run.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the launch did not reach its image build within %s:\n%s", jailTimeout(), run.combined())
		}
	}
	if err := syscall.Kill(run.pid, syscall.SIGINT); err != nil {
		t.Fatalf("interrupting the launch: %v", err)
	}
	if rc := run.wait(t, jailTimeout()); rc != 128+int(syscall.SIGINT) {
		t.Errorf("the launch interrupted at its image build exited %d, want %d (a launch the signal's "+
			"default action ended reports -1):\n%s", rc, 128+int(syscall.SIGINT), run.combined())
	}
	// The interrupt is meant to land before the keeper's spawn. Should a fast build have let the
	// launch get that far first, the keeper owns the tree and unwinds it: wait for it, and the
	// claim below holds all the same.
	if m := regexp.MustCompile(`keeper: started, pid (\d+)`).FindStringSubmatch(run.combined()); m != nil {
		keeper, _ := strconv.Atoi(m[1])
		t.Logf("the launch reached its keeper (pid %d) before the interrupt; waiting for its unwind", keeper)
		if !awaitProcessGone(keeper, 2*time.Minute) {
			t.Fatalf("the keeper (pid %d) of the interrupted launch is still running:\n%s", keeper, run.combined())
		}
	}
	entries, err := os.ReadDir(paths.PackTreeRoot(cname))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		if e.IsDir() {
			left = append(left, e.Name())
		}
	}
	if len(left) != 0 {
		t.Errorf("the interrupted launch left %v under %s, which no container holds:\n%s", left,
			paths.PackTreeRoot(cname), run.combined())
	}
	if n := runningContainers(t, cname); n != 0 {
		t.Errorf("%d containers named %s are running after a launch interrupted before its keeper:\n%s",
			n, cname, run.combined())
	}
}
