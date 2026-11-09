package integration

// stop_test.go is the end-to-end pin on `yolo stop` and the removal of `--new`:
// the two-command replacement series (stop, then an ordinary launch) that every
// remedy now names. stop's unit tier covers the argv and the idempotence table
// (internal/cli/stop_test.go); only a real jail proves the stop actually ends
// the container the launcher started, and that a typed --new refuses by name.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestStopEndsTheWorkspaceJail(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	cname := jailName(dir)

	// Hold the jail open via the live /workspace bind, as the concurrency tests do.
	const releaseName = "release-stop-test"
	first := startYoloBackground(t, "first", dir,
		`echo "SESSION-IN-$((40+2))"; for _ in $(seq 1 300); do [ -f /workspace/`+releaseName+` ] && break; sleep 0.5; done`)
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
	// And its first session is running its command, so the stop ends a session in progress.
	awaitOutput(t, first, regexp.MustCompile(`SESSION-IN-42`))

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

	// The launch that started the jail ends with 128+SIGTERM, as it did while its session was
	// the container's main process. The kernel SIGKILLs the session when the stopped hold
	// exits, so a launcher reading its exec alone reports 137, and on a Mac's small Podman
	// machine then blames the VM's OOM killer for a stop.
	if rc := first.wait(t, jailTimeout()); rc != 143 {
		t.Errorf("the stopped jail's launch returned %d, want 143:\n%s", rc, first.combined())
	}
	if strings.Contains(first.combined(), "OOM") {
		t.Errorf("the stopped jail's launch blamed the OOM killer:\n%s", first.combined())
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

// TestStopStopsEverySetupsJail is W-SU1's Linux half (named-setups-plan.md slice 6b): two setups
// of one workspace run two jails at once; `yolo setup list` marks both running, and one
// `yolo stop` ends both, naming each, whichever setup is selected. The second setup is a
// user-defined podman one, so the test needs no backend beyond the suite's own.
func TestStopStopsEverySetupsJail(t *testing.T) {
	requireJail(t)
	packHome(t, `{"setups": {"lab": {"confinement": "podman"}}}`)
	dir := writeProject(t, `{}`)
	const releaseName = "release-setups-stop-test"
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, releaseName), []byte("go\n"), 0o644)
	})
	runs := map[string]*bgRun{}
	for _, name := range []string{"podman-self", "lab"} {
		runs[name] = startYoloBackground(t, name, dir,
			`echo "UP-IN-$YOLO_SETUP"; for _ in $(seq 1 300); do [ -f /workspace/`+releaseName+` ] && break; sleep 0.5; done`,
			"YOLO_SETUP="+name)
	}
	for name, r := range runs {
		awaitOutput(t, r, regexp.MustCompile(`UP-IN-`+name+`\n`))
	}

	list := runCommand(t, dir, []string{"setup", "list"}, withHostSemantics(), withEnv("YOLO_SETUP=podman-self"))
	for name := range runs {
		marked := false
		for _, line := range strings.Split(list.stdout, "\n") {
			if f := strings.Fields(line); len(f) > 0 && f[0] == name && strings.HasSuffix(line, ", running") {
				marked = true
			}
		}
		if !marked {
			t.Errorf("`yolo setup list` does not mark %s running:\n%s", name, list.combined())
		}
	}

	r := runCommand(t, dir, []string{"stop"}, withHostSemantics(), withEnv("YOLO_SETUP=podman-self"))
	if r.rc != 0 {
		t.Fatalf("yolo stop failed: rc %d\n%s", r.rc, r.combined())
	}
	for name := range runs {
		if n := strings.Count(r.stdout, "stopped "+name+"\n"); n != 1 {
			t.Errorf("the stop said `stopped %s` %d times, want once:\n%s", name, n, r.combined())
		}
		if n := runningContainers(t, jailNameOn(dir, name)); n != 0 {
			t.Errorf("%d containers of %s remain after the stop", n, name)
		}
	}
	for name, run := range runs {
		if rc := run.wait(t, jailTimeout()); rc != 143 {
			t.Errorf("the %s launch returned %d, want 143 (stopped):\n%s", name, rc, run.combined())
		}
	}
}
