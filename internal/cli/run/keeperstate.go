package run

// keeperstate.go is the KEEPER'S HOST STATE and every probe made of it
// (docs/design/jail-lifetime-last-session-wins.md §4.2, §9.1, §9.5; JL-D18, JL-D28, JL-D30).
//
// THREE FILES PER CONTAINER NAME, all in host state no jail mounts:
//
//   - the LIVENESS LOCK, <global storage>/locks/<cname>.keeper, beside the launch lock and the session
//     lock: an exclusive flock the keeper holds for its whole life. A free one means the keeper is
//     gone, however it died, and that, not a PID, is the evidence (JL-D18). It is one file per name,
//     never unlinked or renamed over (JL-D28), so every process that opens it locks one inode.
//   - the START RECORD, <global storage>/owners/<cname>.keeper.json, beside the owner-PID file and
//     the stop record: what the keeper started for its jail that nothing else knows the name of (the
//     skeleton, the pack tree, the scratch volumes), so the jail's last session or `yolo stop` can
//     reap an UNKEPT jail, whose keeper died, as the keeper would have (JL-D30). The keeper writes it
//     at its start and removes it at its end, so one that is present beside a free liveness lock is a
//     keeper that died.
//   - the KEEPER'S LOG (keeperframe.go).
//
// The start record also carries what the keeper saw go down while its jail was up (keeperwatch.go),
// which is why the keeper rewrites it after its start: an arrival reads it there.
//
// WHO ACTS ON A FREE LIVENESS LOCK holds it: the reaper, a quitting session that reaps, and
// `yolo stop` each take it exclusively, and the session lock after it, before they touch the jail
// (JL-D7, JL-D30). So an arrival meanwhile finds it held and waits, as for a draining keeper.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// livenessLockPath is cname's liveness lock.
func livenessLockPath(cname string) string {
	return filepath.Join(paths.GlobalStorage(), "locks", cname+".keeper")
}

// openLivenessLock opens (creating) cname's liveness lock file.
func openLivenessLock(cname string) (*os.File, error) {
	path := livenessLockPath(cname)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
}

// errKeeperAlive is a liveness lock another process holds.
var errKeeperAlive = errors.New("another process holds this jail's liveness lock")

// holdLivenessLock takes cname's liveness lock exclusively, without waiting, and keeps it: the
// keeper's own take, and the take of whoever reaps an unkept jail. errKeeperAlive when a keeper,
// or another reaper, holds it.
func holdLivenessLock(cname string) (*os.File, error) {
	f, err := openLivenessLock(cname)
	if err != nil {
		return nil, err
	}
	if err := flockSyscall(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errKeeperAlive
		}
		return nil, err
	}
	return f, nil
}

// releaseLock unlocks and closes a lock file this process holds. A nil file is a no-op.
func releaseLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

// keeperLiveness is what a probe of the liveness lock found.
type keeperLiveness int

const (
	// keeperGone: nobody holds the lock.
	keeperGone keeperLiveness = iota
	// keeperAlive: a keeper, or a reaper acting for a dead one, holds it.
	keeperAlive
	// keeperUnknown: the lock could not be opened or taken for another reason. "Could not ask" is
	// never "gone" (JL-P3): every caller treats it as alive.
	keeperUnknown
)

// probeKeeper asks cname's liveness lock whether a keeper holds it, and holds nothing afterwards.
func probeKeeper(cname string) keeperLiveness {
	f, err := holdLivenessLock(cname)
	switch {
	case err == nil:
		releaseLock(f)
		return keeperGone
	case errors.Is(err, errKeeperAlive):
		return keeperAlive
	default:
		return keeperUnknown
	}
}

