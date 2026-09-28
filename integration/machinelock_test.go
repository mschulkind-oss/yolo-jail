package integration

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// machinelock_test.go is the suite's CROSS-RUN lock: what lets two `go test ./integration`
// processes on one machine run at once without one of them rewriting state the other is in
// the middle of reading.
//
// # Why a suite needs one
//
// Measured 2026-09-27: 6 of 16 full runs that overlapped another run failed, against 1 of 21
// that ran alone. Most of this package only ever shares what a machine is SUPPOSED to share —
// podman's image store, the nix store — and two launches reading those at once is the normal
// case the product is built for. A few tests do something no ordinary launch does: they take
// the machine-wide state over for their duration.
//
//   - TestImageCopyLockSerializesConcurrentLaunches removes every name in the jail image
//     repository, so that its own three launches are the only ones that need a copy. Any other
//     launch on the machine in that window needs one too, reaches the same image-copy lock, and
//     can be the one that copies — after which the test's launches all report a peer's delivery
//     and it fails for a copy it did not make.
//   - The broker tests (openaiauth_test.go, awsauth_test.go) own a host SINGLETON, whose socket,
//     PID file and adapter port are fixed per machine (paths.HostSingletonSocket, 127.0.0.1:1460
//     and :1461). Another run's launch adopts the test's forged daemon, or is the reason the
//     test finds one already alive.
//
// # The shape: a readers-writer lock, with a turnstile
//
// Every container test holds the lock SHARED for its whole duration (requireJail), and a test
// that takes machine state over holds it EXCLUSIVE instead (requireJailExclusive). Shared
// holders never wait for each other, so two runs of ordinary tests interleave exactly as they
// did before; an exclusive test waits for the other runs' in-flight tests to finish, runs
// alone, and then lets them continue.
//
// flock(2) has no writer preference, so a lone EX waiter can starve behind a stream of SH
// holders from several runs whose tests overlap end to end. The TURNSTILE closes that gap: a
// reader passes through it (takes it shared, takes the main lock shared, drops it) and a writer
// holds it exclusive for as long as it waits and runs, so no new reader gets in behind a writer.
//
// # Where it lives
//
// Beside the host singletons, in paths.HostSingletonDir, and NOT in the yolo state dir: what
// the lock protects is per-machine (one podman store, one set of singleton sockets), while the
// state dir is per-HOME, and two runs under two HOMEs share the podman store just the same.
// The uid is in the name so two users on one machine never fight over one file's mode.
//
// A killed run releases everything: an flock dies with its file descriptor.

// machineLockWait bounds how long a test waits for the lock. A holder is a whole TEST, which
// may run several commands each bounded by jailTimeout(), so the bound is a multiple of that
// rather than the per-command budget itself; a wait that reaches it is reported by name rather
// than left to hang the suite.
func machineLockWait() time.Duration { return 4 * jailTimeout() }

// machineLockPaths returns the main lock and its turnstile.
func machineLockPaths() (main, turnstile string) {
	base := filepath.Join(paths.HostSingletonDir, "yolo-integration-"+strconv.Itoa(os.Getuid()))
	return base + ".lock", base + ".turnstile"
}

// flockWait takes how (LOCK_SH or LOCK_EX) on path, polling non-blockingly until the deadline
// so that a wait is bounded and can be reported. It returns the open file holding the lock.
//
// Opened O_RDONLY: flock does not need a writable descriptor, and a lock file some earlier run
// created with a narrower mode is still lockable.
func flockWait(path string, how int, deadline time.Time) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("still held by another process after the wait")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// machineLock is one held acquisition of the cross-run lock.
type machineLock struct {
	main, turnstile *os.File // turnstile is nil for a shared hold
}

func (l *machineLock) release() {
	if l == nil {
		return
	}
	if l.main != nil {
		_ = l.main.Close()
		l.main = nil
	}
	if l.turnstile != nil {
		_ = l.turnstile.Close()
		l.turnstile = nil
	}
}

