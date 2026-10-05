package macosuser

// sessionfiles.go gives each macos-user SESSION its own root-owned files beside the env dir,
// and a host-side record that says whether the session is still alive.
//
// A SESSION is one macos-user invocation of yolo: one sandbox and what was started for it, from
// launch to teardown (the term is paths.HostServicesSessionPrefix's). This backend has no
// container and no attach, so two terminals in one workspace are two sessions.
//
// # What was wrong
//
// The session env file, the daemons env file and the Seatbelt profile were keyed by the
// WORKSPACE's container name (<stateDir>/env/<cname>.env, <cname>.daemons.env,
// profile-<cname>.sb), so the two sessions of one workspace wrote one file each. RunMacosUser
// releases the workspace lock before the sandbox starts, so session B could rewrite A's env file
// between A's write and A's read and hand A's sandbox B's environment, which the credential gate
// scoped to another program; and the first session to end removed the env file under the other
// (docs/design/jail-lifetime-last-session-wins.md §9.9.10, row 6). No launch removed its profile
// at all, so every launch left one behind.
//
// # The mechanism
//
//   - Each launch mints a SESSION ID, 16 hex digits from crypto/rand, and names every one of
//     those files with the SESSION KEY <cname>.<id> (SessionKey). The plan's Cname stays the
//     workspace's: the launch lock and the staged trees are the workspace's, and stay shared.
//   - The launch holds a LIVENESS RECORD for its whole life: an exclusive flock on
//     <global storage>/locks/macos-user-sessions/<key>.lock, created under a pending name,
//     locked, then renamed, so it never exists unlocked under its final name. The kernel drops
//     the lock when the process dies, however it dies, so a record nobody holds is the evidence
//     that its session is gone. It is the host user's own file in the host user's own state
//     dir, so neither the sandbox account nor another account can touch it. It is independent
//     of the host-services session dir (internal/cli/run/servicessession.go), which can fail to
//     open, which `yolo host` sweeps machine-wide, and which sits in /tmp where the sandbox can
//     read it.
//   - Its teardown removes its own files (sudo rm -f), then the record, then lets the lock go.
//     A removal that fails KEEPS the record (sessionTeardown): the lock is let go and the file
//     stays, so the next launch's sweep finds it free and tries again, and the session warns
//     with the command. Once a session has ended, its record is the only pointer to its files:
//     env/ is a directory only root can list, and the names carry an id nothing else keeps.
//   - Each launch, before it writes anything root-owned, SWEEPS: every record whose lock it can
//     take is a session that ended without its teardown (a SIGKILL, a crash, a closed laptop),
//     so it removes that session's files and the record. A record that is held, cannot be
//     opened, or does not parse is kept: TRI-STATE, the rule every reaper here follows.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// SessionPlaceholder stands for the session id in a plan no launch minted one for: a dry run, and
// BuildRunPlan's callers. The same placeholder servicesSessionPlanDir uses for the host-services
// dir, so a dry run shows where each session's id goes.
const SessionPlaceholder = "<session>"

// sessionIDBytes is the session id's size: 8 random bytes, 16 hex digits.
const sessionIDBytes = 8

