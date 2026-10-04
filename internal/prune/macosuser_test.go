package prune

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// macosuser_test.go pins `yolo prune` on the macos-user runtime, and the one thing every runtime's
// staging sweep now owes it.
//
// THE DEFECT, measured: with YOLO_RUNTIME=macos-user, prune addressed every container probe to a
// binary named `macos-user`, which does not exist. The old-image sweep read that as a decline and
// printed FAILED, so the command exited 1 on every Mac that used the backend, and the stopped
// containers section printed an affirmative "none" without having asked anything.

// redirectSessions points the host-services base at a dir of the test's own, so the sessions a
// test plants are the only ones a sweep can find, and returns it.
func redirectSessions(t *testing.T) string {
	t.Helper()
	prev := paths.HostSingletonDir
	paths.HostSingletonDir = t.TempDir()
	t.Cleanup(func() { paths.HostSingletonDir = prev })
	return paths.HostServicesBase(false)
}

// plantSession makes a session dir the way the launch's openServicesSession does: "live" holds
// its lock on a descriptor of the test's own, "gone" leaves it free, "nolock" makes no lock file
// (a session starting up).
func plantSession(t *testing.T, base, cname, state, workspace string) {
	t.Helper()
	dir, err := os.MkdirTemp(base, paths.HostServicesSessionPrefix(cname))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteSessionRecord(dir, runtime.SessionRecord{
		Notch: runtime.NotchMacosUser, Workspace: workspace, Name: cname}); err != nil {
		t.Fatal(err)
	}
	if state == "nolock" {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, paths.HostServicesSessionLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if state == "gone" {
		_ = f.Close()
		return
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
}

// plantStaging makes AGENTS_DIR/<name> with a file in it, older than the sweep's age floor
// against baseOpts' clock.
func plantStaging(t *testing.T, agents, name string) string {
	t.Helper()
	dir := filepath.Join(agents, name)
	mustMkdir(t, filepath.Join(dir, "pack-trees"))
	mustWrite(t, filepath.Join(dir, "pack-trees", "marker"), []byte("staged"))
	backdate(t, dir, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	return dir
}

// noMacosUserExec is a container-runtime seam that fails the test for any probe addressed to
// `macos-user`, which names no binary.
func noMacosUserExec(t *testing.T) RunFunc {
	return func(argv []string, _ time.Duration) ProbeResult {
		if len(argv) > 0 && argv[0] == "macos-user" {
			t.Errorf("prune ran %v; macos-user has no runtime binary to ask", argv)
			return ProbeResult{Ran: false}
		}
		return ProbeResult{Ran: true}
	}
}

// TestPruneOnMacosUserAsksNoRuntimeAndExitsZero: every container-only section says it does not
// apply and names the command that prunes a container runtime instead, nothing is declined, the
// command exits 0, and the workspaces it tracks are its sessions'.
func TestPruneOnMacosUserAsksNoRuntimeAndExitsZero(t *testing.T) {
	base := redirectSessions(t)
	ws := t.TempDir()
	plantSession(t, base, "yolo-ws-live", "live", ws)
	plantSession(t, base, "yolo-ws-ended", "gone", filepath.Join(t.TempDir(), "ended"))

	for _, apply := range []bool{false, true} {
		o, _ := baseOpts(t)
		o.DetectRuntime = func() string { return "macos-user" }
		o.Exec = noMacosUserExec(t)
		o.Apply = apply
		o.NixGC = true
		o.InJail = func() bool { return false }
		o.NixStoreGC = func(int64, bool) StoreGCOutcome {
			t.Error("prune ran a store GC on macos-user")
			return StoreGCOutcome{}
		}
		var buf bytes.Buffer
		o.Out = &buf
		if rc := Run(o); rc != 0 {
			t.Errorf("apply=%v: rc = %d, want 0\n%s", apply, rc, buf.String())
		}
		out := buf.String()
		if strings.Contains(out, "FAILED") {
			t.Errorf("apply=%v: the report says FAILED:\n%s", apply, out)
		}
		if !strings.Contains(out, "Runtime: macos-user  Workspaces tracked: 1") ||
			!strings.Contains(out, "  • "+resolvePath(ws)) {
			t.Errorf("apply=%v: the tracked workspaces are not the live session's (%s):\n%s", apply, ws, out)
		}
		// Stopped containers, scratch volumes, relays, old images, tarballs, image roots, store
		// outputs, prefix roots and the store GC: nine sections, one line each.
		if n := strings.Count(out, strings.TrimSpace(stripMarkup(macosUserNotApplicable))); n != 9 {
			t.Errorf("apply=%v: %d not-applicable lines, want 9:\n%s", apply, n, out)
		}
		if !strings.Contains(out, "YOLO_RUNTIME=container yolo prune") {
			t.Errorf("apply=%v: the report does not name how to prune a container runtime:\n%s", apply, out)
		}
	}
}

// stripMarkup removes the [dim]…[/dim] tags the printer strips when not on a terminal.
func stripMarkup(s string) string {
	return strings.NewReplacer("[dim]", "", "[/dim]", "").Replace(s)
}

// TestPruneOnMacosUserSweepsStagingByLiveness: under macos-user the staging sweep's known set is
// the tracked container names and every staging dir a live or starting session's workspace key
// selects. A session that has ended protects nothing, and neither does an untracked name no
// session holds.
func TestPruneOnMacosUserSweepsStagingByLiveness(t *testing.T) {
	base := redirectSessions(t)
	o, gs := baseOpts(t)
	o.DetectRuntime = func() string { return "macos-user" }
	o.Exec = noMacosUserExec(t)
	o.Apply = true
	agents := filepath.Join(gs, "agents")

	plantSession(t, base, "yolo-a-live", "live", t.TempDir())
	plantSession(t, base, "yolo-b-ended", "gone", t.TempDir())
	plantSession(t, base, "yolo-c-starting", "nolock", t.TempDir())
	mustMkdir(t, filepath.Join(gs, "containers"))
	mustWrite(t, filepath.Join(gs, "containers", "yolo-e-tracked"), []byte("/ws\n"))
	live := plantStaging(t, agents, "yolo-a-live")
	ended := plantStaging(t, agents, "yolo-b-ended")
	starting := plantStaging(t, agents, "yolo-c-starting")
	orphan := plantStaging(t, agents, "yolo-d-orphan")
	tracked := plantStaging(t, agents, "yolo-e-tracked")

	var buf bytes.Buffer
	o.Out = &buf
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, buf.String())
	}
	for dir, why := range map[string]string{
		live:     "its session's lock is held",
		starting: "its session has no lock yet, which is a session starting up",
		tracked:  "a container jail's tracking file names it",
	} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("the sweep removed %s, but %s:\n%s", dir, why, buf.String())
		}
	}
	for _, dir := range []string{ended, orphan} {
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("the sweep kept %s, which nothing live holds:\n%s", dir, buf.String())
		}
	}
}

