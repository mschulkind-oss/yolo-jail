package runcmd

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
	// Put a fake `socat` on PATH that just creates the listen socket file so the
	// condition-poll succeeds fast, then sleeps.
	bin := t.TempDir()
	fakeSocat := filepath.Join(bin, "socat")
	// The socat argv is: socat UNIX-LISTEN:<sock>,fork,mode=777 TCP:...
	// Extract the path between "UNIX-LISTEN:" and "," and touch it.
	script := `#!/bin/sh
arg="$1"
p="${arg#UNIX-LISTEN:}"
p="${p%%,*}"
: > "$p"
sleep 30
`
	if err := os.WriteFile(fakeSocat, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

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
