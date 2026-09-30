package integration

// stopreason_test.go is the end-to-end pin on §2.3 item 3 of
// docs/design/jail-lifetime-last-session-wins.md (JL-D53): a session attached to a jail that
// ends under it prints why. The unit tier pins each writer's record and the attach's reading of
// it against a faked runtime (internal/cli/run/stopreason_test.go); only a real jail proves the
// status a jail's end gives an exec, that the runtime says the jail is gone by the time the
// attach asks, and that the record is there to read.

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestAnAttachWhoseJailEndedSaysWhy: a second terminal is attached when the jail ends, first because
// `yolo stop` stopped it, then because a stop from outside yolo did. Each time the attach returns
// 137, the status of a process the jail's end killed, and says what ended it, or that nothing
// recorded why. (The session that started the jail quitting ends nothing any more:
// TestQuittingTheFirstSessionLeavesTheOthersRunning.)
func TestAnAttachWhoseJailEndedSaysWhy(t *testing.T) {
	requireJail(t)
	const release = "release-first"
	start := func(t *testing.T, dir string) (first, attach *bgRun) {
		t.Helper()
		first = startYoloBackground(t, "first", dir,
			`echo FIRST-IN-$((40+2)); for _ in $(seq 1 600); do [ -f /workspace/`+release+` ] && break; sleep 0.5; done`)
		t.Cleanup(func() { writeRelease(t, dir, release) })
		awaitOutput(t, first, regexp.MustCompile(`FIRST-IN-42`))
		awaitLaunchLockReleased(t, dir, first)
		attach = startYoloBackground(t, "attach", dir, `echo ATTACH-IN-$((40+2)); exec sleep 600`)
		awaitOutput(t, attach, regexp.MustCompile(`ATTACH-IN-42`))
		if !strings.Contains(attach.combined(), "Attaching to existing jail") {
			t.Fatalf("the second launch did not attach:\n%s", attach.combined())
		}
		return first, attach
	}

	t.Run("yolo stop", func(t *testing.T) {
		dir := writeProject(t, `{}`)
		first, attach := start(t, dir)
		// Not required to succeed: a nested podman cannot clean up an exec session that is live at
		// a stop (`openByHandleAt failed: operation not permitted`, the failure §4.4 of the design
		// records for --rm), and says so with 125 while the stop itself goes through. What is
		// pinned is what the attached session is told.
		if stop := runYoloCLI(t, dir, "stop"); stop.rc != 0 {
			t.Logf("yolo stop returned %d:\n%s", stop.rc, stop.combined())
		}
		if rc := attach.wait(t, jailTimeout()); rc != 137 {
			t.Errorf("the attached session ended rc %d, want 137 from its jail's end:\n%s", rc, attach.combined())
		}
		if !strings.Contains(attach.combined(), "This session ended because its jail stopped: `yolo stop` (pid ") {
			t.Errorf("the attached session was not told that yolo stop ended its jail:\n%s", attach.combined())
		}
		if rc := first.wait(t, jailTimeout()); rc != 143 {
			t.Errorf("the first session ended rc %d, want 143 from a stopped jail:\n%s", rc, first.combined())
		}
	})

	// A stop from outside yolo records nothing, so the attached session says that nothing did, and
	// so does the session that started the jail, whose exec returned 137 because its jail ended too.
	t.Run("a stop from outside yolo", func(t *testing.T) {
		dir := writeProject(t, `{}`)
		first, attach := start(t, dir)
		cname := naming.FromWorkspace(dir)
		rt := detectRuntime()
		ctx, cancel := context.WithTimeout(context.Background(), jailTimeout())
		defer cancel()
		// The same tolerance as `yolo stop` above: a nested podman may say 125 at a stop with a
		// live exec session while the stop itself goes through.
		if out, err := exec.CommandContext(ctx, rt, "stop", "-t", "5", cname).CombinedOutput(); err != nil {
			t.Logf("%s stop: %v\n%s", rt, err, out)
		}
		if rc := attach.wait(t, jailTimeout()); rc != 137 {
			t.Errorf("the attached session ended rc %d, want 137 from its jail's end:\n%s", rc, attach.combined())
		}
		got := attach.combined()
		if !strings.Contains(got, "This session ended because its jail stopped, and nothing recorded why") {
			t.Errorf("the attached session was not told that nothing recorded why its jail ended:\n%s", got)
		}
		// Nothing recorded a stop, so the first session keeps its exec's status (JL-D59).
		if rc := first.wait(t, jailTimeout()); rc != 137 {
			t.Errorf("the first session ended rc %d, want 137 from its jail's end:\n%s", rc, first.combined())
		}
		if !strings.Contains(first.combined(), "nothing recorded why") {
			t.Errorf("the first session was not told its jail ended:\n%s", first.combined())
		}
	})
}
