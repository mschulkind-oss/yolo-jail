//go:build linux

package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The real starter does not wait, and what it starts is detached: its own session (so
// neither the terminal it is about to lose nor the shell's job control reaches it) and
// stdio on /dev/null (so a pipe on the launcher's stdout is not held open by it — the
// linger by another route).
func TestStartDetachedDoesNotWaitAndDetaches(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "facts")
	script := `sleep 2; s=$(cut -d' ' -f6 /proc/$$/stat); a=$(readlink /proc/$$/fd/0); b=$(readlink /proc/$$/fd/1); c=$(readlink /proc/$$/fd/2); ` +
		`printf '%s\n' "$s" "$a" "$b" "$c" > ` + out + `.tmp && mv ` + out + `.tmp ` + out
	start := time.Now()
	if err := startDetached([]string{"sh", "-c", script}); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("startDetached took %s; it waited on a child that sleeps 2s", took)
	}
	deadline := time.Now().Add(20 * time.Second)
	var b []byte
	for {
		var err error
		if b, err = os.ReadFile(out); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the detached child never ran")
		}
		time.Sleep(50 * time.Millisecond)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 4 {
		t.Fatalf("facts = %q", b)
	}
	self, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatal(err)
	}
	// Field 6 is the session id; the comm field (2) has no spaces for a Go test binary.
	mySID := strings.Fields(string(self))[5]
	if lines[0] == mySID {
		t.Errorf("the child shares the launcher's session %s; it must lead its own", mySID)
	}
	for i, fd := range lines[1:] {
		if fd != "/dev/null" {
			t.Errorf("child fd %d = %q, want /dev/null", i, fd)
		}
	}
}

// A test binary never self-execs as the remover.
func TestStartDetachedRefusesToSelfExecATestBinary(t *testing.T) {
	exe, _ := os.Executable()
	if err := startDetached([]string{exe, "internal", ScratchRemoverVerb}); err != errTestBinarySelfExec {
		t.Fatalf("err = %v", err)
	}
}
