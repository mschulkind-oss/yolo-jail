package image

// copylock.go SERIALISES THE IMAGE COPY ACROSS EVERY LAUNCH ON ONE MACHINE.
//
// # The measurement that forced it
//
// MEASURED 2026-09-14: a reboot launched 11 jails at once and 5 of them copied
// ONE identical 3.45 GB image simultaneously, each spending ~4 minutes in the
// store write. Five times the bytes and five times the wall clock, for one
// image's worth of content that one of them could have delivered for all five.
//
// Layer-aware delivery (C9) is what makes that shape possible rather than what
// prevents it. `skopeo copy` asks containers-storage for each blob before
// sending it, and the store can only answer for a layer it has already
// COMMITTED — a copy in flight is invisible to every other copy. So the
// negotiation that turns a warm launch into 26 MB does nothing at all for N
// launches that start together against a store that does not yet hold the
// image. Nothing else serialises them, and the measurement is the proof rather
// than a survey of what might have: the run lock is per CONTAINER NAME
// (internal/cli/run/flock.go), the housekeeping lock is held only across an
// inspect-and-record (autoload.go), and whatever locking containers-storage
// does for itself, five full copies of one image demonstrably ran to completion
// side by side.
//
// # Why this is not the housekeeping lock
//
// They protect different things on different timescales and must not share a
// file. The housekeeping lock (internal/cli/run/housekeeping.go, OQ-BF5) is
// taken by every reaper pass and by the load path's inspect-and-record; its
// housekeeping callers are allowed to SKIP when it is held, because that work
// is best-effort on a debounce and another holder is already doing it. This one
// is held across a multi-minute copy, and its waiter must never skip — skipping
// is precisely how the duplicate copy happens. Folding the two together would
// park every housekeeping pass on the machine behind a 4-minute copy, and would
// let a reaper's skip-on-contention be inherited by a launch that then copies
// anyway.
//
// # Blocking, and loud about it
//
// The wait IS the feature, so it is announced rather than endured in silence: a
// launch parked on a flock with no output reads as a hang, which is the same
// argument acquireWorkspaceLock makes for probing LOCK_NB first and telling the
// user before it blocks. A launch has no quiet mode
// (docs/reference/report-tiers.md, OQ-RO3).
//
// # Every failure degrades, none refuses
//
// An unwritable state dir, or a filesystem whose flock is a no-op, leaves this
// launch doing exactly what every launch did before this file existed: its own
// copy. That is slow, not wrong — the copy is idempotent and its destination
// ref is content-addressed, so a duplicate copy produces the same image — and
// there is nothing here worth refusing a launch over.

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// imageCopyLockName is the machine-wide image-copy lock's file name.
//
// MACHINE-WIDE, not per workspace, for the same reason the housekeeping lock is:
// the thing it protects is the runtime's image store, which every workspace on
// this machine shares. Two launches in different workspaces are exactly the
// collision it exists to prevent — the measurement above is eleven of them.
const imageCopyLockName = "image-copy.lock"

// ImageCopyLockPath is that lock's path.
//
// It goes in `locks/` beside the per-workspace jail locks and the housekeeping
// lock, which is the convention internal/cli/run/run.go establishes: everything
// that serialises on a filesystem lock lives in one directory, so `yolo prune`
// and a human poking around find them together.
func ImageCopyLockPath() string {
	return filepath.Join(paths.GlobalStorage(), "locks", imageCopyLockName)
}

// copyLockNotices are the two console seams lockImageCopy speaks through.
//
// TWO SEAMS, NOT ONE, and a struct rather than two positional func params, for
// the reason internal/cli/run/flock.go's lockNotices states and this file has no
// licence to relearn: A WAIT IS NOT A WARNING. `waiting` says the serialisation
// is doing its job and a pause is expected; `warn` says the serialisation is OFF
// and this launch may duplicate another's copy. The two have identical types, so
// as positional parameters both orders compile and getting them backwards would
// tell a user that correct behaviour is a problem. Either field may be nil.
type copyLockNotices struct {
	// waiting reports that another launch holds the lock and this one is about
	// to block until it is released.
	waiting func(string)
	// warn reports that the lock could not be taken at all, so this launch
	// proceeds unserialised.
	warn func(string)
}

// copyFlockSyscall is syscall.Flock behind a package var so the degraded path —
// the one that lets a launch copy unserialised instead of failing — is reachable
// from a test. Nothing but a test ever reassigns it.
var copyFlockSyscall = syscall.Flock

// lockImageCopy takes the machine-wide image-copy lock at lockPath, BLOCKING
// until it is free, and returns the release — idempotent, and never nil.
//
// NON-BLOCKING FIRST so the contention is observable, then the blocking acquire.
// The only thing that changes between the two calls is that the user has been
// told why the wait is happening; see the file header for why silence is the
// wrong answer here.
//
// A flock ERROR that is not contention still falls through to the blocking
// acquire, because the LOCK_NB attempt is an INFORMATIONAL probe: treating an
// unexpected errno from it as fatal-to-locking would drop the serialisation this
// file exists for on the strength of a notice that failed to print.
func lockImageCopy(lockPath string, notices copyLockNotices) func() {
	notify := func(fn func(string), msg string) {
		if fn != nil {
			fn(msg)
		}
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		notify(notices.warn, "could not create the lock directory ("+err.Error()+
			"); concurrent image copies on this machine are not serialised")
		return func() {}
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		notify(notices.warn, "could not open the image-copy lock ("+err.Error()+
			"); concurrent image copies on this machine are not serialised")
		return func() {}
	}
	fd := int(f.Fd())
	if err := copyFlockSyscall(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		// EAGAIN and EWOULDBLOCK are the same errno on every platform this builds
		// for; both are named because that equality is a platform fact rather than
		// an API promise, and a port that splits them must not lose the notice.
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			notify(notices.waiting, "Waiting for another launch to finish copying an image "+
				"(lock "+filepath.Base(lockPath)+")...")
		}
		if err := copyFlockSyscall(fd, syscall.LOCK_EX); err != nil {
			notify(notices.warn, "could not acquire the image-copy lock ("+err.Error()+
				"); this launch may copy an image another launch is already copying")
			_ = f.Close()
			return func() {}
		}
	}
	// Closing the fd releases the flock. Wrapped in a Once so the release keeps
	// the contract every other lock release in this tree offers — never nil, safe
	// to call twice — which is what lets the call site hand it to a defer and
	// still release early on the branch that skips the copy.
	var once sync.Once
	return func() { once.Do(func() { _ = f.Close() }) }
}