// keeperRecord is the start record.
type keeperRecord struct {
	// PID is the keeper's, which the owner-PID file names too.
	PID int `json:"pid"`
	// Started is when it wrote the record.
	Started   time.Time `json:"started"`
	Workspace string    `json:"workspace"`
	Runtime   string    `json:"runtime"`
	// What the reap of an unkept jail removes, which only the launch and its keeper knew the names of.
	Skeleton       string   `json:"skeleton,omitempty"`
	PackTree       string   `json:"pack_tree,omitempty"`
	ScratchVolumes []string `json:"scratch_volumes,omitempty"`
	ForwardDir     string   `json:"forward_dir,omitempty"`
	SocketsDir     string   `json:"sockets_dir,omitempty"`
	// Scope is the systemd user scope the keeper moved into, "" for none: the reap of an unkept jail
	// stops it, ending whatever the keeper started that outlived it (JL-D32).
	Scope string `json:"scope,omitempty"`
	// Log is the keeper's log, for the lines that name it.
	Log string `json:"log,omitempty"`
	// Down is each host service the keeper runs that ended while its jail was up, in the order it
	// saw them (keeperwatch.go, JL-D19): the keeper rewrites the record as each is seen, and an
	// arrival prints them (noteServicesDown).
	Down []keeperServiceDown `json:"down,omitempty"`

	// THE ROSTER'S HALF (docs/design/jail-lifetime-last-session-wins.md §9.9.4, JL-D38, JL-D45). At
	// macos-user the start record IS the roster: what the keeper of a key started and where, so a
	// joining launch composes against it and starts nothing of its own. Every field below is empty in
	// a container jail's record. Contract is the roster's format version (keeperRosterContract): a
	// joiner reads only one it knows. Ready is set once every service listens and the record names
	// them all, Ending once the keeper began its teardown, after which no arrival joins it.
	Contract        int               `json:"contract,omitempty"`
	Notch           string            `json:"notch,omitempty"`
	Build           string            `json:"build,omitempty"`
	Ready           bool              `json:"ready,omitempty"`
	Ending          bool              `json:"ending,omitempty"`
	Services        []string          `json:"services,omitempty"`
	Endpoints       map[string]string `json:"endpoints,omitempty"`
	CallerTokens    map[string]string `json:"caller_tokens,omitempty"`
	ServedAddresses map[string]string `json:"served_addresses,omitempty"`
	Doorways        []keeperHeld      `json:"doorways,omitempty"`
	LaunchServices  []keeperHeld      `json:"launch_services,omitempty"`
	// Grant is the --with-credentials grant the jail was launched with, NAMES ONLY (jailGrant's
	// exported fields; its values are unexported and never encoded), nil for a jail launched with
	// none. An attach reads it to say what its session holds and to refuse a request the jail
	// does not hold (refuseGrantTheJailLacks, ES-D33).
	Grant *jailGrant `json:"grant,omitempty"`
}

// keeperRecordPath is cname's start record.
func keeperRecordPath(cname string) string {
	return filepath.Join(ownerPIDDir(), cname+".keeper.json")
}

// writeKeeperRecord replaces cname's start record whole: a temporary file beside it, then a rename.
func writeKeeperRecord(cname string, rec keeperRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(ownerPIDDir(), "."+cname+".keeper.*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(append(b, '\n'))
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		if werr != nil {
			return werr
		}
		return cerr
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), keeperRecordPath(cname))
}

// readKeeperRecord reads cname's start record; ok is false when there is none or it cannot be read.
func readKeeperRecord(cname string) (keeperRecord, bool) {
	b, err := os.ReadFile(keeperRecordPath(cname))
	if err != nil {
		return keeperRecord{}, false
	}
	var rec keeperRecord
	if err := json.Unmarshal(b, &rec); err != nil || rec.PID <= 0 {
		return keeperRecord{}, false
	}
	return rec, true
}

// removeKeeperRecord removes cname's start record while it still names pid, and only then: a later
// keeper's record is that keeper's (JL-D28 (4)).
func removeKeeperRecord(cname string, pid int) {
	if rec, ok := readKeeperRecord(cname); ok && rec.PID == pid {
		_ = os.Remove(keeperRecordPath(cname))
	}
}

