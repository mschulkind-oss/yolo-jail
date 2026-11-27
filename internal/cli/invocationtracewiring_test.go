package cli

// invocationtracewiring_test.go pins who OWNS the launcher's trace at the front door
// (run.InvocationScope; plan slice U6b3 A): runRun supplies it, Run borrows it, and it is
// finalized after the title restore, which is itself inside the trace's lifetime.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// launcherLane reads the launcher lane of the one trace the launch left. It returns an error
// rather than failing, so it can run on a goroutine that is not the test's own.
func launcherLane() ([]map[string]any, error) {
	files, _ := filepath.Glob(filepath.Join(paths.TracesDir(), "*", "launcher-*.jsonl"))
	if len(files) != 1 {
		return nil, os.ErrNotExist
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		return nil, err
	}
	var lane []map[string]any
	for _, ln := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			return nil, err
		}
		lane = append(lane, m)
	}
	return lane, nil
}

// laneCount counts the lane's events of kind k named name.
func laneCount(lane []map[string]any, k, name string) int {
	n := 0
	for _, m := range lane {
		if m["k"] == k && m["name"] == name {
			n++
		}
	}
	return n
}

// laneIndex is the position of the first event of kind k named name, or -1.
func laneIndex(lane []map[string]any, k, name string) int {
	for i, m := range lane {
		if m["k"] == k && m["name"] == name {
			return i
		}
	}
	return -1
}

// TestRunRunInvocationTraceOutlivesTitleRestore drives the real runRun over the real run.Run with
// the title restore held on a channel: while the restore is paused the borrowed Run has not closed
// the invocation, and once it is released the restore's end precedes the invocation's, once.
func TestRunRunInvocationTraceOutlivesTitleRestore(t *testing.T) {
	withHomeAndCwd(t)
	t.Setenv("PATH", t.TempDir()) // no runtime: the launch refuses after it has acquired its trace
	t.Setenv(paths.TimingEnv, "1")
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv("YOLO_NO_TMUX", "")

	var held atomic.Bool
	entered := make(chan struct{})
	release := make(chan struct{})
	prevTTY, prevTmux := isattyStdin, tmuxCmd
	isattyStdin = func() bool { return true }
	tmuxCmd = func(args ...string) ([]byte, error) {
		if held.Load() {
			entered <- struct{}{}
			<-release
		}
		return nil, nil
	}
	t.Cleanup(func() { isattyStdin, tmuxCmd = prevTTY, prevTmux })
	prev := launchRunPipeline
	launchRunPipeline = func(o run.Options) int {
		rc := run.Run(o)
		held.Store(true) // everything after the pipeline is the title restore
		return rc
	}
	t.Cleanup(func() { launchRunPipeline = prev })

	done := make(chan struct{})
	go func() {
		defer close(done)
		captureBoth(t, func() { _ = runRun([]string{"--", "true"}) })
	}()
	<-entered
	lane, err := launcherLane()
	if err != nil {
		t.Fatalf("the launch left no launcher lane: %v", err)
	}
	if laneCount(lane, "start", "invocation") != 1 {
		t.Fatalf("want one invocation start, got lane %v", lane)
	}
	if laneCount(lane, "start", "process.title_restore") != 1 {
		t.Errorf("the title restore is not in the trace while paused: %v", lane)
	}
	if laneCount(lane, "end", "invocation") != 0 {
		t.Error("Run finalized the borrowed scope before the title restore finished")
	}
	close(release)
	<-done
	lane, err = launcherLane()
	if err != nil {
		t.Fatal(err)
	}
	if laneCount(lane, "end", "invocation") != 1 {
		t.Fatalf("want exactly one invocation end, got %v", lane)
	}
	if laneIndex(lane, "end", "process.title_restore") < 0 ||
		laneIndex(lane, "end", "process.title_restore") > laneIndex(lane, "end", "invocation") {
		t.Errorf("the title restore must end before the invocation does: %v", lane)
	}
}

// TestRunRunInvocationTraceFinalizesWithNoIndicator: the nil-indicator path has no restore defer
// and still closes the invocation.
func TestRunRunInvocationTraceFinalizesWithNoIndicator(t *testing.T) {
	withHomeAndCwd(t)
	t.Setenv("PATH", t.TempDir())
	t.Setenv(paths.TimingEnv, "1")
	t.Setenv("YOLO_NO_TMUX", "1")
	t.Setenv("KITTY_PID", "")
	prev := launchRunPipeline
	launchRunPipeline = run.Run
	t.Cleanup(func() { launchRunPipeline = prev })
	captureBoth(t, func() { _ = runRun([]string{"--", "true"}) })
	lane, err := launcherLane()
	if err != nil {
		t.Fatalf("the launch left no launcher lane: %v", err)
	}
	if laneCount(lane, "end", "invocation") != 1 {
		t.Errorf("want exactly one invocation end with no title indicator, got %v", lane)
	}
}
