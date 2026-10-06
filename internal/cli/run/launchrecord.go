package run

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// launchrecord.go is the MACHINE-WIDE LAUNCH LINE (OQ-PR3 of
// docs/design/podman-reboot-readiness.md, ruled 2026-09-29): every launch, refused or not,
// leaves one line in GLOBAL_STORAGE/logs/launches.log, beside crossings.log, so a reboot's
// restore storm can be read in one place. The investigation behind that design could not
// find its failing launch at all: each workspace keeps its own launch.log, and the only
// machine-wide record, the crossing log, sees a jail only once it reaches a loophole, which a
// launch refused at runtime selection never does.
//
// ONE LINE, WRITTEN ONCE, AT THE MOMENT THE LAUNCH'S FATE IS KNOWN: its container started, it
// attached to a running jail, or Run returned without either. Not at exit, because a jail runs
// for hours and a storm is read while it happens; not at the start, because the outcome is
// the point. The line's time is the launch's START, which is what orders a storm.
//
// WHAT IT CARRIES, AND WHAT IT NEVER DOES. The workspace is named by paths.JailShortHash of its
// container name, the code crossings.log already uses (`jail=`), never a path or a name: the
// maintainer ruled the log fine for a jail that mounts the directory to read, and the hash is
// what keeps it from naming the user's other projects.
//
// Best-effort like every log here: a line that cannot be written costs nothing but the line.

// MachineLaunchLogName is the machine-wide launch log's leaf name, under GLOBAL_STORAGE/logs.
const MachineLaunchLogName = "launches.log"

// launchLogMaxBytes caps the active log; past it, the file is renamed to MachineLaunchLogName+".1"
// (replacing the last one) and a fresh one starts. At ~120 bytes a line that is some ten
// thousand launches per generation, and at most 2 MiB of launch log exists, ever.
const launchLogMaxBytes = 1 << 20

// MachineLaunchLogPath is where launch lines land.
func MachineLaunchLogPath() string {
	return filepath.Join(paths.GlobalStorage(), "logs", MachineLaunchLogName)
}

// Launch outcomes, the line's `outcome=` field.
const (
	// launchStarted: a fresh launch spawned its container's runtime (or, on macos-user,
	// handed the launch to that backend). Whether the container then ran is the workspace's
	// own launch.log's to say; this line is the storm's timeline.
	launchStarted = "started"
	// launchAttached: the launch attached to this workspace's running jail.
	launchAttached = "attached"
	// launchNotStarted: Run returned with no container started or attached — every refusal,
	// and a container that could not be started. rc says with what.
	launchNotStarted = "not-started"
	// launchInterrupted: a signal ended the launch before its line was written — a Ctrl-C
	// during the podman readiness wait (exit 130), or a SIGINT, SIGHUP or SIGTERM its launch
	// guard took from its pack staging on (launchguard.go), or its keeper's arm took before the
	// keeper spawned the runtime (run.go), each exiting 128+N.
	launchInterrupted = "interrupted"
)

// launchRecord is one launch's line, not yet written. A pointer on Options, because Options
// is copied by value and the Once must be shared by every copy.
type launchRecord struct {
	once  sync.Once
	start time.Time
	cname string
}

// armLaunchRecord starts this launch's record. Called by Run before anything else, the guards
// that refuse a launch before any other side effect included: they refuse launches, and the
// ruling is one line per launch, refused or not.
func (o *Options) armLaunchRecord() {
	o.launchRecord = &launchRecord{start: o.Now(), cname: runtime.FromWorkspace(o.Workspace)}
}

// recordLaunchOutcome writes this launch's line, the first time it is called; every later call
// is a no-op. rc < 0 writes no exit code (the launch is still running).
func (o *Options) recordLaunchOutcome(outcome string, rc int) {
	rec := o.launchRecord
	if rec == nil {
		return
	}
	rec.once.Do(func() {
		appendLaunchLine(MachineLaunchLogPath(), launchLine(rec, o.runtime, o.readiness, outcome, rc, o.Now()))
	})
}

// recordLaunchExit is Run's deferred record: the outcome of a launch that returned without its
// container starting or attaching, which is the only kind that has not written its line yet.
func (o *Options) recordLaunchExit(rc int) {
	outcome := launchNotStarted
	if rc == 130 && o.readinessInterrupted() {
		outcome = launchInterrupted
	}
	o.recordLaunchOutcome(outcome, rc)
}

// interruptedArmExit is the exit for an arm that can end a launch before its line is written:
// it writes the line, interrupted with the code the process exits with, then exits through
// launchArmExit as read now (an arm takes its exit when it is installed). Run's deferred record
// never runs past os.Exit. Once the line is written this adds nothing (recordLaunchOutcome's
// Once).
func (o *Options) interruptedArmExit() func(int) {
	exit := launchArmExit
	return func(code int) {
		o.recordLaunchOutcome(launchInterrupted, code)
		exit(code)
	}
}