// newSessionID mints a session id. crypto/rand.Read does not fail in this Go (it aborts the
// process instead), so there is no error to handle.
func newSessionID() string {
	b := make([]byte, sessionIDBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SessionKey is what a session's files are named by: <cname>.<id>. The dot cannot appear in a
// container name (runtime.FromResolved keeps [a-z0-9-] only), so the key splits back
// unambiguously (parseSessionKey).
func SessionKey(cname, sessionID string) string { return cname + "." + sessionID }

// parseSessionKey splits a key into its container name and session id, strictly: the name is
// [a-z0-9-]+ and the id is exactly 16 lowercase hex digits. ok is false for anything else, which
// a sweep keeps rather than guesses about.
func parseSessionKey(key string) (cname, sessionID string, ok bool) {
	i := strings.LastIndexByte(key, '.')
	if i <= 0 {
		return "", "", false
	}
	cname, sessionID = key[:i], key[i+1:]
	if strings.Trim(cname, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return "", "", false
	}
	if len(sessionID) != 2*sessionIDBytes || strings.Trim(sessionID, "0123456789abcdef") != "" {
		return "", "", false
	}
	return cname, sessionID, true
}

// SessionFilePaths are every root-owned file a session with this key can leave in the state dir,
// in the order a sweep removes them: the session env file, the daemons env file, the CA bundle and
// the extra-CA file beside them, and the Seatbelt profile. A sweep names each one, since env/ is a
// directory only root can list.
func SessionFilePaths(key, sd string) []string {
	return []string{
		SandboxEnvFile(key, sd),
		SandboxDaemonEnvFile(key, sd),
		CABundleFile(key, sd),
		CAExtrasFile(key, sd),
		SessionProfilePath(key, sd),
	}
}

// sessionRecordLeaf is the records' directory under locks/.
const sessionRecordLeaf = "macos-user-sessions"

// sessionRecordSuffix ends every record's name; a pending record's name carries
// sessionRecordPendingSuffix after it, which the sweep's glob does not match.
const (
	sessionRecordSuffix        = ".lock"
	sessionRecordPendingSuffix = ".pending"
)

// sessionRecord is one launch's liveness record: the path it holds, and the open file whose
// exclusive flock is the evidence the session is alive.
type sessionRecord struct {
	path string
	lock *os.File
}

// openSessionRecord creates and locks this session's record in dir. The record appears under its
// final name already held: it is created under a pending name, locked, then renamed, and a flock
// belongs to the open file, so the rename keeps it. Created under its final name, it would exist
// unlocked between the create and the flock, and a sweep in that gap would read the session as
// gone and remove the files it is about to write.
func openSessionRecord(dir, key string) (*sessionRecord, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	final := filepath.Join(dir, key+sessionRecordSuffix)
	f, pending, err := createHeldPendingRecord(final)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(pending, final); err != nil {
		_ = f.Close()
		_ = os.Remove(pending)
		return nil, err
	}
	return &sessionRecord{path: final, lock: f}, nil
}

// createHeldPendingRecord is openSessionRecord's first half, split out so a test can stand in
// the gap before the rename: the record created and locked under its PENDING name, which the
// sweep's glob does not match, so nothing at final exists until it is already held.
func createHeldPendingRecord(final string) (*os.File, string, error) {
	pending := final + sessionRecordPendingSuffix
	f, err := os.OpenFile(pending, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, "", err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		_ = os.Remove(pending)
		return nil, "", err
	}
	return f, pending, nil
}

// close removes the record and then lets its lock go, in that order: a sweeper that finds the lock
// free never finds this record still standing, and one that opened it before the removal holds an
// unlinked file, which claimGoneSessionRecord refuses. Nil-safe and idempotent.
func (r *sessionRecord) close() {
	if r == nil || r.lock == nil {
		return
	}
	_ = os.Remove(r.path)
	_ = r.lock.Close()
	r.lock = nil
}

// release lets the record's lock go WITHOUT removing it, for a session that has ended with a file
// it names possibly still on disk: the next launch's sweep finds the record free and removes
// those files, then the record. Nil-safe and idempotent; a close after it removes nothing.
func (r *sessionRecord) release() {
	if r == nil || r.lock == nil {
		return
	}
	_ = r.lock.Close()
	r.lock = nil
}

// sessionTeardown is a session's removal of its own files as it ends, and what that decides about
// its record. Each removal is best effort, since a session whose agent exited must not be reported
// as failed because a file could not be removed. But a removal that fails leaves a file only the
// record points to, so finish then keeps the record for the next launch's sweep, and warns with
// the command, where it would otherwise unlink it.
type sessionTeardown struct {
	deps Deps
	// failed are the removals that failed, as the user would type them.
	failed []string
}

// remove runs each command under sudo, remembering every one that fails.
func (t *sessionTeardown) remove(cmds [][]string) {
	for _, cmd := range cmds {
		argv := append([]string{"sudo"}, cmd...)
		if t.deps.Run(argv) != 0 {
			t.failed = append(t.failed, shquote.JoinDisplay(argv))
		}
	}
}

// finish ends the session's record, LAST, after every removal: unlinked when each one succeeded,
// kept and let go when one failed. The warning names every file the session can leave (sd is the
// plan's state dir), as the sweep's does.
func (t *sessionTeardown) finish(out printer, rec *sessionRecord, key, sd string) {
	if len(t.failed) == 0 {
		rec.close()
		return
	}
	rec.release()
	then := "The next macos-user launch removes them"
	if rec == nil {
		then = "No record of this session was kept, so no launch removes them"
	}
	argv := append([]string{"sudo", rmBin, "-f"}, SessionFilePaths(key, sd)...)
	out.printf("[yellow]Warning: this session could not remove its own files from %s[/yellow] "+
		"(`%s` failed), and one may hold this launch's credentials. %s; to remove them now, run "+
		"`%s`.", stateDirOr(sd), strings.Join(t.failed, "`, `"), then, shquote.JoinDisplay(argv))
}

// stateDirOr is sd, or the state dir when sd is empty, as the path helpers read it.
func stateDirOr(sd string) string {
	if sd == "" {
		return stateDir
	}
	return sd
}

// sessionLiveness is what a record's lock says about its session.
type sessionLiveness int

const (
	// sessionUnknown: the question could not be answered (the record cannot be opened or locked,
	// or is no longer the file at its path), so a sweep keeps what the session may be using.
	sessionUnknown sessionLiveness = iota
	// sessionLive: another open file holds the record's lock.
	sessionLive
	// sessionGone: nobody holds it, so the session that made it has ended.
	sessionGone
)

// claimGoneSessionRecord asks one record whether its session is gone. On sessionGone it returns
// the record's file, now locked exclusively by this process, so no other sweeper can decide the
// same thing while the caller removes the session's files; the caller unlinks the record and
// then closes the file.
//
// O_RDWR, never O_RDONLY: a directory at a record's name opens read-only and flocks fine on
// Linux, so the read-write open is what makes it unopenable, and kept. THE LOCK MUST STILL BE THE
// FILE AT THE PATH once it is held (claimGoneServicesSession's rule): an owner removes its record
// while holding the lock, so a sweeper that opened the file before the removal and locked it
// after holds an unlinked file, and a record of the same name made since is not that one.
func claimGoneSessionRecord(path string) (*os.File, sessionLiveness) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, sessionUnknown
	}
	return claimOpenedSessionRecord(f, path)
}

