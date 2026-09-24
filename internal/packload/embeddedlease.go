package packload

// embeddedlease.go is the LIVENESS half of the embedded-pack tree: a flock on a regular file
// inside the tree, held SHARED by every process reading that tree and probed EXCLUSIVE by a
// reaper deciding whether it may delete it.
//
// A lock rather than an age cutoff, because the readers are unbounded in lifetime: a `yolo`
// launcher lives for the whole jail session and may read an embedded Pack.Root in its
// Ctrl-C teardown, and a host daemon lives for days. Nothing but asking the kernel "does
// anyone still hold this" can tell a stale tree from a live one, and the kernel drops the
// lock on EVERY exit — SIGKILL and OOM included — which no exit-path cleanup can match.
//
// The lock is on a regular FILE (`.lease`), not on the directory, so the same call works on
// Linux and darwin without depending on directory-fd flock semantics.

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

const (
	// EmbeddedLeaseName is the file inside a tree that carries its lease.
	EmbeddedLeaseName = ".lease"

	// EmbeddedFallbackPrefix names a per-process fallback tree in TMPDIR — taken only when
	// the cache location is unavailable. Each carries its own lease, which is what lets a
	// later process (or `yolo prune`) delete one whose owner died without cleaning up.
	EmbeddedFallbackPrefix = "yolo-embedded-lease-"

	// LegacyEmbeddedPrefix is the TMPDIR prefix every earlier build used for its
	// per-process tree, one directory per invocation of every command. Those directories
	// carry no lease; this package never touches them, and only `yolo prune` reaps them,
	// by its own liveness rule.
	LegacyEmbeddedPrefix = "yolo-embedded-"
)

// LeaseState is ProbeLease's answer. It is TRI-STATE ON PURPOSE, with a fourth value for a
// tree that has no lease file at all: "I could not ask" (LeaseUnknown) must never be
// mistaken for "nobody holds it" (LeaseFree), because the reaper acting on the latter
// deletes a tree a running process may still read.
type LeaseState int

const (
	// LeaseUnknown: the lease exists but could not be probed (open or flock failed for a
	// reason other than contention — a filesystem without flock, a permission). KEEP.
	LeaseUnknown LeaseState = iota
	// LeaseFree: the exclusive lock was taken, so no process holds the tree. The caller
	// holds the lock until it calls unlock.
	LeaseFree
	// LeaseHeld: another process holds the lease. KEEP.
	LeaseHeld
	// LeaseAbsent: the directory has no lease file. What that means depends on the entry —
	// a finished tree always has one, an in-flight populate may not yet.
	LeaseAbsent
)

func (s LeaseState) String() string {
	switch s {
	case LeaseFree:
		return "free"
	case LeaseHeld:
		return "held"
	case LeaseAbsent:
		return "absent"
	default:
		return "unknown"
	}
}

// ProbeLease takes a NON-BLOCKING EXCLUSIVE lock on dir/.lease.
//
// On LeaseFree the lock is HELD when this returns, and unlock releases it: a reaper renames
// the tree out of its final name while holding it, so no reader can adopt the tree between
// the probe and the delete (an adopter blocks on its shared lock, then sees the inode moved
// and retries). Every other state returns a no-op unlock. The error is the reason behind a
// LeaseUnknown, for the reaper's note.
func ProbeLease(dir string) (LeaseState, func(), error) {
	noop := func() {}
	f, err := os.Open(filepath.Join(dir, EmbeddedLeaseName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return LeaseAbsent, noop, nil
		}
		return LeaseUnknown, noop, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return LeaseHeld, noop, nil
		}
		return LeaseUnknown, noop, err
	}
	return LeaseFree, func() { _ = f.Close() }, nil
}

// holdLease opens path read-only and takes a SHARED lock on it, blocking — a reaper holds
// its exclusive lock only across a rename. A nil file with a nil error means the lock is
// unsupported here: the tree is still used, just without liveness evidence, so a reaper
// gets LeaseUnknown and keeps it.
//
// The fd is close-on-exec (os.Open sets O_CLOEXEC), so an exec'ing process drops its lease
// with no call of its own.
func holdLease(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if err := flockShared(f); err != nil {
		_ = f.Close()
		return nil, nil
	}
	return f, nil
}

// createLease creates path (which must not exist) mode 0444 and takes the shared lock on the
// new fd at once, so the tree is leased from the instant it has a lease file at all. A lock
// failure keeps the file and returns a nil *os.File, as holdLease does.
func createLease(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return nil, err
	}
	if err := flockShared(f); err != nil {
		_ = f.Close()
		return nil, nil
	}
	return f, nil
}

func flockShared(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}
