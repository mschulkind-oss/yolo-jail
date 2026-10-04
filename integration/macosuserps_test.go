package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// `yolo ps` AND `yolo prune` ON macos-user, against a session that is really running.
//
// THE DEFECT, measured: with the runtime set to macos-user, `yolo ps` executed `macos-user ps` and
// printed a red "Could not query the macos-user runtime", and `yolo prune` printed FAILED and
// exited 1, because both addressed container probes to a binary named after the runtime. The
// backend runs no container; its jails are its host-services sessions, each holding a lock on its
// own dir for as long as its sandbox runs (internal/runtime/macosusersessions.go). This test holds
// one session open on a sync file and asks both commands about it from the host.

// macosUserSyncDir makes a directory in the workspace that the sandbox account and this process
// can both write, for a session script and the test to signal each other through.
func macosUserSyncDir(t *testing.T, ws, name string) string {
	t.Helper()
	dir := filepath.Join(ws, name)
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	// Mkdir's mode is filtered by the umask; the sandbox account writes here too.
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	return dir
}

// heldSessionScript is a session that says it is up, waits (bounded) for the test's release, and
// ends. extraUp runs before the up marker, extraDown after the release.
func heldSessionScript(sync, extraUp, extraDown string) string {
	return fmt.Sprintf(`set -e
%[2]s
touch %[1]s/up
w=0; while [ ! -e %[1]s/release ] && [ $w -lt 900 ]; do sleep 1; w=$((w+1)); done
%[3]s
echo "=== END ==="`, sync, extraUp, extraDown)
}

// heldSession is one background macos-user launch whose session script waits on a sync dir.
type heldSession struct {
	sync   string
	launch <-chan hd10Result
	result *hd10Result
}

// holdSession starts a held session of script in ws, and releases it at cleanup if the test
// has not, so a failed assertion never leaves a sandbox running.
func holdSession(t *testing.T, ws, sync, script string) *heldSession {
	t.Helper()
	h := &heldSession{sync: sync, launch: hd10Launch(t, ws, script, hd10LaunchEnv())}
	t.Cleanup(func() { h.release(t) })
	return h
}

// wait returns the launch's result, once it has ended.
func (h *heldSession) wait() hd10Result {
	if h.result == nil {
		r := <-h.launch
		h.result = &r
	}
	return *h.result
}

// awaitUp waits for the session's up marker, failing if its launch ends first.
func (h *heldSession) awaitUp(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(macosUserTimeout())
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(h.sync, "up")); err == nil {
			return
		}
		select {
		case r := <-h.launch:
			h.result = &r
			t.Fatalf("the launch ended (rc %d) before its session was up:\n%s", r.rc, lastLines(r.combined(), 60))
		case <-time.After(time.Second):
		}
	}
	t.Fatalf("the session was not up within %s", macosUserTimeout())
}

// release lets the session end and returns its launch's result.
func (h *heldSession) release(t *testing.T) hd10Result {
	t.Helper()
	if h.result == nil {
		if err := os.WriteFile(filepath.Join(h.sync, "release"), []byte("go\n"), 0o644); err != nil {
			t.Error(err)
		}
	}
	return h.wait()
}

// psJSON is the part of `yolo ps --format json` this test reads.
type psJSON struct {
	Runtime    string `json:"runtime"`
	Enumerated bool   `json:"enumerated"`
	Jails      []struct {
		Name, Status, Workspace string
	} `json:"jails"`
}

func runPsJSON(t *testing.T, ws string) psJSON {
	t.Helper()
	r := runCommand(t, ws, []string{"ps", "--format", "json"}, macosUserRunEnv())
	var rep psJSON
	if r.rc != 0 || json.Unmarshal([]byte(r.stdout), &rep) != nil {
		t.Fatalf("`yolo ps --format json` rc %d did not print a report:\n%s", r.rc, r.combined())
	}
	return rep
}

func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// TestMacosUserPsListsItsLiveSession: while a macos-user session runs, `yolo ps` lists it with
// its workspace and `yolo prune` exits 0, says FAILED nowhere and tracks that workspace; once the
// session has ended, `yolo ps` no longer lists it.
func TestMacosUserPsListsItsLiveSession(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": []}`)
	ws := macosUserWorkspace(t, `{}`)
	sync := macosUserSyncDir(t, ws, "ps-sync")
	session := holdSession(t, ws, sync, heldSessionScript(sync, "", ""))
	session.awaitUp(t)

	rep := runPsJSON(t, ws)
	if rep.Runtime != "macos-user" || !rep.Enumerated {
		t.Fatalf("`yolo ps` answered runtime %q, enumerated %v; want an enumerated macos-user listing",
			rep.Runtime, rep.Enumerated)
	}
	listed := false
	for _, j := range rep.Jails {
		if sameDir(j.Workspace, ws) {
			listed = true
			if j.Status != "running (macos-user session)" {
				t.Errorf("the running session lists with status %q", j.Status)
			}
		}
	}
	if !listed {
		t.Errorf("`yolo ps` does not list the running session of %s: %+v", ws, rep.Jails)
	}

	p := runCommand(t, ws, []string{"prune"}, macosUserRunEnv())
	if p.rc != 0 || strings.Contains(p.combined(), "FAILED") {
		t.Errorf("`yolo prune` on macos-user: rc %d (want 0), output:\n%s", p.rc, p.combined())
	}
	if !strings.Contains(p.stdout, "Runtime: macos-user") || !strings.Contains(p.stdout, "  • "+ws) {
		t.Errorf("`yolo prune` does not track the running session's workspace %s:\n%s", ws, p.stdout)
	}
	if !strings.Contains(p.stdout, "not applicable — macos-user runs no containers or images") {
		t.Errorf("`yolo prune`'s container sections do not say they do not apply:\n%s", p.stdout)
	}

	r := session.release(t)
	if r.rc != 0 || !strings.Contains(r.stdout, "=== END ===") {
		t.Fatalf("the held session did not end cleanly (rc %d):\n%s", r.rc, lastLines(r.combined(), 60))
	}
	for _, j := range runPsJSON(t, ws).Jails {
		if sameDir(j.Workspace, ws) {
			t.Errorf("after the session ended, `yolo ps` still lists it: %+v", j)
		}
	}
}
