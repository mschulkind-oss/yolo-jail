package run

// stopreason.go is the STOP RECORD: why a container jail ended, written by whatever ends it and
// read by every session the end cut short (docs/design/jail-lifetime-last-session-wins.md §2.3
// item 3 and §4.5, JL-D53).
//
// A session used to be told nothing when its jail ended under it. Its exec returned, it printed the
// broken-prefix post-mortem when that applied and the macOS OOM hint on a 137, and the user was left
// to guess why an agent in the middle of its work had vanished. Now each yolo process that ends a
// jail says why first, in one record per container name, and a session whose exec returned the way
// a jail's end ends it asks the runtime whether the jail is gone and, when it is, prints the record
// (endSession, keeperspawn.go, for every session, the fresh launch's first included).
//
// WHO WRITES IT, AND WHEN. A process about to stop the jail writes it before the stop and
// replaces whatever was there, since it is the cause: stopJail (the keeper's drain and its signal
// arm, a launch whose keeper died, an attach-skew restart, the orphan reaper), `yolo stop` and
// `yolo check`'s orphan cleanup (RecordJailStop). The first session's end ends nothing any more:
// the jail lives while any session does, so there is no first-session record to write.
//
// HOW A READER KNOWS IT IS THIS JAIL'S. Every record carries the time it was written, and an
// attach believes only one written after it began: a record left by an earlier jail of the same
// name, or by a stop some yolo made before this one attached, is older than that. Nothing
// removes a record; the next one replaces it.
//
// WHERE. Host state no jail mounts, beside the owner-PID file of the same name
// (<machine storage>/owners/<container name>.stopped): a jail process could otherwise write the
// reason its own sessions are shown.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// jailStopRecord is one stop record.
type jailStopRecord struct {
	// At is when the record was written, which is just before the stop it explains.
	At time.Time `json:"at"`
	// Reason completes "This session ended because its jail stopped: …".
	Reason string `json:"reason"`
	// PID is the process that wrote it.
	PID int `json:"pid"`
}

// stopRecordPath is cname's stop record.
func stopRecordPath(cname string) string { return filepath.Join(ownerPIDDir(), cname+".stopped") }

// writeJailStop replaces cname's stop record, whole: a temporary file beside it, then a rename.
// Best effort: a record that cannot be written leaves the attach saying that nothing recorded
// why, which is true.
func writeJailStop(cname string, rec jailStopRecord) {
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(ownerPIDDir(), "."+cname+".stopped.*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(append(b, '\n'))
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), stopRecordPath(cname)); err != nil {
		_ = os.Remove(tmp.Name())
	}
}

// readJailStop reads cname's stop record; ok is false when there is none or it cannot be read.
func readJailStop(cname string) (jailStopRecord, bool) {
	b, err := os.ReadFile(stopRecordPath(cname))
	if err != nil {
		return jailStopRecord{}, false
	}
	var rec jailStopRecord
	if err := json.Unmarshal(b, &rec); err != nil || strings.TrimSpace(rec.Reason) == "" {
		return jailStopRecord{}, false
	}
	return rec, true
}

// recordJailStop is this process saying why it is about to stop cname's jail.
func (o *Options) recordJailStop(cname, reason string) {
	writeJailStop(cname, jailStopRecord{At: o.Now(), Reason: reason, PID: o.Getpid()})
}

// RecordJailStop is recordJailStop for a caller outside the run pipeline: `yolo stop`, and
// `yolo check`'s orphan cleanup, before either stops the jail.
func RecordJailStop(cname, reason string) {
	writeJailStop(cname, jailStopRecord{At: time.Now(), Reason: reason, PID: os.Getpid()})
}

// attachRestartReason is why an attach-skew restart stops the jail.
func attachRestartReason(pid int) string {
	return fmt.Sprintf("a launch in another terminal (pid %d) restarted it, to deliver what it "+
		"could not receive", pid)
}

// orphanReapReason is why the orphan reaper stops a jail.
func orphanReapReason(reaper, owner int) string {
	return fmt.Sprintf("a yolo launch (pid %d) stopped it as an orphan, since the yolo that "+
		"started it (pid %d) was gone", reaper, owner)
}

// YoloStopReason is why `yolo stop` stops a jail.
func YoloStopReason(pid int) string { return fmt.Sprintf("`yolo stop` (pid %d) stopped it", pid) }

// YoloCheckOrphanReason is why `yolo check`'s orphan cleanup removes a running jail: why the
// check found it orphaned ("workspace gone", "stuck in provisioning", …).
func YoloCheckOrphanReason(pid int, why string) string {
	return fmt.Sprintf("`yolo check` (pid %d) removed it as an orphaned jail (%s)", pid, why)
}

