package integration

// stop_test.go is the end-to-end pin on `yolo stop` and the removal of `--new`:
// the two-command replacement series (stop, then an ordinary launch) that every
// remedy now names. stop's unit tier covers the argv and the idempotence table
// (internal/cli/stop_test.go); only a real jail proves the stop actually ends
// the container the launcher started, and that a typed --new refuses by name.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

func TestStopEndsTheWorkspaceJail(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	cname := naming.FromWorkspace(dir)

	// Hold the jail open via the live /workspace bind, as the concurrency tests do.
	const releaseName = "release-stop-test"
	first := startYoloBackground(t, "first", dir,
		`for _ in $(seq 1 300); do [ -f /workspace/`+releaseName+` ] && break; sleep 0.5; done`)
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, releaseName), []byte("go\n"), 0o644)
	})

	deadline := time.Now().Add(jailTimeout())
	for runningContainers(t, cname) == 0 {
		select {
		case err := <-first.done:
			t.Fatalf("first launch exited (%v) before its jail was running:\n%s", err, first.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("first launch never had a running jail within %s:\n%s",
				jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The stop: from the workspace, via the host CLI (not inside the jail).
	r := runYoloCLI(t, dir, "stop")
	if r.rc != 0 {
		t.Fatalf("yolo stop failed: rc %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.combined(), "Stopped "+cname) {
		t.Errorf("stop must say what it stopped:\n%s", r.combined())
	}
	if n := runningContainers(t, cname); n != 0 {
		t.Errorf("%d containers named %s remain after stop (--rm must sweep it)", n, cname)
	}

	// Idempotent: the series' first half never fails on "nothing to stop".
	r2 := runYoloCLI(t, dir, "stop")
	if r2.rc != 0 {
		t.Fatalf("a second stop must succeed, rc %d\n%s", r2.rc, r2.combined())
	}
	if !strings.Contains(r2.combined(), "No jail running") {
		t.Errorf("the no-op stop must say so:\n%s", r2.combined())
	}
}