// recordLaunchEndedBySignal writes the line of a launch the signal now ending this process ends,
// with the code it exits with (-1, no code, when no arm is ending it): a launch that another one
// running inside it, in this process, takes down with it (abandonLaunch).
func (o *Options) recordLaunchEndedBySignal() {
	rc := -1
	if sig, ending := signalEndingTheProcess(); ending {
		rc = 128 + int(sig)
	}
	o.recordLaunchOutcome(launchInterrupted, rc)
}

// launchLine renders one line: the launch's start (UTC), then key=value fields in a fixed
// order. "-" stands for a field this launch has no value for: no runtime resolved, no podman
// readiness wait (another backend, or a launch refused before it), no exit code yet.
func launchLine(rec *launchRecord, rt string, ready *runtime.ReadyResult, outcome string, rc int, now time.Time) string {
	if rt == "" {
		rt = "-"
	}
	wait, tries := "-", "-"
	if ready != nil {
		wait = fmt.Sprintf("%.1fs", ready.Elapsed.Seconds())
		tries = fmt.Sprint(len(ready.Attempts))
		if rt == "-" {
			rt = "podman" // refused at the gate, before runtime selection recorded it
		}
	}
	code := "-"
	if rc >= 0 {
		code = fmt.Sprint(rc)
	}
	return fmt.Sprintf("%s launch jail=%s runtime=%s podman_wait=%s tries=%s outcome=%s rc=%s after=%.1fs\n",
		rec.start.UTC().Format(time.RFC3339), paths.JailShortHash(rec.cname), rt, wait, tries, outcome, code,
		now.Sub(rec.start).Seconds())
}

// appendLaunchLine appends line to path, rotating first when the file is full. One flock
// covers the size check, the rotation and the write, because every launch on the machine
// writes this one file.
//
// THE LOCK IS A SIBLING FILE THAT IS NEVER ROTATED (path + ".lock"), and the log is opened
// only once it is held. A lock on the log's own descriptor locks an inode, not the name: two
// launches that opened a full log together each rotated it in turn, the second renaming the
// FRESH file (holding the first one's line) over the archive, which deleted the whole
// previous generation; and a launch that opened the fresh file meanwhile was excluded by
// nothing. A restore storm is exactly when launches meet at the boundary.
func appendLaunchLine(path, line string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer lock.Close()
	fd := int(lock.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		return
	}
	defer func() { _ = syscall.Flock(fd, syscall.LOCK_UN) }()
	if st, err := os.Stat(path); err == nil && st.Size() > 0 && st.Size()+int64(len(line)) > launchLogMaxBytes {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

// HostLaunchRuntime is the `runtime=` a host-notch launch's line carries: `yolo host -- <cmd>`
// starts no container, so the field names the notch instead of a runtime, and a reader of a
// storm can tell the host's launches from the jails' at a glance (OQ-PR3).
const HostLaunchRuntime = "host"

// HostLaunchRecord is the machine-wide launch line of one `yolo host -- <cmd>`: the SAME file,
// line format and writer a jail launch uses (launchLine, appendLaunchLine), so the one record a
// reboot's storm is read from holds the host's launches too. The workspace is the directory the
// command ran in, named by its short code like a jail's (never its path). There is no podman
// wait, so `podman_wait=- tries=-`.
//
// One line, written once: `outcome=started rc=-` immediately before the hand-over (the exec, or
// the agent started under yolo), or `outcome=not-started rc=<n>` when the launch returned without
// one. A nil *HostLaunchRecord does nothing.
type HostLaunchRecord struct {
	rec *launchRecord
}

// StartHostLaunchRecord starts the record of a host launch from workspace, timed from now.
func StartHostLaunchRecord(workspace string) *HostLaunchRecord {
	return &HostLaunchRecord{rec: &launchRecord{start: time.Now(), cname: runtime.FromWorkspace(workspace)}}
}

// Started writes the line of a launch about to hand over to its command; every later call on
// this record is a no-op.
func (h *HostLaunchRecord) Started() { h.write(launchStarted, -1) }

// NotStarted writes the line of a launch that returned rc without handing over, unless the line
// was already written (Started, then an exec that failed).
func (h *HostLaunchRecord) NotStarted(rc int) { h.write(launchNotStarted, rc) }

func (h *HostLaunchRecord) write(outcome string, rc int) {
	if h == nil || h.rec == nil {
		return
	}
	h.rec.once.Do(func() {
		appendLaunchLine(MachineLaunchLogPath(),
			launchLine(h.rec, HostLaunchRuntime, nil, outcome, rc, time.Now()))
	})
}