// jailEndStatus reports an exec status a jail's end produces: 137, the SIGKILL the kernel sends
// every process of a pid namespace whose init exits; or the runtime's own failure when the
// container went away as the exec started (podman's 125 for a container no longer running, 255
// for one gone from its database). Any other status is the session's own, and costs no probe.
func jailEndStatus(rc int) bool { return rc == 137 || rc == 125 || rc == 255 }

// The attach's wait for a record written as its jail ended: the first session's end is recorded
// by its launcher just after the session returns, while the jail's own end follows within a poll
// of it, so the record can land a moment after the attach sees the jail gone. Then the wait for a
// runtime whose listing trails its jail's end (listingTrailsAJailsEnd). Vars so a test need not
// wait them out.
var (
	stopRecordWait  = time.Second
	stopRecordPoll  = 50 * time.Millisecond
	stopProbeBudget = 5 * time.Second
	jailGoneWait    = 5 * time.Second
	jailGonePoll    = 100 * time.Millisecond
)

// whyTheJailEnded answers, for an attach whose exec returned rc, whether its jail ended under it,
// and why when a record says. ended is true only when the runtime answered that no container of
// the name is running (jailGoneAfterItsEnd): a jail still up, or a runtime that could not say,
// means the session ended on its own, and nothing is claimed. reason is "" when nothing recorded
// why.
func (o *Options) whyTheJailEnded(cname, rt string, rc int, since time.Time) (reason string, ended bool) {
	if !jailEndStatus(rc) {
		return "", false
	}
	if !o.jailGoneAfterItsEnd(cname, rt) {
		return "", false
	}
	deadline := time.Now().Add(stopRecordWait)
	for {
		if rec, ok := readJailStop(cname); ok && !rec.At.Before(since) {
			return rec.Reason, true
		}
		if !time.Now().Before(deadline) {
			return "", true
		}
		time.Sleep(stopRecordPoll)
	}
}

// jailGoneAfterItsEnd asks rt whether cname's jail is gone, for a session whose exec returned a
// status a jail's end gives: true only when the runtime answered that no container of the name is
// running, and false when it lists one or cannot say. A runtime whose listing trails its jail's end
// is asked again while it still lists the jail, every jailGonePoll within jailGoneWait, since its
// first answer comes before the listing has caught up with the end that returned the exec. The
// session waits that bound only for a 137, 125 or 255 its jail survives, which is its own end.
func (o *Options) jailGoneAfterItsEnd(cname, rt string) bool {
	deadline := time.Now().Add(jailGoneWait)
	for {
		id, known := o.probeRunningContainer(cname, rt, stopProbeBudget)
		switch {
		case !known:
			return false
		case id == "":
			return true
		case !listingTrailsAJailsEnd(rt) || !time.Now().Before(deadline):
			return false
		}
		time.Sleep(jailGonePoll)
	}
}

// JailGoneWait is jailGoneAfterItsEnd's bound, for the Apple Container keeper test's measure of how
// long `container ls` lists a jail a stop has ended (integration/applecontainerkeeper_test.go).
func JailGoneWait() time.Duration { return jailGoneWait }

// listingTrailsAJailsEnd reports whether rt still lists a jail as running for a while after every
// process in it has ended, so that a session's exec returns before the listing says the jail is
// gone. Apple Container does (read from apple/container 1.1.0 and containerization 0.35.0, not
// measured): `container stop` kills every process in the container (LinuxContainer.stop's kill of
// pid -1), then unmounts the rootfs, syncs and stops the VM, and only then, deregistering the
// runtime service, marks the container stopped (ContainersService.handleContainerExit), while
// `container ls` lists the containers marked running. Run 37133569003 (2026-10-03, container 1.1.0)
// recorded what a single ask gave, which fits that reading but did not observe the listing: no
// session whose jail a stop ended was told so
// (docs/design/jail-lifetime-last-session-wins.md JL-D83). Podman's `ps` reads the OCI runtime's
// live state, so its one answer stands.
func listingTrailsAJailsEnd(rt string) bool {
	return rt == "container" // parity: HonoredBy — the same answer by asking again: Apple Container marks a stopped container stopped only once its VM is down, where podman's ps reads the OCI runtime's live state
}

// reportJailEnded prints why a session's jail ended, for whyTheJailEnded's answer: nothing when it
// did not end, the record's reason when one said, and what ends a jail with no record otherwise. A
// recorded stop explains a 137, so the caller then skips the OOM hint, which would blame the
// machine's memory for it (endSession).
func (o *Options) reportJailEnded(rt, reason string, ended bool) {
	if !ended {
		return
	}
	err := o.pr(o.Stderr)
	if reason != "" {
		err.printf("[bold yellow]This session ended because its jail stopped: %s.[/bold yellow]", reason)
		return
	}
	err.printf("[bold yellow]This session ended because its jail stopped, and nothing recorded why: "+
		"an out-of-memory kill, a crash, or a stop from outside yolo (`%s stop`) ends a jail "+
		"this way.[/bold yellow]", rt)
}
