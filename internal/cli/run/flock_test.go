package run

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// collector is a lockNotices sink: every message, on the seam it arrived on.
type collector struct {
	warns   chan string
	waits   chan string
	notices lockNotices
}

func newCollector() *collector {
	c := &collector{warns: make(chan string, 4), waits: make(chan string, 4)}
	c.notices = lockNotices{
		warn:    func(m string) { c.warns <- m },
		waiting: func(m string) { c.waits <- m },
	}
	return c
}

// TestWorkspaceLockUncontendedIsSilent pins the POLARITY of the waiting notice:
// the common case — nobody else holds the lock — must say nothing at all.
//
// Without this, "always print the notice" would satisfy the contention test
// below while spamming every single launch.
func TestWorkspaceLockUncontendedIsSilent(t *testing.T) {
	dir := t.TempDir()
	c := newCollector()
	l, err := acquireWorkspaceLock(filepath.Join(dir, "yolo-quiet.lock"), dir, c.notices)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	select {
	case msg := <-c.waits:
		t.Errorf("uncontended acquire emitted a waiting notice: %q", msg)
	case msg := <-c.warns:
		t.Errorf("uncontended acquire emitted a warning: %q", msg)
	default:
	}
}

// TestWorkspaceLockWaitingNotice is the regression test for the silent hang: a
// second yolo launching into a workspace whose lock is already held must SAY so
// before it blocks, and must still get the lock once the holder releases it.
//
// The ordering assertions are the substance. It is not enough that the notice
// appears eventually — it has to appear WHILE the second acquire is still
// blocked, because a notice printed after the wait ends explains nothing. So the
// test reads the notice off the seam first, then proves the acquire has not
// returned yet, and only then releases the holder.
func TestWorkspaceLockWaitingNotice(t *testing.T) {
	dir := t.TempDir()
	workspace := filepath.Join(dir, "myproject")
	lockPath := filepath.Join(dir, "yolo-abc123.lock")

	// flock is per open-file-description, so a second acquire from this same
	// process contends exactly as a second yolo process would.
	held, err := acquireWorkspaceLock(lockPath, workspace, lockNotices{})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	c := newCollector()
	acquired := make(chan *workspaceLock, 1)
	failed := make(chan error, 1)
	go func() {
		l, err := acquireWorkspaceLock(lockPath, workspace, c.notices)
		if err != nil {
			failed <- err
			return
		}
		acquired <- l
	}()

	select {
	case err := <-failed:
		t.Fatalf("contended acquire returned an error: %v", err)
	case msg := <-c.waits:
		// The wording carries the two facts a waiting user needs: WHICH workspace
		// is ahead of them (they may have several jails) and which lock file to
		// look at.
		for _, want := range []string{"Waiting for concurrent jail launch", workspace, "yolo-abc123.lock"} {
			if !strings.Contains(msg, want) {
				t.Errorf("waiting notice %q does not mention %q", msg, want)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no waiting notice while the lock was held (the silent-hang defect)")
	}

	// Contention is not a warning: the race guard is working, not degraded.
	select {
	case msg := <-c.warns:
		t.Errorf("contention emitted a warning: %q", msg)
	default:
	}

	// ...and the notice was not a substitute for waiting.
	select {
	case <-acquired:
		t.Fatal("second acquire returned while the lock was still held")
	case <-time.After(100 * time.Millisecond):
	}

	held.Close()
	select {
	case l := <-acquired:
		l.Close()
	case err := <-failed:
		t.Fatalf("acquire after release returned an error: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("second acquire did not complete after the lock was released")
	}
}

// TestWorkspaceLockFlockErrorDegrades pins the deliberate NON-failure: when
// flock(2) errors for a reason that is not contention, the launch warns and
// proceeds unguarded rather than dying. A workspace lock is a courtesy against a
// self-inflicted race, not a safety property.
func TestWorkspaceLockFlockErrorDegrades(t *testing.T) {
	dir := t.TempDir()
	orig := flockSyscall
	t.Cleanup(func() { flockSyscall = orig })
	calls := 0
	flockSyscall = func(fd, how int) error {
		calls++
		return syscall.EBADF
	}

	c := newCollector()
	l, err := acquireWorkspaceLock(filepath.Join(dir, "yolo-broken.lock"), dir, c.notices)
	if err != nil {
		t.Fatalf("a flock error must not fail the launch, got %v", err)
	}
	if l == nil {
		t.Fatal("a flock error must still yield an open lock handle")
	}
	l.Close()
	// The NB probe is informational: an unexpected errno from it must not skip the
	// blocking acquire, which is the one that would actually have taken the lock.
	if calls != 2 {
		t.Errorf("flock called %d times, want 2 (non-blocking probe, then blocking acquire)", calls)
	}
	select {
	case msg := <-c.warns:
		if !strings.Contains(msg, "race protection disabled") {
			t.Errorf("warning %q does not say race protection is disabled", msg)
		}
	default:
		t.Error("a flock error emitted no warning")
	}
	select {
	case msg := <-c.waits:
		t.Errorf("a flock error must not be reported as waiting: %q", msg)
	default:
	}
}
