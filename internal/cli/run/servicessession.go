package run

// servicessession.go is ONE HOST-SERVICES DIR PER macos-user SESSION
// (docs/design/host-daemon-ownership.md#OQ-HD10, the teardown defect its second run measured).
//
// A SESSION is one macos-user invocation of yolo: one sandbox and the host services started for
// it, from launch to teardown (paths.HostServicesSessionPrefix defines the term). The backend
// has no container and no attach, so two terminals in one workspace are two sessions, and both
// used to publish into the one dir the workspace's container name selects
// (paths.HostServicesDir). The second session's front replaced the first's endpoint file, and
// the first session to end removed the dir under the other. Measured on a Mac: the surviving
// session's claude-oauth-broker endpoint was gone, and before that it had named the other
// session's front.
//
// Each session now creates a dir of its own, /tmp/yolo-host-services-<8hex>-<random>, and:
//
//   - publishes every endpoint file of its launch there, and keys its fronted daemons' upstream
//     sockets by that dir (frontShortHash), so nothing another session writes or removes is
//     this session's;
//   - HOLDS an exclusive flock on paths.HostServicesSessionLockName inside it for its whole life,
//     and removes the dir at its own teardown, before it lets the lock go;
//   - collects, when it starts, every OTHER session dir whose lock nobody holds. The kernel drops
//     a flock when its process dies, however it dies, so a free lock is the evidence that the
//     session that made the dir is gone. That is the liveness answer, and it is TRI-STATE: a
//     lock that is held, or a dir with no lock file, or a lock file that cannot be opened, is a
//     question this cannot answer, and it collects nothing.
//
// It mirrors per-launch pack trees (docs/reference/pack-system.md#oq-pk2): each launch owns
// what it writes, tears down only its own, and a dead owner's copy is collected on evidence.
// The host-wide daemon behind a `scope: "host"` loophole is untouched by any of this: it was
// never in the dir, and each session's front over it lives in that session's own yolo process.

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// servicesSession is one macos-user session's own host-services dir and the lock that says the
// session is alive.
type servicesSession struct {
	dir  string
	lock *os.File
}

