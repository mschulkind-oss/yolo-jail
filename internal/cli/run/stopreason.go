package run

// stopreason.go is the STOP RECORD: why a container jail ended, written by whatever ends it and
// read by every session the end cut short (docs/design/jail-lifetime-last-session-wins.md §2.3
// item 3 and §4.5, JL-D53).
//
// An attached session used to be told nothing when its jail ended under it. Its exec returned,
// the attach printed the broken-prefix post-mortem when that applied and the macOS OOM hint on a
// 137, and the user was left to guess why an agent in the middle of its work had vanished — most
// often because the jail's FIRST session, in another terminal, had quit, which ends the jail
// until the keeper exists (JL-D46). Now each yolo process that ends a jail says why first, in one
// record per container name, and an attach whose exec returned the way a jail's end ends it asks
// the runtime whether the jail is gone and, when it is, prints the record.
//
// WHO WRITES IT, AND WHEN. A process about to stop the jail writes it before the stop and
// replaces whatever was there, since it is the cause: stopJail (the fresh launch's signal arm,
// an attach-skew restart, the orphan reaper, a hold that did not follow its first session) and
// `yolo stop` (RecordJailStop). The fresh launch writes one more, the end of its first session,
// which ends the jail with no stop of its own (the hold follows it out). That one is a
// consequence and may itself be the result of a stop another process recorded, so it is written
// only when nothing was recorded since the session began (recordFirstSessionEnd).
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

// RecordJailStop is recordJailStop for a caller outside the run pipeline: `yolo stop`, before it
// stops the jail.
func RecordJailStop(cname, reason string) {
	writeJailStop(cname, jailStopRecord{At: time.Now(), Reason: reason, PID: os.Getpid()})
}

// firstSessionEndedReason is why a jail ends when the session that started it does.
const firstSessionEndedReason = "the session that started it ended, and a jail still ends with " +
	"the session that started it"

// recordFirstSessionEnd records that the fresh launch's first session ended, unless something
// recorded a stop since that session began: then the session ended because of that stop, and
// the record already says why.
func (o *Options) recordFirstSessionEnd(cname string, since time.Time) {
	if rec, ok := readJailStop(cname); ok && !rec.At.Before(since) {
		return
	}
	o.recordJailStop(cname, firstSessionEndedReason)
}

// launcherInterruptedReason is why the fresh launch's signal arm stops the jail.
func launcherInterruptedReason(pid int) string {
	return fmt.Sprintf("the terminal that started it was closed, or the yolo that started it "+
		"(pid %d) was sent a signal", pid)
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

// jailEndStatus reports an exec status a jail's end produces: 137, the SIGKILL the kernel sends
// every process of a pid namespace whose init exits; or the runtime's own failure when the
// container went away as the exec started (podman's 125 for a container no longer running, 255
// for one gone from its database). Any other status is the session's own, and costs no probe.
func jailEndStatus(rc int) bool { return rc == 137 || rc == 125 || rc == 255 }

// The attach's wait for a record written as its jail ended: the first session's end is recorded
// by its launcher just after the session returns, while the jail's own end follows within a poll
// of it, so the record can land a moment after the attach sees the jail gone. Vars so a test
// need not wait them out.
var (
	stopRecordWait  = time.Second
	stopRecordPoll  = 50 * time.Millisecond
	stopProbeBudget = 5 * time.Second
)

// whyTheJailEnded answers, for an attach whose exec returned rc, whether its jail ended under it,
// and why when a record says. ended is true only when the runtime answered that no container of
// the name is running: a jail still up, or a runtime that could not say, means the session ended
// on its own, and nothing is claimed. reason is "" when nothing recorded why.
func (o *Options) whyTheJailEnded(cname, rt string, rc int, since time.Time) (reason string, ended bool) {
	if !jailEndStatus(rc) {
		return "", false
	}
	if id, known := o.probeRunningContainer(cname, rt, stopProbeBudget); !known || id != "" {
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

// noteJailEnded prints why this session's jail ended, when it did, and returns whether a record
// said why: a stop then explains the 137, and the caller skips the OOM hint that would blame the
// VM for it.
func (o *Options) noteJailEnded(cname, rt string, rc int, since time.Time) (recorded bool) {
	reason, ended := o.whyTheJailEnded(cname, rt, rc, since)
	if !ended {
		return false
	}
	err := o.pr(o.Stderr)
	if reason != "" {
		err.printf("[bold yellow]This session ended because its jail stopped: %s.[/bold yellow]", reason)
		return true
	}
	err.printf("[bold yellow]This session ended because its jail stopped, and nothing recorded why: "+
		"an out-of-memory kill, a crash, or a stop from outside yolo (`%s stop`) ends a jail "+
		"this way.[/bold yellow]", rt)
	return false
}
