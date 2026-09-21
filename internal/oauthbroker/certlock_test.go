package oauthbroker

import (
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// These tests exist because EnsureCAAndLeaf took NO lock, and the broker
// singleton hid it: while exactly one broker process could ever reach the mint,
// the spawn flock in internal/broker was acting as a cert mutex for a caller that
// never asked it to. The hand-run `--init-ca` path never even had that, and the
// no-singleton ruling removes it from the daemon path too.
//
// WHY TWO GOROUTINES ARE A HONEST STAND-IN FOR TWO PROCESSES here: flock(2)
// associates the lock with the OPEN FILE DESCRIPTION, not the process, so two
// os.OpenFile calls in one process exclude each other exactly as two processes
// would. That is also why these tests reject a "modernization" to POSIX
// fcntl-style locks, which are per-process and would let the second caller
// straight through.

// mintStageGrace is how long the staged winner holds its half-written trio open
// for a loser to interleave with, and it is ONE-SIDED on purpose: it is only ever
// reached when the lock WORKS (the loser is blocked in flock and never signals),
// so making it longer only slows a passing run, while making it too short risks a
// false PASS. It is three atomic file writes against half a second.
const mintStageGrace = 500 * time.Millisecond

// foreignHolderGrace is how long we watch a blocked EnsureCAAndLeaf do nothing.
// Same polarity: an unlocked mint signals in microseconds, so the failure is
// detected immediately and only the passing path pays the wait.
const foreignHolderGrace = 150 * time.Millisecond

// bail bounds every wait whose failure mode would otherwise be a package-wide
// test timeout with no message.
const bail = 10 * time.Second

// stageMint swaps the mint step for the duration of one test.
func stageMint(t *testing.T, fn func(string) error) {
	t.Helper()
	orig := mintCAAndLeafFn
	mintCAAndLeafFn = fn
	t.Cleanup(func() { mintCAAndLeafFn = orig })
}

// stagedTrio writes the three files a jail mounts, each tagged with its writer,
// through the production writeFileAtomic. pause runs between the CA and the leaf:
// the window in which a mixed trio is formed.
func stagedTrio(dir, tag string, pause func()) error {
	if err := writeFileAtomic(caCrt(dir), []byte(tag), 0o644); err != nil {
		return err
	}
	pause()
	if err := writeFileAtomic(serverCrt(dir), []byte(tag), 0o644); err != nil {
		return err
	}
	return writeFileAtomic(serverKey(dir), []byte(tag), 0o600)
}

// trioTags reads back which writer each of the three files came from.
func trioTags(t *testing.T, dir string) (string, string, string) {
	t.Helper()
	read := func(path string) string {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		return string(raw)
	}
	return read(caCrt(dir)), read(serverCrt(dir)), read(serverKey(dir))
}

// TestConcurrentEnsureCAAndLeafNeverLeavesAMixedTrio is the OUTCOME test: it
// asserts what a jail would suffer, not that a particular lock call exists.
//
// It is deterministic in the failing direction. The loser does not start until
// the winner has written its ca.crt, so with no mutual exclusion the interleave
// is forced rather than raced: ca.crt ends up the loser's while server.crt and
// server.key end up the winner's, and the assertion fires. Delete withCertLock
// from EnsureCAAndLeaf and this test fails on every run.
//
// The second assertion is the in-lock currency re-check, which is the half that
// makes the lock worth taking rather than merely serializing two full mints: the
// loser must ADOPT the winner's trio, so the mint runs once.
func TestConcurrentEnsureCAAndLeafNeverLeavesAMixedTrio(t *testing.T) {
	dir := brokerStateDir(t)

	caWritten := make(chan struct{}) // the winner has written ITS ca.crt
	loserDone := make(chan struct{}) // the loser has written its whole trio
	var mints atomic.Int32

	stageMint(t, func(d string) error {
		if mints.Add(1) == 1 {
			return stagedTrio(d, "winner", func() {
				close(caWritten)
				select {
				case <-loserDone:
				case <-time.After(mintStageGrace):
				}
			})
		}
		defer close(loserDone)
		return stagedTrio(d, "loser", func() {})
	})

	errs := make(chan error, 2)
	go func() { errs <- EnsureCAAndLeaf(false) }()
	select {
	case <-caWritten:
	case err := <-errs:
		t.Fatalf("the first EnsureCAAndLeaf returned (%v) without reaching the mint", err)
	case <-time.After(bail):
		t.Fatal("the first EnsureCAAndLeaf never reached the mint")
	}
	go func() { errs <- EnsureCAAndLeaf(false) }()

	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatalf("EnsureCAAndLeaf: %v", err)
			}
		case <-time.After(bail):
			t.Fatal("EnsureCAAndLeaf never returned; the cert lock is not being released")
		}
	}

	ca, crt, key := trioTags(t, dir)
	if ca != crt || crt != key {
		t.Errorf("two concurrent EnsureCAAndLeaf calls left a MIXED trio: "+
			"ca.crt from %q, server.crt from %q, server.key from %q.\n"+
			"A launch binds all three by inode, so a jail mounts the mixture whole and "+
			"its terminator serves a leaf that chains to nothing. The mint must run under "+
			"withCertLock.", ca, crt, key)
	}
	if got := mints.Load(); got != 1 {
		t.Errorf("the mint ran %d times, want 1: the loser of the race must re-check "+
			"currency INSIDE the lock and adopt the winner's trio, not mint a second one "+
			"over it", got)
	}
}