// claimOpenedSessionRecord is claimGoneSessionRecord after the open, split out so the one window
// it guards, a record replaced between the open and the lock, can be tested without a race. It
// takes ownership of f: on anything but sessionGone, f is closed.
func claimOpenedSessionRecord(f *os.File, path string) (*os.File, sessionLiveness) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, sessionLive
		}
		return nil, sessionUnknown
	}
	held, herr := f.Stat()
	atPath, perr := os.Lstat(path)
	if herr != nil || perr != nil || !os.SameFile(held, atPath) {
		_ = f.Close()
		return nil, sessionUnknown
	}
	return f, sessionGone
}

// sweepGoneSessions removes the root-owned files of every session whose record says it is gone,
// then the record. EVERY WORKSPACE'S, not this workspace's alone: liveness is each record's own
// lock, so nothing another workspace's dead session left can be mistaken for a live one, and a
// workspace nobody launches again would otherwise keep its dead sessions' files for good.
//
// Best effort, never a refusal: a removal that fails is warned about, naming the command that
// would do it, and its record is kept so the next launch tries again. Silent when there is
// nothing to sweep, which is every launch whose sessions all ended normally.
func sweepGoneSessions(deps Deps, out printer, dir string) {
	if dir == "" {
		return
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*"+sessionRecordSuffix))
	if err != nil {
		return
	}
	for _, path := range matches {
		key := strings.TrimSuffix(filepath.Base(path), sessionRecordSuffix)
		if _, _, ok := parseSessionKey(key); !ok {
			continue
		}
		f, state := claimGoneSessionRecord(path)
		if state != sessionGone {
			continue
		}
		argv := append([]string{rmBin, "-f"}, SessionFilePaths(key, "")...)
		if deps.Run(append([]string{"sudo"}, argv...)) != 0 {
			out.printf("[yellow]Warning: could not remove the files a macos-user session that has "+
				"ended left in %s[/yellow] (`sudo %s` failed). The next launch tries again; to "+
				"remove them now, run that command.", stateDir, shquote.JoinDisplay(argv))
			_ = f.Close()
			continue
		}
		_ = os.Remove(path)
		_ = f.Close()
	}
}

// openSessionRecordOrWarn opens this launch's record, warning when it cannot: the session still
// runs, and its own teardown still removes its files, but a launch killed before that teardown
// leaves them for nothing to sweep, so the warning names the command that removes them. nil when
// the Deps wire no record dir, which is a test's launch.
func openSessionRecordOrWarn(deps Deps, out printer, key string) *sessionRecord {
	if deps.SessionRecordDir == nil {
		return nil
	}
	dir := deps.SessionRecordDir()
	if dir == "" {
		return nil
	}
	rec, err := openSessionRecord(dir, key)
	if err != nil {
		argv := append([]string{"sudo", rmBin, "-f"}, SessionFilePaths(key, "")...)
		out.printf("[yellow]Warning: could not record this session in %s (%v).[/yellow] It runs "+
			"anyway, and removes its own files when it ends; if it is killed first, `%s` removes "+
			"what it left in %s.", dir, err, shquote.JoinDisplay(argv), stateDir)
		return nil
	}
	return rec
}

// sessionFileInvariants is PlanInvariants' rule for the per-session names: the plan carries a
// session id, every file a session writes into the state dir is named by its key (so two sessions
// of one workspace never share one), and the plan removes its own Seatbelt profile and every env
// and CA file it writes when the session ends.
func sessionFileInvariants(plan RunPlan) []string {
	var problems []string
	if plan.SessionID == "" {
		return append(problems, "the plan carries no session id; its env file, daemons env "+
			"file and Seatbelt profile would be the workspace's, shared by every terminal in it")
	}
	key := SessionKey(plan.Cname, plan.SessionID)
	for _, f := range []struct{ what, got, want string }{
		{"Seatbelt profile", plan.ProfilePath, SessionProfilePath(key, plan.StagedDir)},
		{"session env file", plan.EnvFile, SandboxEnvFile(key, plan.StagedDir)},
		{"daemons env file", plan.DaemonEnvFile, SandboxDaemonEnvFile(key, plan.StagedDir)},
		{"CA bundle", plan.CABundleFile, CABundleFile(key, plan.StagedDir)},
		{"extra-CA file", plan.CAExtrasFile, CAExtrasFile(key, plan.StagedDir)},
	} {
		if f.got != "" && f.got != f.want {
			problems = append(problems, "the "+f.what+" is "+f.got+", not "+f.want+
				"; a file not named by this session's key ("+key+") is shared with, or removed "+
				"by, another session of the same workspace")
		}
	}
	if !removesFile(plan.ProfileRemoveCommands, plan.ProfilePath) {
		problems = append(problems, "nothing removes the session's Seatbelt profile "+
			plan.ProfilePath+" when the session ends; every launch would leave one behind")
	}
	for _, f := range []string{plan.EnvFile, plan.CABundleFile, plan.CAExtrasFile} {
		if f != "" && !removesFile(plan.EnvFileRemoveCommands, f) {
			problems = append(problems, "nothing removes "+f+" when the session ends")
		}
	}
	return problems
}

// removesFile reports whether cmds hold `rm -f <path>`.
func removesFile(cmds [][]string, path string) bool {
	for _, c := range cmds {
		if len(c) == 3 && c[0] == rmBin && c[1] == "-f" && c[2] == path {
			return true
		}
	}
	return false
}
