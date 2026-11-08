package paths

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// THE PROJECT'S LOGS LIVE IN <workspace>/.yolo/logs/ (docs/design/workspace-state-safety.md
// SD-D15), not at the top level of `.yolo`, where twenty-odd logs beside the agents' homes made
// the directory read as build output. Every writer of one goes through OpenWorkspaceLogFile (or
// OpenExistingWorkspaceLogFile, or a root from OpenWorkspaceLogsRoot), and every host-side reader
// through FindWorkspaceLog, so the directory's name is written down once.
//
// # A RUNNING JAIL KEEPS WRITING THE OLD PATH
//
// An attach never re-stages, and a running jail keeps the binaries it booted with, so a jail an
// older yolo started goes on writing its logs directly under `.yolo` while a newer host `yolo`
// reads. That is why a reader asks FindWorkspaceLog rather than joining the new path: it takes
// whichever of the two is a regular file, the newer when both are. Old logs at the top level are
// never moved or deleted: moving files under a jail that may be running is the hazard the design
// exists to prevent, and a stale log beside the new directory costs nothing. README.md says so.
//
// # THE LOCKS STAY WHERE THEY ARE, FOR NOW
//
// The locks directly under `.yolo` (scratch-rm.lock, durable-size.lock, receipts.jsonl.lock) are
// each taken by more than one process lifetime, by a launcher and by a jail's daemons or by two
// launchers, which during the transition may be two yolo versions. Two versions taking two
// different files for one resource is two holders of one lock, so they stay at the top level
// until a release after every jail started before this one has stopped. A log's own sidecar
// lock (startup.log.lock, from internal/logcap) moves WITH its log: it guards that one file, so
// an old writer's lock on the old file and a new writer's lock on the new file never guard the
// same bytes.

// WorkspaceLogsDirName is the directory under <workspace>/.yolo that holds the project's logs.
const WorkspaceLogsDirName = "logs"

// WorkspaceLogsDir returns <workspace>/.yolo/logs. Path arithmetic; it creates nothing.
func WorkspaceLogsDir(workspace string) string {
	return filepath.Join(WorkspaceStateDir(workspace), WorkspaceLogsDirName)
}

// WorkspaceLogPath returns <workspace>/.yolo/logs/<name>, where this yolo writes the log name.
func WorkspaceLogPath(workspace, name string) string {
	return filepath.Join(WorkspaceLogsDir(workspace), name)
}

// LegacyWorkspaceLogPath returns <workspace>/.yolo/<name>, where a yolo older than SD-D15
// wrote the log name, and where a jail one of them started still writes it.
func LegacyWorkspaceLogPath(workspace, name string) string {
	return filepath.Join(WorkspaceStateDir(workspace), name)
}

// WorkspaceLogRel returns ".yolo/logs/<name>", the log's path relative to the workspace, for a
// message that names it where the workspace is implied (`/workspace/.yolo/logs/boot.log`).
func WorkspaceLogRel(name string) string {
	return filepath.Join(".yolo", WorkspaceLogsDirName, name)
}

// FindWorkspaceLog returns the path a host-side reader should open for the log name: whichever
// of WorkspaceLogPath and LegacyWorkspaceLogPath is a regular file, the more recently modified
// when both are, and WorkspaceLogPath when neither is. Neither name is followed (Lstat), so a
// link the jail left at either is not chosen. The reader still opens the result beneath a root.
func FindWorkspaceLog(workspace, name string) string {
	cur, legacy := WorkspaceLogPath(workspace, name), LegacyWorkspaceLogPath(workspace, name)
	curT, curOK := regularModTime(cur)
	legacyT, legacyOK := regularModTime(legacy)
	if legacyOK && (!curOK || legacyT.After(curT)) {
		return legacy
	}
	return cur
}

func regularModTime(p string) (time.Time, bool) {
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

// OpenWorkspaceLogsRoot opens <workspace>/.yolo/logs as an os.Root, creating `.yolo` through
// EnsureWorkspaceStateDir (the scope refusal, the .gitignore and the README apply) and `logs`
// beneath a root on `.yolo`. A link at `.yolo` or at `logs` is refused with a
// *LinkedStateDirError, never followed: both are jail-writable.
func OpenWorkspaceLogsRoot(workspace string) (*os.Root, error) {
	dir, err := EnsureWorkspaceStateDir(workspace)
	if err != nil {
		return nil, err
	}
	parent, err := OpenStateDirRoot(dir)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return openLogsBeneath(parent, dir, true)
}

// openLogsBeneath opens `logs` below parent (the root on stateDir), making it first when create
// is set.
func openLogsBeneath(parent *os.Root, stateDir string, create bool) (*os.Root, error) {
	if create {
		if err := parent.Mkdir(WorkspaceLogsDirName, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
	}
	return OpenStateSubdirRoot(parent, WorkspaceLogsDirName, filepath.Join(stateDir, WorkspaceLogsDirName))
}

// OpenWorkspaceLogFile opens the log name in <workspace>/.yolo/logs with flag and perm, on
// OpenWorkspaceStateFile's terms: anything at name that is not a regular file is replaced when
// flag creates, the opened file is checked to be regular, and name is one path component.
func OpenWorkspaceLogFile(workspace, name string, flag int, perm fs.FileMode) (*os.File, error) {
	if !singleComponent(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	r, err := OpenWorkspaceLogsRoot(workspace)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return openRegularBeneath(r, name, flag, perm)
}

// OpenExistingWorkspaceLogFile is OpenWorkspaceLogFile for a writer that must never create
// <workspace>/.yolo (OpenExistingWorkspaceStateFile states why). The `logs` directory is made
// only when flag creates, and only beneath the `.yolo` held open as a root, so a `.yolo` deleted
// meanwhile is not resurrected.
func OpenExistingWorkspaceLogFile(workspace, name string, flag int, perm fs.FileMode) (*os.File, error) {
	if !singleComponent(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if breach := WorkspaceScopeBreach(workspace); breach != nil {
		return nil, breach
	}
	dir := WorkspaceStateDir(workspace)
	parent, err := OpenStateDirRoot(dir)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	r, err := openLogsBeneath(parent, dir, flag&os.O_CREATE != 0)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return openRegularBeneath(r, name, flag, perm)
}

// OpenStateFileBeneath opens name below r, a root on a jail-writable directory, with flag and
// perm, on OpenWorkspaceStateFile's terms: a non-regular file at name is replaced when flag
// creates, and the opened file is checked to be regular. For a writer that already holds the
// root, such as the boot log, which rotates its previous log beneath the same root first.
func OpenStateFileBeneath(r *os.Root, name string, flag int, perm fs.FileMode) (*os.File, error) {
	return openRegularBeneath(r, name, flag, perm)
}

func singleComponent(name string) bool {
	return name != "" && filepath.Base(name) == name && name != "." && name != ".."
}
