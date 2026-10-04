package pidlock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Two opens of one path in one process are two open file descriptions, so they contend exactly as
// two processes would.
func TestNoWaitFailsOnAHeldLockAndWaitGetsItOnRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	first, err := Acquire(path, NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	if Holder(path) != os.Getpid() || !Held(path) {
		t.Errorf("holder %d held %v, want this pid and held", Holder(path), Held(path))
	}
	if _, err := Acquire(path, NoWait, nil); !errors.Is(err, ErrHeld) {
		t.Fatalf("a second NoWait acquire: %v, want ErrHeld", err)
	}
	waited := make(chan int, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		first.Release()
	}()
	second, err := Acquire(path, Mode{Wait: true, Bound: 5 * time.Second}, func(pid int) { waited <- pid })
	if err != nil {
		t.Fatalf("a bounded wait for a lock released in time: %v", err)
	}
	defer second.Release()
	if pid := <-waited; pid != os.Getpid() {
		t.Errorf("onWait was told pid %d, want the holder's %d", pid, os.Getpid())
	}
}

func TestABoundedWaitTimesOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	held, err := Acquire(path, NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	start := time.Now()
	if _, err := Acquire(path, Mode{Wait: true, Bound: 150 * time.Millisecond}, nil); !errors.Is(err, ErrTimedOut) {
		t.Fatalf("err = %v, want ErrTimedOut", err)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Error("the wait gave up before its bound")
	}
}

// A CANCELLED WAIT ends with ErrCanceled, bounded or not (a patched fork's advance, whose Ctrl-C
// ends the wait: docs/design/patched-forks.md PF-D25).
func TestACancelledWaitEnds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	first, err := Acquire(path, NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	for _, bound := range []time.Duration{0, time.Hour} {
		cancel := make(chan struct{})
		go func() { time.Sleep(100 * time.Millisecond); close(cancel) }()
		start := time.Now()
		if _, err := Acquire(path, Mode{Wait: true, Bound: bound, Cancel: cancel}, nil); !errors.Is(err, ErrCanceled) {
			t.Fatalf("bound %s: a cancelled wait returned %v, want ErrCanceled", bound, err)
		}
		if time.Since(start) > 10*time.Second {
			t.Errorf("bound %s: the cancelled wait took %s", bound, time.Since(start))
		}
	}
}
