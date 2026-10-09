package journald

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// AN ABANDONED `yolo-journalctl -f` STOPS ITS `journalctl`. A followed unit that logs
// nothing gives the raw pump nothing to write, so a failed write cannot be what notices
// the client left — the same reason the macos-log filter watches its connection
// (TestAnAbandonedUserScopeStreamTerminatesItsLog). The raw pump used to rely on a write
// alone, which left a quiet unit's `journalctl -f` running on the host for good.
func TestAnAbandonedFollowTerminatesItsJournalctl(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	script := "#!/bin/sh\necho $$ > " + pidFile + "\nexec sleep 300\n"
	if err := os.WriteFile(filepath.Join(dir, "journalctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handleConn(server, ModeFull)
	}()
	// net.Pipe is unbuffered, so the header must be written while handleConn reads it;
	// the daemon then writes nothing until the child does, and this fake prints nothing.
	if _, err := client.Write([]byte(`{"args":["-f"]}` + "\n")); err != nil {
		t.Fatal(err)
	}
	var pid int
	for deadline := time.Now().Add(10 * time.Second); pid == 0 && time.Now().Before(deadline); {
		if b, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("the fake journalctl never started")
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	// Hang up. Closing the pipe also fails the exit-frame write that follows, so the
	// handler cannot block on it.
	_ = client.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the bridge kept serving a `journalctl -f` whose client had gone")
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Errorf("the abandoned follow's journalctl (pid %d) is still running", pid)
	}
}
