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
	"os"
	"path/filepath"
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
func (o *Options) probeAfterQuitWithin(cname string, wait time.Duration) (quitState, quitLocks) {
	live, err := holdLivenessLock(cname)
	switch {
	case errors.Is(err, errKeeperAlive):
		// The keeper is alive: below.
	case err != nil:
		return quitUnknown, quitLocks{}
	default:
		if _, era := o.keeperEra(cname); !era {
			releaseLock(live)
			return quitNoKeeper, quitLocks{}
		}
		sessions, ok := tryExclusiveSessionLock(cname)
		if !ok {
			releaseLock(live)
			return quitUnkeptOthers, quitLocks{}
		}
		return quitUnkeptLast, quitLocks{liveness: live, sessions: sessions.f}
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
		if !time.Now().Before(deadline) {
			return quitUnknown, quitLocks{}
		}
		time.Sleep(10 * time.Millisecond)
	}
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
