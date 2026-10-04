package runtime

// macosusersessions.go is the READ-ONLY LISTER of host-services SESSIONS: the one registry the
// macos-user backend has of its running jails, which `yolo ps` lists and `yolo prune` and the
// launch's housekeeping slot read so that a sweep never removes what a live session uses.
//
// A SESSION (the term is paths.HostServicesSessionPrefix's) is one macos-user invocation of yolo,
// or one `yolo host` launch that opens a doorway: one command and the host services started for
// it, from launch to teardown. Each creates its own dir under paths.HostServicesBase, named
// yolo-host-services-<8hex>-<random>, where <8hex> is paths.JailShortHash of the workspace's
// container name, and holds an exclusive flock on paths.HostServicesSessionLockName inside it for
// its whole life (internal/cli/run/servicessession.go). The backend has no container and so no
// runtime to ask; that lock is the liveness answer, and the kernel drops it when its process
// dies, however it dies.
//
// WHY THIS IS NOT A KEEPER OR A NEW FILE OF RECORD. Every non-dry-run macos-user launch already
// opens its session before the sandbox starts and ends it after the sandbox exits, so the set of
// held session locks IS the set of running sessions. Reading it costs a glob and one
// non-blocking flock per dir.
//
// TRI-STATE, NEVER COLLAPSED (the same rule as the container listing, audit D11): a lock some
// process holds is LIVE; a lock nobody holds is GONE; and a dir with no lock file yet (a session
// between creating its dir and its lock), or a lock file that cannot be opened, is UNKNOWN. A
// caller that would delete on the strength of this treats UNKNOWN as live.
//
// NOTHING HERE REMOVES ANYTHING. The probe takes a SHARED lock and lets it go at once, so it can
// never be mistaken for the owner, and a session's own teardown, which removes its dir while it
// holds the exclusive lock, is never in its way. The collector that does remove a gone session's
// dir is the launch's (claimGoneServicesSession in internal/cli/run), and it shares only
// LockSession with this file: the removal and the check that the lock it holds is still the file
// at the path stay with the remover.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// SessionLiveness is what a session dir's lock says about the session that made it.
type SessionLiveness int

const (
	// SessionUnknown: the question could not be answered (no lock file yet, or one that
	// cannot be opened or locked), so a sweep must keep what the session may be using.
	SessionUnknown SessionLiveness = iota
	// SessionLive: another process holds the session's lock.
	SessionLive
	// SessionGone: nobody holds the lock, so the session that made the dir has ended.
	SessionGone
)

// String is the liveness word a listing prints.
func (l SessionLiveness) String() string {
	switch l {
	case SessionLive:
		return "live"
	case SessionGone:
		return "gone"
	default:
		return "unknown"
	}
}

// The notches a session record names. NotchUnknown is what a dir without a readable record
// lists as: one made by a yolo older than the record, which wrote none.
const (
	NotchMacosUser = "macos-user"
	NotchHost      = "host"
	NotchUnknown   = "unknown"
)

// SessionRecordName is the file in a session's dir that says whose session it is. Written by the
// session's own opener before its lock reaches its name (openServicesSession), 0600, so a
// listing that finds the lock held finds the record beside it. Host-only for the lock file's
// reason: the sandbox account's grant on the dir is search alone.
const SessionRecordName = ".session.json"

// SessionRecord is a session's record: the notch that opened it and the workspace it serves.
type SessionRecord struct {
	// Notch is NotchMacosUser for a macos-user launch and NotchHost for a `yolo host` launch.
	Notch string `json:"notch"`
	// Workspace is the launch's workspace path, as the launch resolved it.
	Workspace string `json:"workspace"`
	// Name is the workspace's container name (FromWorkspace), the name `yolo ps` lists.
	Name string `json:"name"`
}

