package run

// sessionlock.go is the SESSION LOCK: the host-side count of the sessions in a container jail
// (docs/design/jail-lifetime-last-session-wins.md §4.2, JL-D2).
//
// A session is one `yolo` invocation that runs a command in a container jail — its launcher on
// the host and the process tree it started inside (the design's §1.1). Each session's launcher
// holds LOCK_SH on one file per container name for as long as it runs; the kernel drops the
// lock however the launcher dies, SIGKILL included, so the lock is the count and nothing has
// to keep it. It is HOST state that no jail mounts (the launch lock's own directory), so
// nothing inside a jail can hold a jail open (JL-P2).
//
// WHAT READS IT is the jail's KEEPER, which blocks on the lock taken exclusively and ends the jail
// once it holds it — zero sessions (keeper.go, the design's §9.5); a quitting session, which asks
// after letting its own go whether it was the last (probeAfterQuit, keeperstate.go); and the orphan
// reaper, which reaps a jail only when it can take the lock exclusively and holds it across the
// reap (JL-D7), so a jail whose owner died while a terminal was attached is not stopped under that
// terminal by the next `yolo` in any workspace (the design's §2.3, item 4).
//
// THE RULES THE DESIGN SETS, and where each is kept:
//
//   - It is taken UNDER THE LAUNCH LOCK, before that lock is released: the fresh launch before
//     its container starts, an attach before its exec. There is then no instant at which a
//     session is inside the jail but uncounted.
//   - No process upgrades a lock it holds (flock(2): conversion "is not guaranteed to be atomic:
//     the existing lock is first removed"); the reaper takes a lock of its own.
//   - The file is one per container name and is never unlinked or renamed over (JL-D28), so
//     every process that opens it locks the same inode.
//   - "Could not count" is never zero (JL-P3): a lock file the reaper cannot open means
//     sessions may remain.
//   - Go opens files O_CLOEXEC, so no child a launcher starts — the runtime client included —
//     inherits the lock.

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// sessionLockPath is the container name's session lock: <global storage>/locks/<cname>.sessions,
// beside the workspace launch lock (<cname>.lock) and in the same host-only directory.
func sessionLockPath(cname string) string {
	return filepath.Join(paths.GlobalStorage(), "locks", cname+".sessions")
}

// sessionLockWait bounds how long a session waits for LOCK_SH while another process holds the
// lock exclusively — a reaper stopping the jail, which takes seconds. A var so a test need
// not wait it out.
var sessionLockWait = 30 * time.Second

// sessionLockPoll is how often that wait tries again: flock(2) takes no timeout.
var sessionLockPoll = 100 * time.Millisecond

// sessionLock is one session's hold on its jail's session lock. A nil one is valid and holds
// nothing.
type sessionLock struct {
	f *os.File
}

// openSessionLock opens (creating) the session lock file for cname.
func openSessionLock(cname string) (*os.File, error) {
	path := sessionLockPath(cname)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
}

// errKeeperDraining is a session lock the jail's keeper holds exclusively: zero sessions, and the
// keeper is ending the jail. A session never counts itself into it, and never waits for it here,
// where its caller holds the launch lock the keeper's teardown guards take non-blocking
// (docs/design/jail-lifetime-last-session-wins.md JL-D28): the caller waits for the keeper instead,
// with the launch lock released.
var errKeeperDraining = errors.New("this jail's keeper is ending it")

// takeSessionLock takes LOCK_SH on cname's session lock, waiting up to sessionLockWait while
// another process holds it exclusively. contended reports that it had to wait: the exclusive
// holder was a reaper stopping this jail, so the caller looks at the container again. When the
// exclusive holder is the jail's own keeper, draining, it returns errKeeperDraining at once. A nil
// lock with a nil error never happens; an error means this session is uncounted.
func takeSessionLock(cname string) (lock *sessionLock, contended bool, err error) {
	f, err := openSessionLock(cname)
	if err != nil {
		return nil, false, err
	}
	deadline := time.Now().Add(sessionLockWait)
	for {
		err := flockSyscall(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
		if err == nil {
			return &sessionLock{f: f}, contended, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = f.Close()
			return nil, contended, err
		}
		contended = true
		if probeKeeper(cname) == keeperAlive {
			_ = f.Close()
			return nil, true, errKeeperDraining
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, contended, errors.New("another yolo process has held it exclusively for " +
				sessionLockWait.String())
		}
		time.Sleep(sessionLockPoll)
	}
}

// release lets this session's lock go. Idempotent, and a no-op on a nil lock.
func (l *sessionLock) release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	l.f = nil
}

// tryExclusiveSessionLock is the reaper's take: LOCK_EX|LOCK_NB, which succeeds only while no
// session holds the lock. ok is false when a session does, or when the file cannot be opened
// or locked for any other reason — "could not count" is never zero (JL-P3). A jail launched
// before the session lock existed has no file yet; opening creates it, nobody holds it, and
// the take succeeds, which is that jail's old owner-PID rule unchanged.
func tryExclusiveSessionLock(cname string) (*sessionLock, bool) {
	f, err := openSessionLock(cname)
	if err != nil {
		return nil, false
	}
	if err := flockSyscall(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, false
	}
	return &sessionLock{f: f}, true
}

// holdSessionLock counts this launch as a session of cname (takeSessionLock) and keeps the
// hold on o until Run's deferred release. It says so when it cannot, and the launch goes on
// uncounted rather than refused over it. The count decides when the jail's keeper ends the jail,
// and whether a reaper may stop it: a fresh launch that goes on uncounted tells its keeper so,
// which then never drains on the count (keeperPlan.Uncounted, JL-P3), and an uncounted attach is
// in a jail its keeper may end under it once the counted sessions have left, which the warning
// says. contended is takeSessionLock's.
func (o *Options) holdSessionLock(cname string) (contended bool) {
	if o.sessionLock != nil {
		return false
	}
	lock, contended, err := takeSessionLock(cname)
	if errors.Is(err, errKeeperDraining) {
		o.keeperDrainSeen = true
		return true
	}
	if err != nil {
		o.pr(o.Stderr).printf("[dim]Warning: could not count this session in %s (%s)[/dim]",
			cname, err.Error())
		return contended
	}
	o.sessionLock = lock
	return contended
}

// releaseSessionLock ends this launch's hold on its session lock. Idempotent.
func (o *Options) releaseSessionLock() {
	o.sessionLock.release()
	o.sessionLock = nil
}
