package testsupport

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/heldchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestIsolateGivesAPrivateDirAndRestoresIt: the seam moves off the machine-wide /tmp for
// the duration, the children inherit it, and the cleanup puts everything back.
func TestIsolateGivesAPrivateDirAndRestoresIt(t *testing.T) {
	t.Setenv(inheritEnv, "")
	release := IsolateHostSingletons()
	dir := paths.HostSingletonDir
	if dir == paths.DefaultHostSingletonDir || !strings.HasPrefix(dir, "/tmp/"+dirPrefix) {
		t.Fatalf("HostSingletonDir = %q, want a private /tmp/%s* dir", dir, dirPrefix)
	}
	if got := os.Getenv(inheritEnv); got != dir {
		t.Errorf("%s = %q, want %q so helper children inherit it", inheritEnv, got, dir)
	}
	if len(paths.HostSingletonSocket("x")) >= 104 {
		t.Errorf("socket path %q is past darwin's 104-byte sun_path", paths.HostSingletonSocket("x"))
	}
	release()
	if paths.HostSingletonDir != paths.DefaultHostSingletonDir {
		t.Errorf("after release HostSingletonDir = %q, want the default", paths.HostSingletonDir)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the private dir %s survived its release: %v", dir, err)
	}
}

// TestIsolateReusesAnInheritedDir: a helper child reuses its parent's directory and owns
// no cleanup, so a child that exits from inside its test leaks nothing.
func TestIsolateReusesAnInheritedDir(t *testing.T) {
	parent := t.TempDir()
	t.Setenv(inheritEnv, parent)
	prev := paths.HostSingletonDir
	t.Cleanup(func() { paths.HostSingletonDir = prev })
	release := IsolateHostSingletons()
	if paths.HostSingletonDir != parent {
		t.Fatalf("HostSingletonDir = %q, want the inherited %q", paths.HostSingletonDir, parent)
	}
	release()
	if _, err := os.Stat(parent); err != nil {
		t.Errorf("a child's release removed its parent's dir: %v", err)
	}
}

// TestReleaseLeavesEveryOtherDirAlone: another run's directory under /tmp is never
// touched, however abandoned it looks — no owner record, older than any age threshold,
// and a PID file naming a live process whose argv mentions it. The machine is shared,
// so only what this process created is this process's to remove or stop.
func TestReleaseLeavesEveryOtherDirAlone(t *testing.T) {
	t.Setenv(inheritEnv, "")
	other, err := os.MkdirTemp("/tmp", dirPrefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(other) })
	daemon, daemonDone := startUnheld(t, other)
	writeFile(t, filepath.Join(other, "yolo-x.pid"), strconv.Itoa(daemon.Process.Pid))
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(other, old, old); err != nil {
		t.Fatal(err)
	}

	release := IsolateHostSingletons()
	release()

	if _, err := os.Stat(other); err != nil {
		t.Errorf("another run's dir %s was removed: %v", other, err)
	}
	if !stillRunning(daemonDone) {
		t.Errorf("a process this test process does not hold was stopped")
	}
}

// TestReleaseStopsOnlyTheDaemonsItHolds: the release stops a daemon this process spawned
// and handed to heldchildren, and leaves a process a PID file in its own directory names,
// because a PID file is not a handle.
func TestReleaseStopsOnlyTheDaemonsItHolds(t *testing.T) {
	t.Setenv(inheritEnv, "")
	release := IsolateHostSingletons()
	dir := paths.HostSingletonDir

	held := startHeld(t)
	named, namedDone := startUnheld(t, dir)
	writeFile(t, filepath.Join(dir, "yolo-x.pid"), strconv.Itoa(named.Process.Pid))

	release()

	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Errorf("the daemon this process holds is still running after the release")
	}
	if !stillRunning(namedDone) {
		t.Errorf("the release stopped a PID it read from a file")
	}
}

// startUnheld starts a long-running child whose argv mentions dir, reaped as soon as it
// exits, and does NOT hand it to heldchildren. Reaping matters: an unreaped child that was
// signalled is a zombie, which kill(pid, 0) still finds, so only the exit channel can tell.
func startUnheld(t *testing.T, dir string) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command("sh", "-c", "while :; do sleep 1; done; : "+dir)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	return cmd, done
}

// stillRunning reports whether done stays open for a moment: long enough for a SIGTERM the
// release sent to have ended the child.
func stillRunning(done <-chan struct{}) bool {
	select {
	case <-done:
		return false
	case <-time.After(300 * time.Millisecond):
		return true
	}
}

// startHeld starts a long-running child, reaps it, and hands it to heldchildren, as
// broker's realSpawn does. The returned channel closes when it exits.
func startHeld(t *testing.T) <-chan struct{} {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	heldchildren.Hold(cmd.Process, done)
	return done
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
