package durable

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// SizeRecordName is the file, directly under `<workspace>/.yolo` and beside the durable dir
// rather than in it, holding the durable dir's last COMPLETE size count. The launch line's walk
// stops at WalkBudget, which on a large dir is a small fraction of it (one launch printed
// "≥ 12.9 GB" for 410 GB), so the line shows the last complete count beside its own floor, and
// a walk with no budget (`yolo-jaild durable-size`, started by the boot when its walk stopped)
// records the next one.
//
// WRITTEN AND READ ONLY IN THE JAIL. `.yolo` is jail-writable, and host code reads no file
// content the jail could have written about the durable dir (DS-D3): the host's own reports
// (`yolo check`, `yolo stores`) measure for themselves.
const SizeRecordName = "durable-size.json"

// sizeRecordCap bounds a read of the record: one small JSON object.
const sizeRecordCap = 4 << 10

// SizeRecord is one complete count of the durable dir.
type SizeRecord struct {
	Bytes      int64     `json:"bytes"`
	MeasuredAt time.Time `json:"measured_at"`
}

// RecordSize writes s as workspace's last complete count, measured at at. A partial count is
// not a count and is refused (nothing written, no error). It never creates `.yolo`, and it
// replaces the record through a temp file and a rename, so a reader never sees half of one.
func RecordSize(workspace string, s Size, at time.Time) error {
	if s.Partial {
		return nil
	}
	b, err := json.Marshal(SizeRecord{Bytes: s.Bytes, MeasuredAt: at.UTC()})
	if err != nil {
		return err
	}
	if breach := paths.WorkspaceScopeBreach(workspace); breach != nil {
		return breach
	}
	r, err := paths.OpenStateDirRoot(paths.WorkspaceStateDir(workspace))
	if err != nil {
		return err
	}
	defer r.Close()
	tmp := SizeRecordName + "." + strconv.Itoa(os.Getpid()) + ".tmp"
	if err := paths.WriteRegularFileBeneath(r, tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := r.Rename(tmp, SizeRecordName); err != nil {
		_ = r.Remove(tmp)
		return err
	}
	return nil
}

// LastSize is workspace's last complete count, if one was recorded and can be read.
func LastSize(workspace string) (SizeRecord, bool) {
	r, err := paths.OpenStateDirRoot(paths.WorkspaceStateDir(workspace))
	if err != nil {
		return SizeRecord{}, false
	}
	defer r.Close()
	f, err := paths.OpenRegularFileBeneath(r, SizeRecordName)
	if err != nil {
		return SizeRecord{}, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, sizeRecordCap))
	if err != nil {
		return SizeRecord{}, false
	}
	var rec SizeRecord
	if err := json.Unmarshal(b, &rec); err != nil || rec.MeasuredAt.IsZero() || rec.Bytes < 0 {
		return SizeRecord{}, false
	}
	return rec, true
}

// CountMain is `yolo-jaild durable-size --workspace DIR`: one size walk of the durable dir with
// no budget, recorded (RecordSize) for the next launch line. The boot starts it detached when
// its own walk stopped at WalkBudget. It runs at the lowest CPU priority it may set, and it is
// one walk at a time per workspace: a second exits at once.
func CountMain(args []string) int {
	if len(args) != 2 || args[0] != "--workspace" || args[1] == "" {
		os.Stderr.WriteString("usage: yolo-jaild durable-size --workspace DIR\n")
		return 2
	}
	ws := args[1]
	unlock, ok := lockCount(ws)
	if !ok {
		return 0
	}
	defer unlock()
	lowerPriority()
	start := time.Now()
	s, err := Measure(ws, 0, nil)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0
		}
		os.Stderr.WriteString("durable-size: " + err.Error() + "\n")
		return 0
	}
	if err := RecordSize(ws, s, start); err != nil {
		os.Stderr.WriteString("durable-size: recording: " + err.Error() + "\n")
	}
	return 0
}

// sizeLockName serializes the background counts of one workspace.
//
// It stays at the top of `.yolo` (docs/design/workspace-state-safety.md SD-D15 moves logs, not
// this): the counts run in the jails of a workspace, two setups' jails can run at once, and a
// jail an older yolo started takes the lock here. One resource, one file, across versions.
const sizeLockName = "durable-size.lock"

// lockCount takes the workspace's count lock without waiting; ok is false when another count
// holds it or the lock cannot be made (no `.yolo`, or a linked one).
func lockCount(workspace string) (unlock func(), ok bool) {
	f, err := paths.OpenExistingWorkspaceStateFile(workspace, sizeLockName, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false
	}
	return func() { f.Close() }, true
}

// lowerPriority makes this process the last to get the CPU: the count is for the next launch,
// and never worth slowing this one.
func lowerPriority() { _ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 19) }