// WriteSessionRecord writes r into a session dir, 0600, refusing to overwrite: a session writes
// its record once, into a dir that os.MkdirTemp has just created for it.
func WriteSessionRecord(dir string, r SessionRecord) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, SessionRecordName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(body, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// readSessionRecord reads a session dir's record, false when it has none or it does not parse.
func readSessionRecord(dir string) (SessionRecord, bool) {
	body, err := os.ReadFile(filepath.Join(dir, SessionRecordName))
	if err != nil {
		return SessionRecord{}, false
	}
	var r SessionRecord
	if err := json.Unmarshal(body, &r); err != nil || r.Notch == "" {
		return SessionRecord{}, false
	}
	return r, true
}

// LockSession opens a session dir's lock and tries to take it without blocking: exclusively for
// a collector about to remove the dir, shared for a listing. On SessionGone it returns the open,
// now-locked file, which the caller closes (a collector after its removal, a listing at once);
// otherwise nil.
//
// A MISSING LOCK FILE IS NOT EVIDENCE. A session creates its dir and then its lock, which
// reaches its name already held, so a dir with no lock yet is a session starting up: unknown.
func LockSession(dir string, exclusive bool) (*os.File, SessionLiveness) {
	flags, how := os.O_RDONLY, syscall.LOCK_SH
	if exclusive {
		flags, how = os.O_RDWR, syscall.LOCK_EX
	}
	f, err := os.OpenFile(filepath.Join(dir, paths.HostServicesSessionLockName), flags, 0)
	if err != nil {
		return nil, SessionUnknown
	}
	if err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, SessionLive
		}
		return nil, SessionUnknown
	}
	return f, SessionGone
}

// Session is one host-services session dir and what it says.
type Session struct {
	// Dir is the session's host-services dir.
	Dir string
	// Key is the 8-hex workspace key in the dir's name: paths.JailShortHash of the container
	// name the session's workspace selects. "" when the name does not carry one.
	Key      string
	Liveness SessionLiveness
	// Notch, Workspace and Name come from the session's record; NotchUnknown, "" and "" when it
	// has none.
	Notch     string
	Workspace string
	Name      string
}

// ListSessions lists every session dir under base that belongs to this user, with its liveness
// and its record. ok is false only when the listing itself could not run (the glob refused its
// pattern), which a caller must not read as "no sessions".
//
// THIS USER'S DIRS ONLY: a dir another account made is 0700 and its lock cannot be opened, so it
// could only ever list as unknown, and nothing this user's yolo does depends on another
// account's sessions. A symlink at a session's name is not a session either.
func ListSessions(base string) (sessions []Session, ok bool) {
	matches, err := filepath.Glob(paths.HostServicesSessionGlob(base))
	if err != nil {
		return nil, false
	}
	euid := os.Geteuid()
	for _, dir := range matches {
		st, err := os.Lstat(dir)
		if err != nil || !st.IsDir() {
			continue
		}
		if sys, isStat := st.Sys().(*syscall.Stat_t); isStat && int(sys.Uid) != euid {
			continue
		}
		s := Session{Dir: dir, Key: sessionKey(filepath.Base(dir)), Notch: NotchUnknown}
		f, state := LockSession(dir, false)
		if f != nil {
			_ = f.Close()
		}
		s.Liveness = state
		if r, found := readSessionRecord(dir); found {
			s.Notch, s.Workspace, s.Name = r.Notch, r.Workspace, r.Name
		}
		sessions = append(sessions, s)
	}
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].Dir < sessions[j].Dir })
	return sessions, true
}

// MacosUserSessions is ListSessions narrowed to what can be a macos-user session: every session
// whose record names that notch, and every one with no record, which an older yolo's macos-user
// launch would have left as much as its `yolo host` launch. A `yolo host` session's record says
// so, and it is not a jail.
func MacosUserSessions(base string) ([]Session, bool) {
	all, ok := ListSessions(base)
	if !ok {
		return nil, false
	}
	var out []Session
	for _, s := range all {
		if s.Notch == NotchMacosUser || s.Notch == NotchUnknown {
			out = append(out, s)
		}
	}
	return out, true
}

// sessionKey reads the 8-hex workspace key out of a session dir's base name,
// yolo-host-services-<8hex>-<random>; "" when the name does not carry one.
func sessionKey(base string) string {
	rest, found := strings.CutPrefix(base, paths.HostServicesDirName(""))
	if !found {
		return ""
	}
	key, _, found := strings.Cut(rest, "-")
	if !found || len(key) != 8 || strings.Trim(key, "0123456789abcdef") != "" {
		return ""
	}
	return key
}
