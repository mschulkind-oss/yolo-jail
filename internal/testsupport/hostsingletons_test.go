package testsupport

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

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

// TestSweepStopsAndRemovesAnAbandonedDir: a dir whose owner process is gone is removed,
// and a daemon still running in it (named by a PID file, its argv mentioning the dir) is
// stopped. A dir whose owner is alive is left alone.
func TestSweepStopsAndRemovesAnAbandonedDir(t *testing.T) {
	abandoned, err := os.MkdirTemp("/tmp", dirPrefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(abandoned) })
	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(abandoned, ownerFile), strconv.Itoa(dead.Process.Pid))
	daemon := exec.Command("sh", "-c", "while :; do sleep 1; done; : "+abandoned)
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemon.Process.Kill(); _, _ = daemon.Process.Wait() })
	writeFile(t, filepath.Join(abandoned, "yolo-x.pid"), strconv.Itoa(daemon.Process.Pid))

	live, err := os.MkdirTemp("/tmp", dirPrefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(live) })
	writeFile(t, filepath.Join(live, ownerFile), strconv.Itoa(os.Getpid()))

	sweepAbandoned()

	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Errorf("the abandoned dir %s survived the sweep: %v", abandoned, err)
	}
	done := make(chan struct{})
	go func() { _, _ = daemon.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Errorf("the daemon in the abandoned dir is still running")
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("the sweep removed a dir whose owner is alive: %v", err)
	}
}

// TestStopSkipsAPIDWhoseArgvIsSomethingElse: a stale PID file can name a PID the kernel
// has since reused; a process whose argv does not mention the dir is never signalled.
func TestStopSkipsAPIDWhoseArgvIsSomethingElse(t *testing.T) {
	dir := t.TempDir()
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _, _ = other.Process.Wait() })
	writeFile(t, filepath.Join(dir, "yolo-x.pid"), strconv.Itoa(other.Process.Pid))
	stopSingletonsIn(dir)
	if err := syscall.Kill(other.Process.Pid, 0); err != nil {
		t.Errorf("stopSingletonsIn signalled a process whose argv never mentioned %s", dir)
	}
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
