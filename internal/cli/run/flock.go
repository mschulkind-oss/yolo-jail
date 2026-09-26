package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// workspaceLock is a held exclusive flock on a lock file (the per-workspace race
// guard). Close releases the lock + closes the fd (idempotent, and safe to call from two
// goroutines at once: runContainer's onStarted releases it on the proxy's goroutine while the
// normal-exit arm may release it on the launch's own).
type workspaceLock struct {
	mu     sync.Mutex
	f      *os.File
	closed bool
	// path is set only on a lock registered as this process's LAUNCH lock (holdLaunchLock), so
	// that Close can take it back out of heldLaunchLocks.
	path string
}

// lockNotices are the two console seams acquireWorkspaceLock speaks through.
//
// TWO SEAMS, NOT ONE, and the distinction is the point of the type rather than
// an accident of the signature: a WAIT IS NOT A WARNING. `warn` says the race
// guard is off and this launch may collide with another; `waiting` says the guard
// is doing exactly its job and the caller should expect a pause. Folding the
// second onto the first would have printed "Warning: Waiting for concurrent jail
// launch..." — the call site wraps warn's text in a "Warning:" prefix — telling a
// user that normal, correct serialisation is a problem. They also differ in
// arity over time (a wait has a natural end, a warning does not), so keeping
// them apart leaves room for a "done waiting" note without re-typing warn.
//
// A struct rather than two positional func params because the two have identical
// types: at the call site `warn, waiting` and `waiting, warn` both compile, and
// the failure would be silent and exactly backwards. Either field may be nil.
type lockNotices struct {
	// warn reports that the flock could not be taken at all (race protection
	// disabled) — a degraded launch that still proceeds.
	warn func(string)
	// waiting reports that another process holds the lock and this one is about
	// to block until it is released.
	waiting func(string)
}

// acquireWorkspaceLock opens lockPath and takes an exclusive flock, NON-BLOCKING
// FIRST so that contention is observable.
//
// The blocking-only version of this function was silent: a second `yolo` in the
// same workspace parked on syscall.Flock while the first built the image and
// provisioned the overlay, with no terminal output whatsoever, which reads as a
// hang rather than as the race guard working. So the acquire is now two steps —
// LOCK_NB to learn whether anyone else holds it, then the blocking acquire — and
// the only thing that changes between them is that the user is told why the wait
// is happening (workspace named, so a user with several jails knows WHICH one is
// ahead of them; lock file named, so the wait is traceable to a real path).
//
// A flock ERROR (anything that is not contention) keeps the old behavior
// deliberately: warn, and return the open file anyway so the caller proceeds
// unguarded. A workspace lock is a courtesy against a self-inflicted race, not a
// safety property worth refusing a launch over.
//
// warn/waiting are the two seams (see lockNotices); either may be nil.
func acquireWorkspaceLock(lockPath, workspace string, notices lockNotices) (*workspaceLock, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	fd := int(f.Fd())

	err = flockSyscall(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return &workspaceLock{f: f}, nil
	}
	// EAGAIN and EWOULDBLOCK are the same errno on every platform this builds
	// for; both are named because that equality is a platform fact, not an API
	// promise, and a port that split them must not lose the notice.
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		if notices.waiting != nil {
			notices.waiting(fmt.Sprintf(
				"Waiting for concurrent jail launch in workspace %s (lock %s)...",
				workspace, filepath.Base(lockPath)))
		}
	}
	// Fall through to the blocking acquire on a non-contention error too: the NB
	// attempt is an INFORMATIONAL probe, and treating an unexpected errno from it
	// as fatal-to-locking would drop a guard the old code still took. Only a
	// failure of the blocking call itself means the lock was not obtained.
	if err := flockSyscall(fd, syscall.LOCK_EX); err != nil {
		if notices.warn != nil {
			notices.warn("could not acquire workspace lock (" + err.Error() +
				"); race protection disabled")
		}
	}
	return &workspaceLock{f: f}, nil
}

// flockSyscall is syscall.Flock behind a package var so the error path above —
// the one that degrades a launch instead of failing it — is reachable from a
// test. Nothing but a test ever reassigns it.
var flockSyscall = syscall.Flock

// Close releases the flock and closes the fd. Idempotent (guarded here for the
// multiple teardown paths).
func (l *workspaceLock) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	if l.path != "" {
		heldLaunchLocks.forget(l.path, l)
	}
	_ = l.f.Close() // closing the fd releases the flock
}

// AcquireWorkspaceLockFor is the exported front door: take the per-workspace launch lock
// and return the release, which is idempotent and never nil.
//
// It exists for ONE caller outside this package — the macos-user backend, whose
// provisioning stage writes the same per-workspace npm prefix and mise store a
// concurrent launch's stage would (docs/design/macos-user-provisioning.md §4,
// "Concurrency"). That backend has no attach, so two launches on one workspace really do
// run two stages; the container serialises the same window with the same lock and then
// attaches instead.
//
// THE CALL MOVES, THE IMPLEMENTATION DOES NOT. internal/macosuser cannot import this
// package (this one imports it), so the backend takes the lock through a Deps seam the
// front door wires to this function. A second flock implementation over there would be
// the third hand-rolled copy in the tree and the one nothing compares to the others.
//
// The failure mode is the same one acquireWorkspaceLock already chose: a lock that cannot
// be taken WARNS and returns a no-op release, because a workspace lock is a courtesy
// against a self-inflicted race and not a safety property worth refusing a launch over.
//
// A LOCK THIS PROCESS ALREADY HOLDS IS HANDED OVER, NOT TAKEN AGAIN. The launch takes this
// same lock before it stages (holdLaunchLock), so by the time the backend asks for it the
// process holds it — and a second flock on a second descriptor of the same file waits on the
// first one, which in one process is a wait for ever. The backend gets the held lock's
// release instead, and calling it at its own release point (before the agent starts) is what
// ends the launch's window there.
func AcquireWorkspaceLockFor(workspace, cname string, warn, waiting func(string)) func() {
	if held := heldLaunchLocks.lookup(launchLockPath(cname)); held != nil {
		return held.Close
	}
	lockDir := filepath.Join(paths.GlobalStorage(), "locks")
	_ = os.MkdirAll(lockDir, 0o755)
	lock, err := acquireWorkspaceLock(launchLockPath(cname), workspace,
		lockNotices{warn: warn, waiting: waiting})
	if err != nil {
		if warn != nil {
			warn("could not open the workspace lock (" + err.Error() +
				"); concurrent launches in this workspace are not serialised")
		}
		return func() {}
	}
	return lock.Close
}

