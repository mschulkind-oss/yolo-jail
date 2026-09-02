package run

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestStartHostPortForwardingEmpty(t *testing.T) {
	o := &Options{}
	fillDefaults(o)
	if got := o.startHostPortForwarding(nil, "c", t.TempDir()); got != nil {
		t.Errorf("expected nil for no forwards, got %v", got)
	}
}

func TestStartHostPortForwardingSpawnsSocat(t *testing.T) {
	// The fake socat lives in hostforwardgate_test.go now that a second test needs
	// it: two hand-written copies of one shell script is the drift this package's
	// shared-predicate comments are about, at the fixture level.
	fakeSocatOnPath(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	o := &Options{}
	fillDefaults(o)
	socketDir := filepath.Join(t.TempDir(), "yolo-fwd-test")
	procs := o.startHostPortForwarding([]any{8080, "9090:5432"}, "test", socketDir)
	t.Cleanup(func() { cleanupPortForwarding(procs, socketDir) })

	if len(procs) != 2 {
		t.Fatalf("expected 2 socat procs, got %d", len(procs))
	}
	// Both socket files should exist (the fake socat created them).
	for _, port := range []int{8080, 9090} {
		sock := filepath.Join(socketDir, "port-"+strconv.Itoa(port)+".sock")
		if _, err := os.Stat(sock); err != nil {
			t.Errorf("socket %s missing: %v", sock, err)
		}
	}
}

func TestCleanupPortForwardingRemovesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "yolo-fwd-x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cleanupPortForwarding(nil, dir)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("expected socket dir removed, err=%v", err)
	}
}
