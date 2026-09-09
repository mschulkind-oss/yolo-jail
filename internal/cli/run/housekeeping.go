package run

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// housekeeping.go is the POST-LAUNCH SLOT and the lock that serialises it
// (disk-levers-and-backfill.md §5.1, OQ-BF5).
//
// HOUSEKEEPING SLOT — the window in the host `yolo` process AFTER the container
// is running and attached, while the launcher is otherwise waiting on its child.
// It exists structurally: the launcher spawns the runtime and waits, it does not
// exec into it. Work done there never delays the jail, needs no daemon (it dies
// with the launch), and runs on a host process with the host's view of every
// store.
//
// THREE PROPERTIES OF THE SLOT THAT ARE NOT OBVIOUS, and that anything added
// here has to respect:
//
//  1. THE GOROUTINE DIES ON os.Exit. The terminate arm exits the process, so a
//     class can be cut mid-delete. Every pass must therefore be restartable and
//     must stamp its debounce ONLY on completion — a stamp written first turns
//     one interrupted pass into a day of not running.
//  2. STDOUT IS THE CONTAINER'S BY THEN. The pty is attached, so anything the
//     slot prints on stdout lands inside the user's session output. Slot work
//     writes to stderr or not at all.
//  3. IT IS NOT A BACKGROUND JOB. Same process, same lifetime, same YOLO_*
//     environment the launch had.

// housekeepingLockName is the machine-wide lock every housekeeping pass and
// every load-and-record step takes.
//
// MACHINE-WIDE, not per workspace: the stores it protects (podman's image store,
// build/roots, the caches) are machine-wide, so two launches in different
// workspaces are exactly the collision it exists to prevent.
const housekeepingLockName = "housekeeping.lock"

// HousekeepingLockPath is that lock's path, beside the per-workspace locks.
func HousekeepingLockPath() string {
	return filepath.Join(paths.GlobalStorage(), "locks", housekeepingLockName)
}

// withHousekeepingLock runs fn while holding the machine-wide housekeeping lock,
// and SKIPS fn entirely if the lock is already held.
//
// SKIP RATHER THAN WAIT, deliberately. Every caller is best-effort housekeeping
// on a debounce; another launch holding the lock means the work is already being
// done, so waiting would buy a duplicate pass at the price of blocking a launch.
// The one thing that must not happen is two passes interleaving, and skipping
// prevents that as completely as waiting does.
//
// THE RACE THIS CLOSES is narrower than "two reapers at once", because `rmi` is
// no longer forced (feddc5e0) and fails on an image with a container. What is
// left is the window between another launch's image inspect and its
// AddLoadedPath: B decides its image is present, A's reap sees no container on
// it yet and no sentinel entry, and removes it — then B's `podman run` fails on
// an image that existed a moment ago. So the LOAD side takes the same lock
// around its re-inspect-and-record, and only that: holding it across the stream
// itself would serialise every launch's image load on the machine.
func (o *Options) withHousekeepingLock(fn func()) {
	path := HousekeepingLockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	if ferr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); ferr != nil {
		_ = f.Close()
		return // held elsewhere: the other holder is doing this work
	}
	defer func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}()
	fn()
}

// runHousekeeping is the slot's body: every automatic class, in one place, under
// one lock, after the container is up.
//
// Ordering inside it is not load-bearing today — each class is independent and
// debounced on its own stamp. What IS load-bearing is that this runs AFTER the
// container is visible (so a reap can never race this launch's own image) and
// that nothing here can fail the launch.
func (o *Options) runHousekeeping(rt string) {
	sp := o.Perf.Span("housekeeping.slot")
	defer sp.End()
	o.withHousekeepingLock(func() {
		o.autoReapOldImages(rt)
	})
}

// lockHousekeepingFn is the load path's half of OQ-BF5's lock: it hands
// image.AutoLoadImage a way to take the SAME machine-wide lock the slot takes,
// so an inspect-and-record can never interleave with a reap.
//
// It BLOCKS where the slot skips, and the asymmetry is the point. The slot is
// best-effort work on a debounce — if someone else holds the lock, the work is
// already happening and skipping costs nothing. A launch cannot skip: it needs
// the window closed or its `podman run` may fail on an image removed underneath
// it. The wait is bounded by the other holder's pass, which is housekeeping and
// short.
//
// nil in-jail: nothing inside a jail reaps the host's images, so there is
// nothing to serialise against and the lock file is not even on a shared
// filesystem there.
func (o *Options) lockHousekeepingFn() func() func() {
	if o.inJail() {
		return nil
	}
	return func() func() {
		path := HousekeepingLockPath()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return func() {}
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return func() {}
		}
		if ferr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); ferr != nil {
			_ = f.Close()
			return func() {}
		}
		return func() {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
		}
	}
}
