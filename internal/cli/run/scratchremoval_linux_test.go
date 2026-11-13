//go:build linux

package run

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// gateFIFO makes a named pipe a child blocks on (`read x < <path>`) until release writes a
// line to it. A child gated this way runs exactly as long as the test says, so these tests
// wait on that event instead of on a child's sleep (docs/plans/test-suite-speed.md). A gate
// never released is opened and closed at cleanup, which ends the child's read with EOF.
func gateFIFO(t *testing.T) (path string, release func()) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "gate")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	released := false
	release = func() {
		t.Helper()
		released = true
		done := make(chan error, 1)
		go func() {
			// Opening for write blocks until the child has opened its end for reading.
			f, err := os.OpenFile(path, os.O_WRONLY, 0)
			if err == nil {
				_, err = f.WriteString("go\n")
				_ = f.Close()
			}
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(testsupport.ReadinessBudget(t)):
			t.Fatal("the child never opened its gate")
		}
	}
	t.Cleanup(func() {
		if released {
			return
		}
		// Non-blocking: with no reader the open fails, and there is nobody to free.
		if f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
	})
	return path, release
}

// readFIFO reads everything a child writes to the named pipe at path, guarded by the
// readiness budget so a child that never writes is a red test, not a hang.
func readFIFO(t *testing.T, path string) []byte {
	t.Helper()
	type res struct {
		b   []byte
		err error
	}
	done := make(chan res, 1)
	go func() {
		b, err := os.ReadFile(path)
		done <- res{b, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.b
	case <-time.After(testsupport.ReadinessBudget(t)):
		// Unblock the reader goroutine before failing.
		if f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
		t.Fatal("the detached child never wrote its facts")
		return nil
	}
}

// The real starter does not wait, and what it starts is detached: its own session (so
// neither the terminal it is about to lose nor the shell's job control reaches it) and
// stdio on /dev/null (so a pipe on the launcher's stdout is not held open by it — the
// linger by another route).
//
// The child blocks on a gate the test releases only after startDetached has returned, so a
// starter that waited on its child would never return: the guard turns that into a red test.
func TestStartDetachedDoesNotWaitAndDetaches(t *testing.T) {
	gate, release := gateFIFO(t)
	facts := filepath.Join(t.TempDir(), "facts")
	if err := syscall.Mkfifo(facts, 0o600); err != nil {
		t.Fatal(err)
	}
	script := `read x < ` + shquote.Quote(gate) + `; s=$(cut -d' ' -f6 /proc/$$/stat); a=$(readlink /proc/$$/fd/0); b=$(readlink /proc/$$/fd/1); c=$(readlink /proc/$$/fd/2); ` +
		`printf '%s\n' "$s" "$a" "$b" "$c" > ` + shquote.Quote(facts)
	started := make(chan error, 1)
	go func() { started <- startDetached([]string{"sh", "-c", script}, nil) }()
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(testsupport.ReadinessBudget(t)):
		t.Fatal("startDetached did not return while its child was blocked: it waited on the child")
	}
	release()
	b := readFIFO(t, facts)
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
// It holds it until the test releases its gate, so the first wait is certain to find the
// lock held and the second certain to find it freed only by the child's exit.
func TestWaitForScratchRemoversWaitsForTheSpawnedChild(t *testing.T) {
	ws := t.TempDir()
	gate, release := gateFIFO(t)
	o := &Options{Workspace: ws, helperPin: testPin()}
	o.StartDetached = func(argv []string, inherit *os.File) error {
		return startDetached([]string{"sh", "-c", "read x < " + shquote.Quote(gate)}, inherit)
	}
	if err := o.spawnScratchRemover("podman", 0, []string{"v"}); err != nil {
		t.Fatal(err)
	}
	if err := WaitForScratchRemovers(ws, 100*time.Millisecond); err == nil {
		t.Fatal("the wait returned while the child still held the lock: the launcher's " +
			"closed copy was the only holder")
	}
	release()
	if err := WaitForScratchRemovers(ws, testsupport.ReadinessBudget(t)); err != nil {
		t.Fatal(err)
	}
}