// TestAContainerRuntimesStagingSweepKeepsALiveMacosUserSession: a Mac that runs macos-user beside
// a container runtime runs that runtime's prune too, and the runtime lists no macos-user session,
// so without the session term the live session's staging would read as an orphan once it was an
// hour old. INFERRED from the code, not measured on a Mac: the human check is a macos-user session
// older than an hour beside a container launch.
func TestAContainerRuntimesStagingSweepKeepsALiveMacosUserSession(t *testing.T) {
	base := redirectSessions(t)
	o, gs := baseOpts(t)
	o.Apply = true // runtime podman, which answers and lists nothing
	agents := filepath.Join(gs, "agents")
	plantSession(t, base, "yolo-a-live", "live", t.TempDir())
	live := plantStaging(t, agents, "yolo-a-live")
	orphan := plantStaging(t, agents, "yolo-d-orphan")

	var buf bytes.Buffer
	o.Out = &buf
	Run(o)
	if _, err := os.Stat(live); err != nil {
		t.Errorf("podman's prune removed a live macos-user session's staging %s:\n%s", live, buf.String())
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Errorf("the orphan %s survived, so the sweep did not run and the keep above proves nothing:\n%s",
			orphan, buf.String())
	}
}

// TestSessionStagingNamesMatchesByWorkspaceKey: the protected names are the AGENTS_DIR entries
// whose container-name hash is a live or starting session's key, and nothing else.
func TestSessionStagingNamesMatchesByWorkspaceKey(t *testing.T) {
	base := redirectSessions(t)
	agents := t.TempDir()
	plantSession(t, base, "yolo-a-live", "live", "/a")
	plantSession(t, base, "yolo-b-ended", "gone", "/b")
	for _, n := range []string{"yolo-a-live", "yolo-b-ended", "yolo-c-other"} {
		mustMkdir(t, filepath.Join(agents, n))
	}
	names, ok := SessionStagingNames(agents, base)
	if !ok {
		t.Fatal("SessionStagingNames could not list")
	}
	if _, kept := names["yolo-a-live"]; !kept || len(names) != 1 {
		t.Errorf("names = %v, want only the live session's yolo-a-live", names)
	}
}
