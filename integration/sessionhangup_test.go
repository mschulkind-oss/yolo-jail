package integration

// sessionhangup_test.go is the end-to-end pin on an attached session's hangup
// (docs/design/jail-lifetime-last-session-wins.md §2.3 item 1, JL-D4, OQ-JL8 ruled A: a session's
// agent ends with its pane). Killing a `podman exec` client does not end what it started, so an
// attach whose terminal closed used to leave its command running in the jail with no terminal.
// The unit tiers pin each half (internal/entrypoint's hangup form and session records,
// internal/cli/run's arm and its call site); only a real jail proves the launcher's hangup
// reaches the session's processes through the runtime.

import (
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// inJailSleepCount counts the jail's `sleep 4321` processes, from /proc.
const inJailSleepCount = `n=0; for p in /proc/[0-9]*; do ` +
	`c=$(tr '\0' ' ' < "$p/cmdline" 2>/dev/null); case "$c" in "sleep 4321"*) n=$((n+1));; esac; done; ` +
	`echo "SLEEPS=$n"`

// TestAHungUpAttachEndsItsOwnSessionAndNoOther: two sessions in one jail. The second's launcher
// gets a SIGHUP, which is what a closed window or pane sends it. It exits 128+SIGHUP, its
// command is gone from the jail, and the jail and the first session run on untouched.
func TestAHungUpAttachEndsItsOwnSessionAndNoOther(t *testing.T) {
	requireJail(t)
	dir := writeProject(t, `{}`)
	cname := naming.FromWorkspace(dir)

	const release = "release-first"
	first := startYoloBackground(t, "first", dir,
		`echo FIRST-IN-$((40+2)); for _ in $(seq 1 600); do [ -f /workspace/`+release+` ] && break; sleep 0.5; done; `+
			`echo FIRST-OUT-$((40+2))`)
	t.Cleanup(func() { writeRelease(t, dir, release) })
	awaitOutput(t, first, regexp.MustCompile(`FIRST-IN-42`))
	awaitLaunchLockReleased(t, dir, first)

	attach := startYoloBackground(t, "attach", dir, `echo ATTACH-IN-$((40+2)); exec sleep 4321`)
	awaitOutput(t, attach, regexp.MustCompile(`ATTACH-IN-42`))
	if !strings.Contains(attach.combined(), "Attaching to existing jail") {
		t.Fatalf("the second launch did not attach:\n%s", attach.combined())
	}
	if before := runYolo(t, dir, inJailSleepCount); !strings.Contains(before.stdout, "SLEEPS=1") {
		t.Fatalf("the attached session's command is not running in the jail:\n%s", before.combined())
	}

	if err := syscall.Kill(attach.pid, syscall.SIGHUP); err != nil {
		t.Fatalf("hanging up the attach's launcher: %v", err)
	}
	if rc := attach.wait(t, jailTimeout()); rc != 128+int(syscall.SIGHUP) {
		t.Errorf("the hung-up attach returned %d, want %d:\n%s", rc, 128+int(syscall.SIGHUP), attach.combined())
	}

	// The hangup is sent before the launcher exits; give the jail's processes a moment to act on it.
	var after result
	for deadline := time.Now().Add(10 * time.Second); ; {
		after = runYolo(t, dir, inJailSleepCount)
		if strings.Contains(after.stdout, "SLEEPS=0") || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(after.stdout, "SLEEPS=0") {
		t.Errorf("the hung-up session's command is still running in the jail:\n%s", after.combined())
	}
	if n := runningContainers(t, cname); n != 1 {
		t.Fatalf("the jail is not running after one of its sessions was hung up (%d containers)", n)
	}
	select {
	case err := <-first.done:
		t.Fatalf("the first session ended (%v) when the second was hung up:\n%s", err, first.combined())
	default:
	}

	writeRelease(t, dir, release)
	if rc := first.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("the first session ended rc %d:\n%s", rc, first.combined())
	}
	if !strings.Contains(first.combined(), "FIRST-OUT-42") {
		t.Errorf("the first session did not finish its command:\n%s", first.combined())
	}
}