// acquireMachineLock takes the cross-run lock shared or exclusive. See the file header.
func acquireMachineLock(exclusive bool, wait time.Duration) (*machineLock, error) {
	mainPath, turnstilePath := machineLockPaths()
	deadline := time.Now().Add(wait)
	if exclusive {
		ts, err := flockWait(turnstilePath, syscall.LOCK_EX, deadline)
		if err != nil {
			return nil, fmt.Errorf("turnstile %s: %w", turnstilePath, err)
		}
		m, err := flockWait(mainPath, syscall.LOCK_EX, deadline)
		if err != nil {
			_ = ts.Close()
			return nil, fmt.Errorf("%s: %w", mainPath, err)
		}
		return &machineLock{main: m, turnstile: ts}, nil
	}
	ts, err := flockWait(turnstilePath, syscall.LOCK_SH, deadline)
	if err != nil {
		return nil, fmt.Errorf("turnstile %s: %w", turnstilePath, err)
	}
	defer ts.Close() // a reader only PASSES the turnstile
	m, err := flockWait(mainPath, syscall.LOCK_SH, deadline)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", mainPath, err)
	}
	return &machineLock{main: m}, nil
}

// heldMachineLock is the current test's hold. The package runs its tests serially (no
// t.Parallel anywhere), so one variable is the whole bookkeeping: requireJailExclusive needs
// it to drop requireJail's shared hold before it asks for the exclusive one — a second
// descriptor's EX would otherwise wait forever on this process's own SH.
var heldMachineLock *machineLock

// holdMachineLock takes the lock for the rest of t and releases it at t's cleanup.
//
// It is registered before anything the test creates, and cleanups run LIFO, so the release is
// the LAST cleanup: a test's containers are removed, and an exclusive test's machine state
// restored, before another run is let in.
func holdMachineLock(t *testing.T, exclusive bool, why string) {
	t.Helper()
	if heldMachineLock != nil {
		heldMachineLock.release()
		heldMachineLock = nil
	}
	start := time.Now()
	l, err := acquireMachineLock(exclusive, machineLockWait())
	if err != nil {
		mode := "shared"
		if exclusive {
			mode = "exclusive"
		}
		t.Fatalf("could not take the integration suite's cross-run lock (%s) within %s: %v — "+
			"another `go test ./integration` on this machine holds it; see machinelock_test.go",
			mode, machineLockWait(), err)
	}
	if waited := time.Since(start); waited > time.Second {
		if exclusive {
			t.Logf("waited %s for other integration runs' tests to finish, to run alone: %s",
				waited.Round(100*time.Millisecond), why)
		} else {
			t.Logf("waited %s for another integration run's exclusive test to finish",
				waited.Round(100*time.Millisecond))
		}
	}
	heldMachineLock = l
	t.Cleanup(func() {
		l.release()
		if heldMachineLock == l {
			heldMachineLock = nil
		}
	})
}

// requireJailExclusive is requireJail for a test that takes MACHINE-WIDE state over for its
// duration — the jail image repository's names, a host singleton, a fixed port — and must
// therefore run while no other integration run on the machine is mid-test. why is logged when
// the wait is noticeable, so a slow start says what it was for.
func requireJailExclusive(t *testing.T, why string) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping container integration test (-short)")
	}
	isolateHome(t, "{}")
	holdMachineLock(t, true, why)
}

// holdMachineLockForSetup takes the shared lock for TestMain's own launches (the image
// delivery and the warmup), which run before any test and have no *testing.T. A failure is
// DEGRADED rather than fatal, like the rest of that setup: the tests still run and still take
// the lock themselves.
func holdMachineLockForSetup() func() {
	l, err := acquireMachineLock(false, machineLockWait())
	if err != nil {
		degraded("could not take the integration suite's cross-run lock for the image delivery "+
			"and warmup: %v — continuing without it", err)
		return func() {}
	}
	return l.release
}

// logMachineLock names the lock in the suite log once, so two overlapping runs can be told
// apart from a hang.
func logMachineLock() {
	mainPath, _ := machineLockPaths()
	log.Printf("[integration] cross-run lock: %s (shared per test, exclusive for tests that "+
		"take machine state over — machinelock_test.go)", mainPath)
}

