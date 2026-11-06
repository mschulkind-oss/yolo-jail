package paths

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// Per-setup state (docs/design/named-setups.md §9, SU-D6): each setup in a project keeps its
// home and run state under <workspace>/.yolo/setups/<name>/; approvals, grants, .yolo/durable,
// handover.md and launch.log stay directly under .yolo. These three are path arithmetic and
// create nothing; EnsureSetupStateDir creates.

// SetupsDir returns <workspace>/.yolo/setups, the parent of every setup's state.
func SetupsDir(workspace string) string {
	return filepath.Join(WorkspaceStateDir(workspace), "setups")
}

// SetupStateDir returns <workspace>/.yolo/setups/<name>, one setup's state.
func SetupStateDir(workspace, name string) string {
	return filepath.Join(SetupsDir(workspace), name)
}

// SetupHomeState returns <workspace>/.yolo/setups/<name>/home, one setup's home: the per-setup
// successor of LegacyHomeState.
func SetupHomeState(workspace, name string) string {
	return filepath.Join(SetupStateDir(workspace, name), "home")
}

// HomeStateRel returns home relative to <workspace>/.yolo when home is one of the workspace's
// homes: the legacy "home" (LegacyHomeState), or "setups/<name>/home" for a valid setup name
// (SetupHomeState). ok is false for any other path, so a reader handed a home by another process
// (a registration file, a guest's environment) can check that it names this workspace's.
func HomeStateRel(workspace, home string) (rel string, ok bool) {
	if workspace == "" || home == "" {
		return "", false
	}
	rel, err := filepath.Rel(WorkspaceStateDir(workspace), filepath.Clean(home))
	if err != nil {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	switch {
	case len(parts) == 1 && parts[0] == "home":
	case len(parts) == 3 && parts[0] == "setups" && setup.ValidName(parts[1]) && parts[2] == "home":
	default:
		return "", false
	}
	return rel, true
}

// IsWorkspaceHomeState reports whether home is one of workspace's homes (HomeStateRel).
func IsWorkspaceHomeState(workspace, home string) bool {
	_, ok := HomeStateRel(workspace, home)
	return ok
}

// OpenWorkspaceHomeRoot opens home, one of workspace's homes (HomeStateRel), as an os.Root,
// refusing a symbolic link at `.yolo` and at every component below it with a
// *LinkedStateDirError naming the component: each is jail-writable through the workspace bind.
// It creates nothing; a missing component is an fs.ErrNotExist. A home that is not one of the
// workspace's is fs.ErrInvalid.
func OpenWorkspaceHomeRoot(workspace, home string) (*os.Root, error) {
	rel, ok := HomeStateRel(workspace, home)
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: home, Err: fs.ErrInvalid}
	}
	dir := WorkspaceStateDir(workspace)
	cur, err := OpenStateDirRoot(dir)
	if err != nil {
		return nil, err
	}
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		dir = filepath.Join(dir, seg)
		next, err := OpenStateSubdirRoot(cur, seg, dir)
		cur.Close()
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

// WorkspaceHomeStates lists the homes workspace holds now, sorted: the legacy home and every
// setup's home that is a real directory. A symbolic link at `.yolo`, `setups`, a setup's
// directory or a home is not followed and not listed, so a reader that walks each one (prune)
// never walks where a jail pointed it. A workspace with none lists nothing.
func WorkspaceHomeStates(workspace string) []string {
	state, err := OpenStateDirRoot(WorkspaceStateDir(workspace))
	if err != nil {
		return nil
	}
	defer state.Close()
	isDir := func(r *os.Root, name string) bool {
		fi, err := r.Lstat(name)
		return err == nil && fi.IsDir()
	}
	var out []string
	if isDir(state, "home") {
		out = append(out, LegacyHomeState(workspace))
	}
	setups, err := OpenStateSubdirRoot(state, "setups", SetupsDir(workspace))
	if err != nil {
		return out
	}
	defer setups.Close()
	f, err := setups.Open(".")
	if err != nil {
		return out
	}
	names, _ := f.Readdirnames(-1)
	f.Close()
	sort.Strings(names)
	for _, name := range names {
		if !setup.ValidName(name) || !isDir(setups, name) {
			continue
		}
		d, err := OpenStateSubdirRoot(setups, name, SetupStateDir(workspace, name))
		if err != nil {
			continue
		}
		if isDir(d, "home") {
			out = append(out, SetupHomeState(workspace, name))
		}
		d.Close()
	}
	return out
}

// SplitHomeState splits home, a workspace home in either spelling (LegacyHomeState or
// SetupHomeState), into its workspace and its path under <workspace>/.yolo (HomeStateRel).
// ok is false for a path that is neither, so a reader can re-root a home onto its own
// spelling of the workspace without guessing which form it holds.
func SplitHomeState(home string) (workspace, rel string, ok bool) {
	home = filepath.Clean(home)
	for _, up := range []int{2, 4} {
		ws := home
		for i := 0; i < up; i++ {
			ws = filepath.Dir(ws)
		}
		if r, ok := HomeStateRel(ws, home); ok && strings.Count(filepath.ToSlash(r), "/") == up-2 {
			return ws, r, true
		}
	}
	return "", "", false
}