// launchLockPath is the per-workspace launch lock's file, <global storage>/locks/<cname>.lock:
// the one file every acquisition of it names.
func launchLockPath(cname string) string {
	return filepath.Join(paths.GlobalStorage(), "locks", cname+".lock")
}

// heldLaunchLocks is every per-workspace launch lock THIS PROCESS holds, by lock-file path —
// the record AcquireWorkspaceLockFor consults to hand a held lock over instead of waiting on
// it. Only holdLaunchLock registers and Close forgets, so an entry is exactly a lock whose
// flock this process holds right now.
var heldLaunchLocks = &launchLockRegistry{m: map[string]*workspaceLock{}}

type launchLockRegistry struct {
	mu sync.Mutex
	m  map[string]*workspaceLock
}

func (r *launchLockRegistry) hold(path string, l *workspaceLock) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[path] = l
}

func (r *launchLockRegistry) lookup(path string) *workspaceLock {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.m[path]
}

// forget removes path's entry only while it still names l, so a late Close of an earlier
// holder cannot drop the entry of the holder after it.
func (r *launchLockRegistry) forget(path string, l *workspaceLock) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m[path] == l {
		delete(r.m, path)
	}
}

// holdLaunchLock takes the per-workspace launch lock for this launch BEFORE it stages
// anything, unless this launch already holds it.
//
// THE WINDOW IS THE WORKSPACE'S SHARED STAGING, which is why it opens here and not where it
// used to (runContainer's fresh path, and the macos-user orchestrator's bootstrap). Two
// launches of one workspace share one AGENTS_DIR/<cname>: the staged packs, the skills and
// briefing staging, and on macos-user the home-overlay and /ctx trees. Each launch writes them
// and then READS THEM BACK well after writing: its host daemons start from the staged loophole
// module dirs, its skills are copied out of the staged packs, and the container binds them or
// the sandbox bootstrap copies them. Staging used to run before any lock, so a second launch
// restaged under the first one's reads. The first macOS run of
// TestMacosUserTwoConcurrentLaunchesOfOneWorkspace measured it as `unlinkat
// …/packs/_official/claude: directory not empty` in one launch and "loophole module dir … is
// not a directory" in the other (docs/reference/pack-system.md#concurrent-launches-of-one-workspace).
//
// So the lock is held from here until the launch no longer touches that staging, and released
// by whichever arm ends the window:
//
//   - podman and Apple Container, fresh launch: once the container is running (runContainer's
//     onStarted), or at any return before that, as before.
//   - podman and Apple Container, attach: before the attach execs (runContainer's attach
//     branch).
//   - macos-user: by the orchestrator, before the agent starts. It asks for this lock through
//     AcquireWorkspaceLockFor, which hands the held one over.
//   - every other return: Run's deferred releaseLaunchLock.
//
// A second launch of the workspace therefore WAITS, with the "Waiting for concurrent jail
// launch" notice, instead of restaging under the first. That is the ruled behavior: a launch's
// result never depends on another's, and it waits for what it needs
// (docs/design/pi-git-extension-caching.md OQ-2). Its own staging then runs in full, and with
// an unchanged config it changes nothing on disk (internal/treesync).
//
// The degraded mode is acquireWorkspaceLock's: a lock file that cannot be opened warns and
// leaves this launch unserialised, because a workspace lock is a courtesy and not a reason to
// refuse a launch.
func (o *Options) holdLaunchLock(cname string) {
	if o.launchLock != nil && !o.launchLock.isClosed() {
		return
	}
	out := o.pr(o.Stdout)
	sp := o.Perf.Span("launch.acquire_workspace_lock")
	defer sp.End()
	path := launchLockPath(cname)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	lock, err := acquireWorkspaceLock(path, o.Workspace, lockNotices{
		warn: func(msg string) { out.printf("[dim]Warning: %s[/dim]", msg) },
		waiting: func(msg string) {
			o.launchLockWaited = true
			out.printf("[bold cyan]%s[/bold cyan]", msg)
		},
	})
	if err != nil {
		out.printf("[dim]Warning: could not open the workspace lock (%s); concurrent launches "+
			"in this workspace are not serialised[/dim]", err.Error())
		o.launchLock = nil
		return
	}
	lock.path = path
	heldLaunchLocks.hold(path, lock)
	o.launchLock = lock
}

// releaseLaunchLock ends this launch's hold on the workspace launch lock. Idempotent, and a
// no-op for a launch that holds none.
func (o *Options) releaseLaunchLock() {
	o.launchLock.Close()
}

// isClosed reports whether Close has run.
func (l *workspaceLock) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}