// probeMachineLock reports whether a NEW descriptor could take the main lock in mode how right
// now — the view another run has of this process's hold.
func probeMachineLock(t *testing.T, how int) bool {
	t.Helper()
	mainPath, _ := machineLockPaths()
	f, err := os.Open(mainPath)
	if err != nil {
		t.Fatalf("opening the cross-run lock %s: %v", mainPath, err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB); err != nil {
		return false
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return true
}

// TestRequireJailHoldsTheMachineLock pins the CALL SITES: requireJail holds the cross-run lock
// shared and requireJailExclusive holds it exclusive, as another run would see them. The -short
// test below proves the lock's semantics; only this one fails if either call is deleted. It runs
// in the container suite (requireJail skips under -short) and launches nothing.
func TestRequireJailHoldsTheMachineLock(t *testing.T) {
	t.Run("shared", func(t *testing.T) {
		requireJail(t)
		if probeMachineLock(t, syscall.LOCK_EX) {
			t.Error("another run could take the lock EXCLUSIVE during a requireJail test — " +
				"an exclusive test elsewhere would run under this one")
		}
		if !probeMachineLock(t, syscall.LOCK_SH) {
			t.Error("another run could NOT take the lock shared during a requireJail test — " +
				"two runs' ordinary tests would serialize")
		}
	})
	t.Run("exclusive", func(t *testing.T) {
		requireJailExclusive(t, "TestRequireJailHoldsTheMachineLock probes it")
		if probeMachineLock(t, syscall.LOCK_SH) {
			t.Error("another run could take the lock shared during a requireJailExclusive test")
		}
	})
}

// withMachineLockDir points the cross-run lock at a private directory for one test, so the
// unit tests below never contend with a real run on this machine.
func withMachineLockDir(t *testing.T) {
	t.Helper()
	saved := paths.HostSingletonDir
	paths.HostSingletonDir = t.TempDir()
	t.Cleanup(func() { paths.HostSingletonDir = saved })
}

// TestMachineLockSharesAndExcludes pins the readers-writer semantics every container test now
// runs under, under -short (no container): shared holders coexist, an exclusive holder
// excludes both kinds, and a WAITING exclusive holder already keeps new readers out — the
// turnstile, without which a writer starves behind two runs' overlapping readers.
func TestMachineLockSharesAndExcludes(t *testing.T) {
	withMachineLockDir(t)
	const brief = 150 * time.Millisecond

	a, err := acquireMachineLock(false, brief)
	if err != nil {
		t.Fatalf("first shared hold: %v", err)
	}
	b, err := acquireMachineLock(false, brief)
	if err != nil {
		t.Fatalf("a second shared hold must not wait for the first: %v", err)
	}
	if l, err := acquireMachineLock(true, brief); err == nil {
		l.release()
		t.Fatal("an exclusive hold was granted while two shared holds were live")
	}
	b.release()

	// A writer that arrives while a reader holds the lock waits; while it waits, a NEW reader
	// must not get in ahead of it.
	got := make(chan *machineLock, 1)
	go func() {
		l, err := acquireMachineLock(true, 10*time.Second)
		if err != nil {
			t.Errorf("the waiting exclusive hold was never granted: %v", err)
		}
		got <- l
	}()
	_, turnstile := machineLockPaths()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f, err := os.Open(turnstile)
		if err == nil {
			busy := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) != nil
			_ = f.Close()
			if busy {
				break // the writer holds the turnstile and is waiting on the main lock
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the exclusive waiter never took the turnstile")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if l, err := acquireMachineLock(false, brief); err == nil {
		l.release()
		t.Fatal("a new shared hold got in ahead of a waiting exclusive one — a writer can starve")
	}
	a.release()
	w := <-got
	if w == nil {
		t.FailNow()
	}
	if l, err := acquireMachineLock(false, brief); err == nil {
		l.release()
		t.Fatal("a shared hold was granted while an exclusive hold was live")
	}
	w.release()
	if l, err := acquireMachineLock(false, brief); err != nil {
		t.Fatalf("the lock is not free once the exclusive hold is released: %v", err)
	} else {
		l.release()
	}
}
