package cli

// hostapplylock.go names ONE WRITER for a host home (docs/design/host-apply-staleness.md §4.6).
//
// # Why idempotence is not enough
//
// The render is idempotent, so two concurrent host applies converge on the same content — that
// is the load-bearing fact of the whole design (§2.1) and it is a statement about the RESULT,
// not about the writes that produce it. Two processes read-modify-writing one
// `~/.claude/settings.json` interleave at the file, not at the value: each decodes the file it
// found, folds its layers, and writes its own whole text back, so the later write silently
// discards whatever the earlier one added. `applyHost` never needed a lock because its caller
// was always a human running one command; §4.3's gate makes an agent launch a caller, and two
// wrapped agents started at once are the ordinary case (R4).
//
// # A launch that cannot take the lock EXECS
//
// It does not wait, and it does not refuse. Waiting would put a launch behind another
// process's apply — including one stopped at a [y/N] prompt, which is an unbounded pause on
// somebody else's terminal — and refusing would make starting two agents at once a failure.
// "Another process is applying this home right now" is a state this launch cannot resolve and
// cannot describe, which is precisely §4.4's cannot-determine class: exec, one line, and let
// the other writer finish. The next launch sees the settled home.
//
// # The path is the key
//
// §4.6 asks for a lockfile "keyed by the resolved home", and the state dir already is that
// key: paths.GlobalStorageUnder(home) resolves to `<home>/.local/share/yolo-jail`, so two
// homes cannot name one lock file and no hashing is needed. Deriving a hashed basename the way
// the per-workspace lock does (runtime.FromWorkspace, because a workspace has no state dir of
// its own to live in) would encode the home twice and make the file unfindable by the one
// route a human takes — looking under the state dir for the thing holding them up.
//
// It goes in `locks/` beside the per-workspace jail locks, which is the convention
// internal/cli/run/run.go establishes for exactly this: everything that serialises on a
// filesystem lock is in one directory, so `yolo prune` and a person poking around find them
// together.

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostApplyLock is a held exclusive flock on one home's host-apply lock file.
type hostApplyLock struct {
	f      *os.File
	closed bool
}

// Close releases the flock and closes the fd. Idempotent, and nil-safe so the caller can
// defer it on a path where the lock was never taken.
func (l *hostApplyLock) Close() {
	if l == nil || l.closed {
		return
	}
	l.closed = true
	_ = l.f.Close() // closing the fd releases the flock
}

// hostApplyLockPath is the lock file for one resolved home.
func hostApplyLockPath(home string) string {
	return filepath.Join(paths.GlobalStorageUnder(home), "locks", "host-apply.lock")
}

// tryHostApplyLock takes the home's host-apply lock WITHOUT BLOCKING, returning nil when it
// could not be taken.
//
// NON-BLOCKING ONLY, which is where this deliberately diverges from
// run.acquireWorkspaceLock: that one probes non-blocking for the sake of a "waiting…" notice
// and then blocks, because a jail launch genuinely wants to attach to whatever the other
// process is building. Here there is nothing to attach to and nothing worth waiting for — see
// the file header.
//
// EVERY FAILURE IS THE SAME ANSWER: contention, an unwritable state dir, a filesystem whose
// flock is a no-op. The caller has exactly one thing to do with all of them (exec), so
// distinguishing them here would produce an error nobody could act on differently. The
// mkdir is best-effort for the same reason.
func tryHostApplyLock(home string) *hostApplyLock {
	path := hostApplyLockPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	if err := hostApplyFlock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil
	}
	return &hostApplyLock{f: f}
}

// hostApplyFlock is syscall.Flock behind a package var so the contention path — the one that
// degrades a launch instead of failing it — is reachable from a test without a second process.
// Nothing but a test reassigns it.
var hostApplyFlock = syscall.Flock
