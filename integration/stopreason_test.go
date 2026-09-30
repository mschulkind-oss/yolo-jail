package integration

// stopreason_test.go is the end-to-end pin on §2.3 item 3 of
// docs/design/jail-lifetime-last-session-wins.md (JL-D53): a session attached to a jail that
// ends under it prints why. The unit tier pins each writer's record and the attach's reading of
// it against a faked runtime (internal/cli/run/stopreason_test.go); only a real jail proves the
// status a jail's end gives an exec, that the runtime says the jail is gone by the time the
// attach asks, and that the record is there to read.

import (
	"regexp"
	"strings"
	"testing"
)

// TestAnAttachWhoseJailEndedSaysWhy: a second terminal is attached when the jail ends, first
// because the session that started it quit, then because `yolo stop` stopped it. Each time the
// attach returns 137, the status of a process the jail's end killed, and says what ended it.
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

	t.Run("its first session quit", func(t *testing.T) {
		dir := writeProject(t, `{}`)
		first, attach := start(t, dir)
		writeRelease(t, dir, release)
		if rc := first.wait(t, jailTimeout()); rc != 0 {
			t.Errorf("the first session ended rc %d:\n%s", rc, first.combined())
		}
		if rc := attach.wait(t, jailTimeout()); rc != 137 {
			t.Errorf("the attached session ended rc %d, want 137 from its jail's end:\n%s", rc, attach.combined())
		}
		if !strings.Contains(attach.combined(),
			"This session ended because its jail stopped: the session that started it ended") {
			t.Errorf("the attached session was not told why its jail ended:\n%s", attach.combined())
		}
	})

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
}