// TestEnsureCAAndLeafWaitsForAForeignHolderOfTheCertLock pins the RENDEZVOUS, not
// just the exclusion: the thing a second broker PROCESS contends for is the flock
// on certLockPath(BrokerDir()), and a holder that never goes through
// EnsureCAAndLeaf must still keep it out.
//
// That is what makes the lock's path part of the contract. A future mint that
// locked something else — its own fd, a path derived from the socket name — would
// pass the mixed-trio test above (two goroutines through one code path agree on
// any path) and fail here.
func TestEnsureCAAndLeafWaitsForAForeignHolderOfTheCertLock(t *testing.T) {
	dir := brokerStateDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	held, err := os.OpenFile(certLockPath(dir), os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatalf("flocking the cert lock: %v", err)
	}

	entered := make(chan struct{}, 1)
	stageMint(t, func(d string) error {
		entered <- struct{}{}
		return mintCAAndLeaf(d)
	})

	done := make(chan error, 1)
	go func() { done <- EnsureCAAndLeaf(false) }()

	select {
	case <-entered:
		t.Fatal("EnsureCAAndLeaf minted while another holder had the cert flock: the mint " +
			"is not serialized against a second broker process")
	case err := <-done:
		t.Fatalf("EnsureCAAndLeaf returned (%v) while another holder had the cert flock", err)
	case <-time.After(foreignHolderGrace):
	}

	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatalf("releasing the cert lock: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(bail):
		t.Fatal("EnsureCAAndLeaf never minted after the cert lock was released")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("EnsureCAAndLeaf after the lock was released: %v", err)
		}
	case <-time.After(bail):
		t.Fatal("EnsureCAAndLeaf never returned after the cert lock was released")
	}
	if !caAndLeafAreCurrent(dir) {
		t.Error("the mint that waited for the lock produced no usable trio")
	}
}

// TestEnsureCAAndLeafRefusesToMintWhenItCannotTakeTheLock pins the STANCE the
// package already takes in withRefreshLock: an unavailable lock is a hard error,
// never a licence to proceed. A mint that shrugged and carried on would be the
// unlocked mint this whole file exists to rule out, reported as success.
//
// A directory at the lock path makes os.OpenFile(O_WRONLY) fail with EISDIR,
// which no amount of retrying or racing can turn into a success.
func TestEnsureCAAndLeafRefusesToMintWhenItCannotTakeTheLock(t *testing.T) {
	dir := brokerStateDir(t)
	if err := os.MkdirAll(certLockPath(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	stageMint(t, func(string) error {
		t.Error("EnsureCAAndLeaf minted although it could not take the cert lock")
		return nil
	})

	if err := EnsureCAAndLeaf(false); err == nil {
		t.Fatal("EnsureCAAndLeaf reported success though the cert lock was untakeable; " +
			"the caller then believes a trio exists that was never written")
	}
	if isFile(caCrt(dir)) {
		t.Error("a CA was written without the lock")
	}
}
