package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestNoArgsPrintsUsage(t *testing.T) {
	rc := run([]string{})
	if rc != 2 {
		t.Errorf("run([]) = %d, want 2", rc)
	}
}

func TestHelpFlag(t *testing.T) {
	rc := run([]string{"--help"})
	if rc != 0 {
		t.Errorf("run([--help]) = %d, want 0", rc)
	}
}

func TestUnknownSubcommand(t *testing.T) {
	rc := run([]string{"unknown-cmd"})
	if rc != 2 {
		t.Errorf("run([unknown-cmd]) = %d, want 2", rc)
	}
}

func TestNoEndpointMsg(t *testing.T) {
	var buf bytes.Buffer
	noEndpointMsg(&buf)
	msg := buf.String()
	if !strings.Contains(msg, "serial") || !strings.Contains(msg, "loopholes") {
		t.Errorf("noEndpointMsg output unexpected: %s", msg)
	}
}

// TestOpenPty: openPty gives a PTY on the two platforms this client runs on, a container jail
// (pty_linux.go, /dev/pts/N) and the macos-user guest (pty_darwin.go, /dev/ttysNNN), and refuses
// on every other (pty_other.go). pty_darwin_test.go also carries bytes across the darwin pair.
func TestOpenPty(t *testing.T) {
	slavePrefix := map[string]string{"linux": "/dev/pts/", "darwin": "/dev/ttys"}
	master, slavePath, err := openPty()
	want, supported := slavePrefix[runtime.GOOS]
	if !supported {
		if err == nil {
			master.Close()
			t.Errorf("openPty on %s succeeded, want the unsupported-platform error", runtime.GOOS)
		}
		return
	}
	if err != nil {
		t.Fatalf("openPty failed on %s: %v", runtime.GOOS, err)
	}
	defer master.Close()
	if !strings.HasPrefix(slavePath, want) {
		t.Errorf("slavePath = %q, want %s...", slavePath, want)
	}
}