// openServicesSession creates this session's host-services dir, takes its liveness lock, and
// first collects every session dir on the machine whose owner is known to be gone. Its one
// caller is the spawn (startLoopholesMatching), for the reason mkdirHostServicesDir's is:
// every path out of that call reaches the arm's teardown, and a creation anywhere earlier
// would have none on the refusal paths between the two.
//
// os.MkdirTemp is the creation, and both of its properties are wanted here. The dir is 0700,
// which svcendpoint requires of a dir it publishes a bearer token into; and the name is new and
// taken exclusively, so another account cannot claim it in /tmp ahead of this one, which a
// deterministic path cannot promise.
//
// THE LOCK APPEARS UNDER ITS NAME ALREADY HELD. It is created as servicesSessionLockPending,
// locked, and only then renamed to paths.HostServicesSessionLockName; a flock belongs to the
// open file, so the rename keeps it. Created under its final name, it would exist unlocked
// between the create and the flock, and another session's sweep could lock it in that gap,
// read "gone", and remove the dir this session is about to publish into. A sweep reads the
// final name only, so what it can see in the gap is a dir with no lock file, which it keeps.
//
// THE RECORD IS WRITTEN BEFORE THE LOCK REACHES ITS NAME, for the same reason: a reader that
// finds the lock held finds the record beside it. It names the notch that opened the session
// (notch: "macos-user", or "host" for a `yolo host` launch's doorways) and the workspace, and it
// is what `yolo ps` lists and `yolo prune` reads on macos-user, which has no container runtime to
// ask (runtime.ListSessions). Not fatal: a session whose record could not be written still runs,
// and a listing shows its notch and workspace as unknown, as it does for a dir an older yolo made.
func (o *Options) openServicesSession(cname, notch string) (*servicesSession, error) {
	base := paths.HostServicesBase(o.IsMacOS)
	o.collectDeadServicesSessions(base)
	dir, err := os.MkdirTemp(base, paths.HostServicesSessionPrefix(cname))
	if err != nil {
		return nil, err
	}
	_ = runtime.WriteSessionRecord(dir, runtime.SessionRecord{Notch: notch, Workspace: o.Workspace, Name: cname})
	pending := filepath.Join(dir, servicesSessionLockPending)
	f, err := os.OpenFile(pending, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := os.Rename(pending, filepath.Join(dir, paths.HostServicesSessionLockName)); err != nil {
		_ = f.Close()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &servicesSession{dir: dir, lock: f}, nil
}

// servicesSessionLockPending is the lock file's name between its creation and its flock. No
// sweep reads it.
const servicesSessionLockPending = paths.HostServicesSessionLockName + ".pending"

// release lets the session's liveness lock go. Called only after the dir is removed, so a
// sweeper that finds the lock free never finds this session's dir still standing.
func (s *servicesSession) release() {
	if s == nil || s.lock == nil {
		return
	}
	// Nothing was written to this fd, so a Close error cannot lose data, and the flock goes
	// with the close either way.
	_ = s.lock.Close()
	s.lock = nil
}

// endServicesSession is the macos-user arm's teardown: stop this session's services, then remove
// this session's own dir and release its lock, in that order.
//
// stopLoopholes is handed no cname, so it takes none of the container guards: no relaunch lock
// and no container-exists probe. Both answer a question about a dir that a relaunch of the same
// jail publishes into, and nothing but this session ever publishes into this one, so the dir is
// removed unconditionally, and only this one.
func (o *Options) endServicesSession(handles []loopholeDaemon) {
	s := o.servicesSession
	o.servicesSession = nil
	if s == nil {
		// The session dir was never created (openServicesSession's failure was reported at the
		// spawn), so no service started and there is nothing of this session's to remove.
		o.stopLoopholes(handles, "", "", "")
		return
	}
	o.stopLoopholes(handles, s.dir, "", "")
	s.release()
}

// claimGoneServicesSession asks one session dir's lock whether its session is gone. On
// SessionGone it returns the lock, now held exclusively by this process, so the dir can be
// removed while no other sweeper can decide the same thing; the caller removes it and then closes
// the lock. The open and the flock are runtime.LockSession's, the one probe this collector shares
// with the read-only listing behind `yolo ps` and `yolo prune`; the removal and the check below
// stay here, with the only code that removes.
//
// A MISSING LOCK FILE IS NOT EVIDENCE (LockSession says why). The leak this costs is a dir whose
// owner died between creating its dir and its lock, which stays in /tmp until the machine
// restarts.
//
// THE LOCK MUST STILL BE THE FILE AT THE PATH once it is held. The owner removes its dir while
// holding the lock and releases the lock afterwards, so a sweeper that opened the file before the
// removal and locked it after holds an unlinked file. Removing by name at that point would remove
// whatever dir now has the name, and a dir a new session created there is the one that matters.
func claimGoneServicesSession(dir string) (*os.File, runtime.SessionLiveness) {
	f, state := runtime.LockSession(dir, true)
	if state != runtime.SessionGone {
		return nil, state
	}
	held, herr := f.Stat()
	atPath, perr := os.Lstat(filepath.Join(dir, paths.HostServicesSessionLockName))
	if herr != nil || perr != nil || !os.SameFile(held, atPath) {
		_ = f.Close()
		return nil, runtime.SessionUnknown
	}
	return f, runtime.SessionGone
}

// collectDeadServicesSessions removes every session dir under base whose session is known to be
// gone, with that session's fronted daemons' upstream sockets, and leaves every other one.
//
// EVERY WORKSPACE'S, not this workspace's alone. Liveness is a property of each session's own
// lock, not of the workspace, so there is nothing a second workspace's dead session could be
// mistaken for, and a workspace nobody launches again would otherwise keep its dead sessions'
// dirs until the machine restarts. Another account's dirs are 0700 and theirs, so their locks
// cannot be opened, which is sessionUnknown: kept.
//
// SILENT, except for a removal that failed. A dead session's endpoint files name listeners that
// died with it, so what a leftover costs is litter, not a wrong address anyone is handed: no
// session reads another session's dir.
func (o *Options) collectDeadServicesSessions(base string) {
	matches, _ := filepath.Glob(paths.HostServicesSessionGlob(base))
	for _, dir := range matches {
		lock, state := claimGoneServicesSession(dir)
		if state != runtime.SessionGone {
			continue
		}
		retireFrontSockets(frontShortHash(dir))
		if err := os.RemoveAll(dir); err != nil {
			o.pr(o.Stdout).printf("[yellow]Warning: could not remove the host-services dir %s "+
				"of a macos-user session that has ended (%v).[/yellow]", dir, err)
		}
		_ = lock.Close()
	}
}

// servicesSessionPlanDir is the dir a --dry-run names for this session's endpoints. A plan render
// starts nothing, so no session dir exists and its random suffix is not known; the placeholder
// stands where the suffix goes, so the plan shows the shape of the path and of the grant the
// sandbox will get on it.
func servicesSessionPlanDir(cname string, isMacOS bool) string {
	return filepath.Join(paths.HostServicesBase(isMacOS), paths.HostServicesSessionPrefix(cname)+"<session>")
}

// servicesSessionFailure is the warning a launch prints when its session dir cannot be made.
func servicesSessionFailure(err error) string {
	return fmt.Sprintf("[yellow]Warning: could not create this session's host-services dir "+
		"(%v); no host service starts for it, so the sandbox cannot reach any.[/yellow]", err)
}
