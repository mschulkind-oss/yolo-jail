// Package durable is the durable dir (a term coined in docs/design/durable-scratch-space.md
// §1.2): `<workspace>/.yolo/durable`, the one agent-neutral directory every jail backend
// exports as $YOLO_DURABLE_DIR: an agent's scratch space for what it does not want a restart to
// wipe, such as worktrees and clones (OQ-DS1). It is not part of the project, and the user never
// needs to look in it (the maintainer, 2026-10-02; DS-D35).
//
// This package holds the three things more than one caller needs: the directory's names,
// its one creator (Ensure, which the launcher alone calls on a fresh launch, DS-D2), and
// its report — what accumulates there, which yolo prints and never deletes (OQ-DS2, DS-P4).
//
// THE HOST'S CONTACT WITH THE DIRECTORY IS METADATA (DS-D3). The jail writes it through the
// workspace bind, so host code here never follows a link, never reads a file's content and
// never removes anything beneath it: Measure is an lstat walk beneath an os.Root, and the
// worktree report reads git's own admin files under the workspace's `.git`, never the
// worktrees. Nothing here runs git; the in-jail columns that need it (unique commits,
// changed files) are the caller's, and only an in-jail caller computes them.
package durable

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const (
	// DirName is the durable dir's name under `<workspace>/.yolo`. Not "scratch": in this
	// repository the scratch volumes are the per-launch mounts (OQ-DS1).
	DirName = "durable"
	// EnvVar holds the durable dir's absolute path at the jail's spelling. It is set for
	// every process in a jail, attaches included, ONLY when the directory existed at launch,
	// and never at the host notch (OQ-DS3).
	EnvVar = "YOLO_DURABLE_DIR"
	// WorktreesDir is the subdirectory the briefing sends hand-made worktrees to
	// (docs/design/durable-scratch-space.md §5.3).
	WorktreesDir = "worktrees"
	// ContainerJailPath is the durable dir on both container backends: reached through the
	// workspace's own bind at /workspace, with no mount of its own (DS-D8), so the host and
	// the jail see one relative geometry.
	ContainerJailPath = "/workspace/.yolo/" + DirName
)

// RemoveAdvice is how the briefing and `yolo check` tell a reader to remove a durable
// worktree, which the briefing has them make with `--lock`. Unlock, then a plain remove,
// which still refuses a tree with uncommitted changes; the doubled `-f` that also overrides
// the lock overrides that refusal too, so it is named only as the form that discards them.
const RemoveAdvice = "Remove one with `git worktree unlock <path> && git worktree remove <path>`, " +
	"which refuses while it holds uncommitted changes; `git worktree remove -f -f <path>` discards them."

// CleanCommand is the one command that deletes the durable dir, as the briefing and `yolo
// check` both name it. `.yolo` is an untracked directory whose every file is ignored, so only
// a clean told both to descend into untracked directories (`-d`) and to take ignored files
// (`-x` or `-X`) reaches it; `git clean -fx` alone leaves it (MEASURED,
// docs/design/durable-scratch-space.md Appendix A).
const CleanCommand = "`git clean -fdx` (or `-fdX`)"

// HostPath is the durable dir of workspace, on the host's side. It creates nothing.
func HostPath(workspace string) string {
	return filepath.Join(paths.WorkspaceStateDir(workspace), DirName)
}

// Ensure creates the durable dir if it is absent and returns its host path. It is the ONE
// writer of the directory itself (DS-D2): `.yolo` is made through
// paths.EnsureWorkspaceStateDir, so the workspace-scope refusal and the ignore file apply,
// and `durable` is made beneath an os.Root on `.yolo` that refuses `.yolo` as a link. A link
// the jail left at `durable` itself, dangling or not, is refused rather than followed. An
// existing directory is left exactly as it is; its contents are the agents'.
func Ensure(workspace string) (string, error) {
	stateDir, err := paths.EnsureWorkspaceStateDir(workspace)
	if err != nil {
		return "", err
	}
	r, err := paths.OpenStateDirRoot(stateDir)
	if err != nil {
		return "", err
	}
	defer r.Close()
	full := filepath.Join(stateDir, DirName)
	if err := r.Mkdir(DirName, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	sub, err := paths.OpenStateSubdirRoot(r, DirName, full)
	if err != nil {
		return "", err
	}
	sub.Close()
	return full, nil
}

// Reason renders an Ensure failure as the words the briefing and the launch line both print
// (docs/design/durable-scratch-space.md §5.6): short, naming the path the jail can see.
func Reason(err error, workspace string) string {
	var linked *paths.LinkedStateDirError
	if errors.As(err, &linked) {
		return "`" + relToWorkspace(linked.Path, workspace) + "` is a symbolic link"
	}
	var pe *fs.PathError
	if errors.As(err, &pe) && errors.Is(pe.Err, syscall.ENOTDIR) {
		return "`" + relToWorkspace(pe.Path, workspace) + "` is not a directory"
	}
	if errors.Is(err, fs.ErrPermission) {
		return "permission denied creating `.yolo/" + DirName + "`"
	}
	return err.Error()
}

func relToWorkspace(p, workspace string) string {
	if rel, err := filepath.Rel(workspace, p); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

// Open opens workspace's durable dir, at this frame's spelling, as an os.Root, refusing a
// link at `.yolo` or at `durable` with a *paths.LinkedStateDirError. It is how EVERY reader
// here reaches the directory: opening `<workspace>/.yolo/durable` by path would follow a
// link the jail left at `.yolo` (the jail writes it through the workspace bind) onto a host
// directory of its choosing, whose entry names and sizes host yolo would then print. It
// creates nothing; a missing directory is an fs.ErrNotExist.
func Open(workspace string) (*os.Root, error) {
	return paths.OpenWorkspaceStateSubdir(workspace, DirName)
}

// Check is Ensure's refusal without Ensure's writes: nil when the durable dir exists as a
// real directory or could be made, else the error Ensure would return for a link or a
// non-directory at `.yolo` or `durable`. An attach and a dry run, which make nothing, ask it
// why a launch has no durable dir.
func Check(workspace string) error {
	stateDir := paths.WorkspaceStateDir(workspace)
	for _, p := range []string{stateDir, filepath.Join(stateDir, DirName)} {
		fi, err := os.Lstat(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return err
		case fi.Mode()&fs.ModeSymlink != 0:
			return &paths.LinkedStateDirError{Path: p}
		case !fi.IsDir():
			return &fs.PathError{Op: "open", Path: p, Err: syscall.ENOTDIR}
		}
	}
	return nil
}

// UnavailableLine is the launch line for a launch that has no durable dir (§5.4's fourth
// form). The launcher prints it, since only the launcher knows why.
func UnavailableLine(reason string) string {
	return fmt.Sprintf("Durable dir: unavailable this launch: %s.", reason)
}