// readOwnerPID reads cname's owner-PID file.
func readOwnerPID(cname string) (int, bool) {
	raw, err := os.ReadFile(ownerPIDFile(cname))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// clearOwnerPIDIf removes cname's owner-PID file while it still names pid, and only then (JL-D28
// (4)): a teardown that runs late must not take away the file the next jail's keeper wrote.
func clearOwnerPIDIf(cname string, pid int) {
	if got, ok := readOwnerPID(cname); ok && got == pid {
		clearOwnerPID(cname)
	}
}

// Bounds on every wait on a keeper (JL-D34). Vars so a test need not wait them out.
var (
	// keeperTeardownWait bounds how long the last session, `yolo stop`, an arrival during a drain and
	// the attach-skew restart wait for a keeper to finish. Its chain is a bounded stop (15 s), each
	// fronted daemon's stop grace (5 s), a bounded existence probe and the config capture.
	keeperTeardownWait = 2 * time.Minute
	// keeperPoll is how often those waits look again.
	keeperPoll = 50 * time.Millisecond
	// quitProbeWait bounds how long a quitting session looks for the keeper's drain after it let its
	// own lock go: the keeper, blocked on the exclusive lock, takes it within a scheduling tick.
	quitProbeWait = 2 * time.Second
)

// waitForKeeper waits, up to bound, for cname's liveness lock to come free, handing each new line of
// the keeper's log (from offset) to line as it lands. It returns true when the keeper is gone. stop,
// when it closes, ends the wait early and leaves the keeper running (a Ctrl-C during a streamed
// teardown, JL-D34).
func waitForKeeper(cname string, offset int64, bound time.Duration, line func(string), stop <-chan struct{}) bool {
	follow := &keeperLogFollower{path: keeperLogPath(cname), offset: offset}
	deadline := time.Now().Add(bound)
	for {
		gone := probeKeeper(cname) == keeperGone
		if line != nil {
			for _, l := range follow.next() {
				line(l)
			}
		}
		if gone {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		select {
		case <-stop:
			return false
		case <-time.After(keeperPoll):
		}
	}
}

// quitState is what a quitting session found once it let its own session lock go.
type quitState int

const (
	// quitOthers: other sessions still hold the jail; it stays up for them.
	quitOthers quitState = iota
	// quitLast: this was the last session, and the keeper is ending the jail.
	quitLast
	// quitUnkeptLast: the keeper is gone and this was the last session: the caller holds both locks
	// and reaps the jail (JL-D30).
	quitUnkeptLast
	// quitUnkeptOthers: the keeper is gone and other sessions remain; the last of them reaps.
	quitUnkeptOthers
	// quitNoKeeper: the jail has no keeper and never had one: a jail an older yolo started, whose
	// launcher still owns it.
	quitNoKeeper
	// quitUnknown: nothing said which, within quitProbeWait.
	quitUnknown
)

// quitLocks are the two locks a quitUnkeptLast probe returns held, for the reap.
type quitLocks struct {
	liveness, sessions *os.File
}

func (l quitLocks) release() {
	releaseLock(l.sessions)
	releaseLock(l.liveness)
}

// keeperEra reports whether cname's jail was started with a keeper: its start record is present,
// or its owner-PID file names a process that is gone (a jail of an earlier step whose launcher
// died, which the same reap serves).
func (o *Options) keeperEra(cname string) (keeperRecord, bool) {
	if rec, ok := readKeeperRecord(cname); ok {
		return rec, true
	}
	if pid, ok := readOwnerPID(cname); ok && !o.PIDAlive(pid) {
		return keeperRecord{PID: pid}, true
	}
	return keeperRecord{}, false
}

// probeAfterQuit is a quitting session's look at its jail once its own session lock is gone
// (JL-D11, JL-D30; the probe's order is JL-D57). A keeper blocked on the exclusive session lock
// takes it within a scheduling tick of the last shared one going, so readSessionLockHolder's answer
// is the state: the keeper's exclusive hold is the last session gone, a shared hold is another
// session, and nobody is the keeper not yet having run, so look again.
//
// A free liveness lock is no keeper: an unkept jail (keeperEra), reaped by the session that finds
// no other one, or a jail an older yolo's launcher still owns.
func (o *Options) probeAfterQuit(cname string) (quitState, quitLocks) {
	return o.probeAfterQuitWithin(cname, quitProbeWait)
}

// probeAfterQuitWithin is probeAfterQuit looking again for nobody's hold only until wait runs out,
// quitUnknown after it. A wait of 0 reads the lock once.
//
// A KEEPER CAN BE GONE BEFORE THE FIRST LOOK. Nobody holding the session lock is the keeper not yet
// having taken it, or the keeper having taken it, ended and let it go already, which a keeper at
// macos-user does in a few milliseconds: it holds no container, so its teardown is its own services'
// stops. So each look that finds nobody asks the liveness lock again, and a keeper found gone is
// read as a probe begun then reads it (quitAtGoneKeeper), except that one whose record is gone too
// finished its teardown, which its log holds: the last session's (quitLast). Read only once, at
// the start, the last session of a key whose keeper was that fast said it could not tell.
func (o *Options) probeAfterQuitWithin(cname string, wait time.Duration) (quitState, quitLocks) {
	live, err := holdLivenessLock(cname)
	switch {
	case errors.Is(err, errKeeperAlive):
		// The keeper is alive: below.
	case err != nil:
		return quitUnknown, quitLocks{}
	default:
		return o.quitAtGoneKeeper(cname, live, false)
	}
	deadline := time.Now().Add(wait)
	for {
		f, err := openSessionLock(cname)
		if err != nil {
			return quitUnknown, quitLocks{}
		}
		holder := readSessionLockHolder(f)
		_ = f.Close()
		switch holder {
		case heldExclusive:
			return quitLast, quitLocks{}
		case heldShared:
			return quitOthers, quitLocks{}
		case heldUnknown:
			return quitUnknown, quitLocks{}
		}
		live, err := holdLivenessLock(cname)
		switch {
		case err == nil:
			return o.quitAtGoneKeeper(cname, live, true)
		case !errors.Is(err, errKeeperAlive):
			return quitUnknown, quitLocks{}
		}
		if !time.Now().Before(deadline) {
			return quitUnknown, quitLocks{}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// quitAtGoneKeeper is a quitting session's answer once it holds cname's liveness lock, live: no keeper
// is alive. A key with no record of a keeper is one that never had one (quitNoKeeper), or, when
// wasAlive says a keeper held the lock when the probe began, one whose keeper finished its teardown
// (quitLast). A record is a keeper that died (keeperEra): its roster is marked ending first
// (markKeyEnding), and the session lock decides between the reap (quitUnkeptLast, both locks held
// for it) and the other sessions that remain (quitUnkeptOthers).
func (o *Options) quitAtGoneKeeper(cname string, live *os.File, wasAlive bool) (quitState, quitLocks) {
	rec, era := o.keeperEra(cname)
	if !era {
		releaseLock(live)
		if wasAlive {
			return quitLast, quitLocks{}
		}
		return quitNoKeeper, quitLocks{}
	}
	markKeyEnding(cname, rec.PID)
	sessions, ok := tryExclusiveSessionLock(cname)
	if !ok {
		releaseLock(live)
		return quitUnkeptOthers, quitLocks{}
	}
	return quitUnkeptLast, quitLocks{liveness: live, sessions: sessions.f}
}

// markKeyEnding marks key's roster ending while it still names the keeper pid, and only a roster: a
// container jail's start record is left as it is. It is what a REAPER writes first once it holds a
// dead keeper's liveness lock (a quitting session's probe, `yolo stop`): an arrival that finds that
// lock held cannot tell the reaper from a keeper, and it reads the roster only once it has counted
// itself (arriveMacosUser), so it then waits for the reap, as for a keeper's own teardown, instead of
// joining services that are gone (JL-D44).
func markKeyEnding(key string, pid int) {
	rec, ok := readKeeperRecord(key)
	if !ok || rec.PID != pid || rec.Notch == "" || rec.Ending {
		return
	}
	rec.Ending = true
	_ = writeKeeperRecord(key, rec)
}

// keeperRosterPresent reports whether key's roster file exists, whether or not it can be read: an
// arrival waits for a keeper whose roster is gone (one finishing, which removes it before it lets
// its liveness lock go) and refuses only one whose roster is there and unreadable.
func keeperRosterPresent(key string) bool {
	_, err := os.Lstat(keeperRecordPath(key))
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

// sessionLockHolder is who holds a jail's session lock, as non-blocking takes read it.
type sessionLockHolder int

const (
	// heldByNobody: neither a session nor the keeper holds it.
	heldByNobody sessionLockHolder = iota
	// heldExclusive: the keeper holds it exclusively, draining the jail.
	heldExclusive
	// heldShared: one or more sessions hold it shared.
	heldShared
	// heldUnknown: a take failed for a reason other than contention.
	heldUnknown
)

// readSessionLockHolder reads who holds the session lock open at f, without waiting. A shared take
// that fails is an exclusive hold, and an exclusive take that succeeds is nobody. An exclusive take
// that fails after a shared one succeeded is NOT yet a session's shared hold: a keeper blocked on
// the exclusive lock can take it between the two takes, and its failure is then the keeper's hold.
// So a second shared take decides: it fails under the keeper's exclusive hold, and succeeds beside
// the sessions' shared ones. Read without that second take, a last session the keeper overtook said
// its jail stayed up for other sessions while the keeper tore it down.
func readSessionLockHolder(f *os.File) sessionLockHolder {
	fd := int(f.Fd())
	shared := func() (bool, sessionLockHolder) {
		err := flockSyscall(fd, syscall.LOCK_SH|syscall.LOCK_NB)
		if err == nil {
			_ = syscall.Flock(fd, syscall.LOCK_UN)
			return true, heldByNobody
		}
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return false, heldExclusive
		}
		return false, heldUnknown
	}
	if ok, h := shared(); !ok {
		return h
	}
	err := flockSyscall(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		return heldByNobody
	}
	if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
		return heldUnknown
	}
	if ok, h := shared(); !ok {
		return h
	}
	return heldShared
}

// keeperDraining reports whether cname's keeper is ending its jail: it is alive, and it holds the
// session lock exclusively, so a session's shared take would fail. An arrival that finds this waits
// for the keeper instead of counting itself into a jail being stopped (JL-D28).
func keeperDraining(cname string) bool {
	if probeKeeper(cname) != keeperAlive {
		return false
	}
	f, err := openSessionLock(cname)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := flockSyscall(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// A KEY (docs/design/jail-lifetime-last-session-wins.md §1.1, JL-D37) is what one keeper holds:
// a container jail's name at a container backend, and the workspace's container name with the
// notch appended at macos-user, so one workspace can have a jail, a macos-user key and a guest
// key at once, each with its own liveness lock, session lock, arrival lock, log and record. A
// container name never holds a dot (runtime.FromWorkspace), so no key is another's.
const (
	// keeperNotchMacosUser is the macos-user backend at the jail notch, and keeperNotchGuest the
	// same backend at the guest notch: a key never spans two notches, since what its sessions share
	// would carry from the more confined to the less (JL-D37).
	keeperNotchMacosUser = "macos-user"
	keeperNotchGuest     = "guest"
)

// macosUserKeeperNotches are the keys a workspace's macos-user sessions can hold, in the order
// `yolo stop` ends them.
var macosUserKeeperNotches = []string{keeperNotchMacosUser, keeperNotchGuest}

// keeperKey is the key a keeper of cname's workspace at notch holds: cname itself for a container
// jail (notch ""), and cname.notch otherwise.
func keeperKey(cname, notch string) string {
	if notch == "" {
		return cname
	}
	return cname + "." + notch
}

// keeperRosterContract is the roster's format version (JL-D45): what its fields mean and which
// token variables they name. A keeper of another build that writes the same version is joined, and
// the joiner says it runs another build; one that writes a version this build does not know is
// refused. Raise it whenever a field changes meaning, never when one is only added.
const keeperRosterContract = 1

// arrivalLockPath is a key's ARRIVAL LOCK (JL-D37): the lock an arrival at macos-user takes to
// probe the key's keeper and count itself, which plays the launch lock's part at a container
// (§4.2). The fresh launch hands it to the keeper, which lets it go once its roster is written.
func arrivalLockPath(key string) string {
	return filepath.Join(paths.GlobalStorage(), "locks", key+".arrival")
}

// THE SESSION RECORDS (JL-D44): each macos-user session of a key names itself in a file of its own,
// so a refused arrival can name the sessions still running and `yolo stop` can signal them. They
// never count: the session lock does (JL-D2). A record is LIVE while its session holds the flock on
// it, which the kernel drops however the session dies, so a SIGKILLed session's record reads stale
// with no pid or start time to compare (an implementation decision recorded as JL-D87, the shape
// JL-D84's macos-user session records took first). EVERY session writes one, a launch that plans
// nothing a keeper holds included (JL-D42), so `yolo stop` ends it too; such a session's record says
// it is not KEPT, and the lines that name or count a keeper's sessions read only the kept ones
// (keptSessions).

// keyedSessionsDir is the directory key's session records live in, in host state no jail mounts.
func keyedSessionsDir(key string) string {
	return filepath.Join(paths.GlobalStorage(), "locks", key+".session-records")
}

// keyedSession is one live session of a key, as its record names it.
type keyedSession struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	// Kept is set for a session of the key's keeper, one that joined it or started it, and unset for
	// a launch that planned nothing a keeper holds and runs with none (JL-D42).
	Kept bool `json:"kept,omitempty"`
	path string
}

// keptSessions is the sessions of sessions that are the keeper's (keyedSession.Kept).
func keptSessions(sessions []keyedSession) []keyedSession {
	var out []keyedSession
	for _, s := range sessions {
		if s.Kept {
			out = append(out, s)
		}
	}
	return out
}

// keyedSessionRecordPending is a record's name between its creation and its flock, which no
// reader reads (servicessession.go's reason for its own lock).
const keyedSessionRecordPending = ".pending"

// openKeyedSessionRecord writes this session's record into key's directory, holding its flock
// for as long as the returned file stays open: created under a pending name, locked, written, and
// only then renamed to its final name, so a reader never finds it unlocked. kept is the record's
// keyedSession.Kept.
func openKeyedSessionRecord(key string, pid int, started time.Time, kept bool) (*os.File, error) {
	dir := keyedSessionsDir(key)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, strconv.Itoa(pid)+".*"+keyedSessionRecordPending)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*os.File, error) {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, err
	}
	if err := flockSyscall(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fail(err)
	}
	body, err := json.Marshal(keyedSession{PID: pid, Started: started, Kept: kept})
	if err != nil {
		return fail(err)
	}
	if _, err := f.Write(append(body, '\n')); err != nil {
		return fail(err)
	}
	final := strings.TrimSuffix(f.Name(), keyedSessionRecordPending) + ".json"
	if err := os.Rename(f.Name(), final); err != nil {
		return fail(err)
	}
	return f, nil
}

// closeKeyedSessionRecord removes this session's record, then lets its flock go. A nil file is a
// no-op.
func closeKeyedSessionRecord(f *os.File) {
	if f == nil {
		return
	}
	_ = os.Remove(strings.TrimSuffix(f.Name(), keyedSessionRecordPending) + ".json")
	_ = f.Close()
}

// liveKeyedSessions is every session of key whose record is still held, oldest first. A record
// nobody holds is a session that ended without removing it, and is removed here while it is still
// the file at its path; one that cannot be opened or read is left, and is not named.
func liveKeyedSessions(key string) []keyedSession {
	matches, _ := filepath.Glob(filepath.Join(keyedSessionsDir(key), "*.json"))
	var out []keyedSession
	for _, path := range matches {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		err = flockSyscall(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
		if err == nil {
			held, herr := f.Stat()
			atPath, perr := os.Lstat(path)
			if herr == nil && perr == nil && os.SameFile(held, atPath) {
				_ = os.Remove(path)
			}
			_ = f.Close()
			continue
		}
		var s keyedSession
		data, rerr := io.ReadAll(io.LimitReader(f, 4096))
		_ = f.Close()
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			continue
		}
		if rerr != nil || json.Unmarshal(data, &s) != nil || s.PID <= 0 {
			continue
		}
		s.path = path
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

// sessionsThat words sessions as the subject of a clause whose verb agrees with them: one is the
// verb for a single session and many for any other count, as in "1 session (pid 4242, since
// 09:14:03) still runs" and "2 sessions (…; …) still run". With none, which a count held by sessions
// that could not record themselves leaves, it names them as unnamed: "sessions yolo cannot name
// still run".
func sessionsThat(sessions []keyedSession, one, many string) string {
	if len(sessions) == 0 {
		return "sessions yolo cannot name " + many
	}
	var parts []string
	for _, s := range sessions {
		parts = append(parts, fmt.Sprintf("pid %d, since %s", s.PID, s.Started.Format("15:04:05")))
	}
	return fmt.Sprintf("%d %s (%s) %s", len(sessions), plural(len(sessions), "session", "sessions"),
		strings.Join(parts, "; "), plural(len(sessions), one, many))
}

// keeperGrantedPath is the record of the endpoint files of key's keeper a session's stage has
// granted the sandbox account (§9.9.5): a joiner grants only a file it does not find here.
func keeperGrantedPath(key string) string {
	return filepath.Join(ownerPIDDir(), key+".granted")
}

// readKeeperGranted is the set the record names, empty when there is none.
func readKeeperGranted(key string) map[string]bool {
	data, err := os.ReadFile(keeperGrantedPath(key))
	out := map[string]bool{}
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out[line] = true
		}
	}
	return out
}

// recordKeeperGranted adds paths to the record, replacing it whole.
func recordKeeperGranted(key string, paths []string) {
	have := readKeeperGranted(key)
	for _, p := range paths {
		have[p] = true
	}
	lines := make([]string, 0, len(have))
	for p := range have {
		lines = append(lines, p)
	}
	sort.Strings(lines)
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(ownerPIDDir(), "."+key+".granted.*")
	if err != nil {
		return
	}
	_, werr := tmp.WriteString(strings.Join(lines, "\n") + "\n")
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	_ = os.Chmod(tmp.Name(), 0o600)
	if err := os.Rename(tmp.Name(), keeperGrantedPath(key)); err != nil {
		_ = os.Remove(tmp.Name())
	}
}
