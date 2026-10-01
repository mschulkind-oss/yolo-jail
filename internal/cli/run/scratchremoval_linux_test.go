//go:build linux

package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// The real starter does not wait, and what it starts is detached: its own session (so
// neither the terminal it is about to lose nor the shell's job control reaches it) and
// stdio on /dev/null (so a pipe on the launcher's stdout is not held open by it — the
// linger by another route).
func TestStartDetachedDoesNotWaitAndDetaches(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "facts")
	script := `sleep 2; s=$(cut -d' ' -f6 /proc/$$/stat); a=$(readlink /proc/$$/fd/0); b=$(readlink /proc/$$/fd/1); c=$(readlink /proc/$$/fd/2); ` +
		`printf '%s\n' "$s" "$a" "$b" "$c" > ` + shquote.Quote(out+".tmp") + ` && mv ` +
		shquote.Quote(out+".tmp") + ` ` + shquote.Quote(out)
	start := time.Now()
	if err := startDetached([]string{"sh", "-c", script}, nil); err != nil {
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
	if err := startDetached([]string{exe, "internal", ScratchRemoverVerb}, nil); err != errTestBinarySelfExec {
		t.Fatalf("err = %v", err)
	}
}

// THE IN-FLIGHT LOCK OUTLIVES THE LAUNCHER'S DESCRIPTOR AND DIES WITH THE CHILD. The spawn
// takes the lock, hands it to the child as fd 3 and closes its own copy — so the moment
// the launcher returns, a waiter already sees the remover, with no window before the child
// has run far enough to lock anything itself; and the wait ends when the child exits.
// The child is `sh`, standing in for the remover: holding the lock is keeping fd 3 open.
func TestWaitForScratchRemoversWaitsForTheSpawnedChild(t *testing.T) {
	ws := t.TempDir()
	o := &Options{Workspace: ws}
	o.StartDetached = func(argv []string, inherit *os.File) error {
		return startDetached([]string{"sh", "-c", "sleep 1.5"}, inherit)
	}
	start := time.Now()
	if err := o.spawnScratchRemover("podman", 0, []string{"v"}); err != nil {
		t.Fatal(err)
	}
	if err := WaitForScratchRemovers(ws, 100*time.Millisecond); err == nil {
		t.Fatal("the wait returned while the child still held the lock: the launcher's " +
			"closed copy was the only holder")
	}
	if err := WaitForScratchRemovers(ws, 20*time.Second); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < 1400*time.Millisecond {
		t.Errorf("the wait returned after %s, before the 1.5s child could have exited", took)
	}
}
