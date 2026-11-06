package paths

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

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

// A FRESH HOME (docs/design/workspace-state-safety.md §3.6, SD-D14) is the home a
// `yolo --fresh-home` launch binds instead of its setup's: a new empty directory beside the
// setup's home, <workspace>/.yolo/setups/<name>/home.fresh-<stamp>, kept after the launch until the
// user deletes it. Its capture store is prism.fresh-<stamp> beside it (PrismStateRel), so nothing
// the fresh session does reaches the setup's own home or its captured edits. It is one of the
// workspace's homes for every reader handed it (HomeStateRel), and deliberately NOT listed by
// WorkspaceHomeStates, the list the reapers walk: no reaper touches a fresh home.

// FreshHomePrefix starts a fresh home's directory name; FreshPrismPrefix its capture store's.
const (
	FreshHomePrefix  = "home.fresh-"
	FreshPrismPrefix = "prism.fresh-"
)

// freshStampLayout is a fresh home's stamp: the moment it was made, in UTC, sortable, as a
// rescue's is.
const freshStampLayout = "20060102T150405Z"

// freshStamp matches a fresh home's stamp: freshStampLayout, with the numbered suffix a second
// fresh home made in the same second gets.
var freshStamp = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z(-[0-9]{1,3})?$`)

// FreshHomeName is a fresh home's directory name for the moment now; NewFreshHome adds a suffix
// when it is taken.
func FreshHomeName(now time.Time) string {
	return FreshHomePrefix + now.UTC().Format(freshStampLayout)
}

// IsFreshHomeName reports whether name is a fresh home's directory name (FreshHomePrefix and a
// stamp).
func IsFreshHomeName(name string) bool {
	stamp, ok := strings.CutPrefix(name, FreshHomePrefix)
	return ok && freshStamp.MatchString(stamp)
}

// IsFreshHomeRel reports whether rel, a home relative to .yolo (HomeStateRel), is a fresh home.
func IsFreshHomeRel(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return len(parts) == 3 && parts[0] == "setups" && setup.ValidName(parts[1]) && IsFreshHomeName(parts[2])
}

// IsFreshHomeState reports whether home is one of workspace's fresh homes.
func IsFreshHomeState(workspace, home string) bool {
	rel, ok := HomeStateRel(workspace, home)
	return ok && IsFreshHomeRel(rel)
}

// HomeStateRel returns home relative to <workspace>/.yolo when home is one of the workspace's
// homes: the legacy "home" (LegacyHomeState), "setups/<name>/home" for a valid setup name
// (SetupHomeState), or a fresh home "setups/<name>/home.fresh-<stamp>" (IsFreshHomeName). ok is
// false for any other path, so a reader handed a home by another process (a registration file, a
// guest's environment) can check that it names this workspace's.
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
	case len(parts) == 3 && parts[0] == "setups" && setup.ValidName(parts[1]) &&
		(parts[2] == "home" || IsFreshHomeName(parts[2])):
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
	return openWorkspaceStateRel(workspace, rel)
}

// HomeStateEnv names, in a jail's environment, the home its launch bound, relative to the
// workspace's .yolo ("home" or "setups/<name>/home", HomeStateRel). The session's own readers (its
// capture store, capture-run's surface door) then use the home the launch chose rather than guess
// it from the setup, which the legacy home's SUP-D21 attribution would get wrong
// (named-setups-plan.md SUP-D34).
const HomeStateEnv = "YOLO_HOME_STATE"

// ValidHomeStateRel reports whether rel is a home spelled relative to .yolo: "home",
// "setups/<valid name>/home" or a fresh home "setups/<valid name>/home.fresh-<stamp>".
func ValidHomeStateRel(rel string) bool {
	if rel == "" || filepath.IsAbs(rel) {
		return false
	}
	_, ok := HomeStateRel("/w", filepath.Join(WorkspaceStateDir("/w"), filepath.FromSlash(rel)))
	return ok
}

// PrismStateRel is the capture store (the prism sidecars: last_render, overlay, selection) of the
// home at homeRel, relative to .yolo: the "prism" beside it, `.yolo/prism` for the legacy home and
// `.yolo/setups/<name>/prism` for a setup's. A baseline belongs to the home it was rendered into,
// so one setup's boot never diffs its home against another setup's render (named-setups-plan.md
// SUP-D35). A fresh home's is its own `prism.fresh-<stamp>`, never the setup's, so a fresh
// session's captured edits stay with it. An invalid homeRel names the legacy store.
func PrismStateRel(homeRel string) string {
	if !ValidHomeStateRel(homeRel) {
		return "prism"
	}
	dir, base := filepath.Split(filepath.FromSlash(homeRel))
	if stamp, ok := strings.CutPrefix(base, FreshHomePrefix); ok {
		return filepath.Join(dir, FreshPrismPrefix+stamp)
	}
	return filepath.Join(filepath.Dir(filepath.FromSlash(homeRel)), "prism")
}

// OpenWorkspacePrismRoot opens the capture store of the home at homeRel (PrismStateRel) as an
// os.Root, refusing a symbolic link at `.yolo` and at every component below it, as
// OpenWorkspaceHomeRoot does. It creates nothing.
func OpenWorkspacePrismRoot(workspace, homeRel string) (*os.Root, error) {
	return openWorkspaceStateRel(workspace, PrismStateRel(homeRel))
}

// OpenWorkspaceStateRel opens rel, an existing directory below <workspace>/.yolo spelled
// relative to it ("prism", "setups/<name>/prism", a home), as an os.Root, refusing a symbolic link
// at `.yolo` and at every component below it with a *LinkedStateDirError naming the component. A
// rel that is absolute, empty, or climbs out with ".." is fs.ErrInvalid. It creates nothing.
func OpenWorkspaceStateRel(workspace, rel string) (*os.Root, error) {
	clean := filepath.ToSlash(filepath.Clean(rel))
	if rel == "" || filepath.IsAbs(rel) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return nil, &fs.PathError{Op: "open", Path: rel, Err: fs.ErrInvalid}
	}
	return openWorkspaceStateRel(workspace, clean)
}

// openWorkspaceStateRel opens rel, a relative path beneath <workspace>/.yolo, one no-link
// component at a time.
func openWorkspaceStateRel(workspace, rel string) (*os.Root, error) {
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
// setup's home that is a real directory. A fresh home is not listed (WorkspaceFreshHomes lists
// those): this is the list the reapers walk, and no reaper touches a fresh home. A symbolic link at `.yolo`, `setups`, a setup's
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

// SetupStateNames lists, sorted, the setups with state in workspace now: every real directory
// under .yolo/setups whose name is a setup name (setup.ValidName). A link at `.yolo`, `setups` or
// a setup's directory is not followed and not listed. A workspace with none lists nothing.
func SetupStateNames(workspace string) []string {
	r, err := openWorkspaceStateRel(workspace, "setups")
	if err != nil {
		return nil
	}
	defer r.Close()
	f, err := r.Open(".")
	if err != nil {
		return nil
	}
	names, _ := f.Readdirnames(-1)
	f.Close()
	sort.Strings(names)
	var out []string
	for _, name := range names {
		if fi, err := r.Lstat(name); err == nil && fi.IsDir() && setup.ValidName(name) {
			out = append(out, name)
		}
	}
	return out
}

// ReadSetupStateFile reads name, a regular file directly in <workspace>/.yolo/setups/<setupName>,
// through a no-link descriptor chain: a link at `.yolo`, `setups`, the setup's directory or the
// file is never followed. A file or directory that is not there is an fs.ErrNotExist.
func ReadSetupStateFile(workspace, setupName, name string) ([]byte, error) {
	if !setup.ValidName(setupName) {
		return nil, &fs.PathError{Op: "open", Path: SetupStateDir(workspace, setupName), Err: fs.ErrInvalid}
	}
	r, err := openWorkspaceStateRel(workspace, "setups/"+setupName)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return ReadRegularFileBeneath(r, name)
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

// FreshHome is one fresh home a workspace holds (WorkspaceFreshHomes).
type FreshHome struct {
	// Setup is the setup it was made for, Dir the home, and Prism its capture store beside it,
	// "" when that was never made.
	Setup, Dir, Prism string
}

// WorkspaceFreshHomes lists the fresh homes workspace holds now, sorted by setup and then by
// name, which is by age. Like WorkspaceHomeStates it follows no symbolic link at `.yolo`,
// `setups`, a setup's directory or the home, and lists only real directories.
func WorkspaceFreshHomes(workspace string) []FreshHome {
	state, err := OpenStateDirRoot(WorkspaceStateDir(workspace))
	if err != nil {
		return nil
	}
	defer state.Close()
	setups, err := OpenStateSubdirRoot(state, "setups", SetupsDir(workspace))
	if err != nil {
		return nil
	}
	defer setups.Close()
	var out []FreshHome
	for _, name := range readDirNames(setups) {
		if !setup.ValidName(name) {
			continue
		}
		d, err := OpenStateSubdirRoot(setups, name, SetupStateDir(workspace, name))
		if err != nil {
			continue
		}
		for _, entry := range readDirNames(d) {
			if !IsFreshHomeName(entry) {
				continue
			}
			if fi, err := d.Lstat(entry); err != nil || !fi.IsDir() {
				continue
			}
			f := FreshHome{Setup: name, Dir: filepath.Join(SetupStateDir(workspace, name), entry)}
			prism := FreshPrismPrefix + strings.TrimPrefix(entry, FreshHomePrefix)
			if fi, err := d.Lstat(prism); err == nil && fi.IsDir() {
				f.Prism = filepath.Join(SetupStateDir(workspace, name), prism)
			}
			out = append(out, f)
		}
		d.Close()
	}
	return out
}

// readDirNames is r's entries' names, sorted; nil when it cannot be read.
func readDirNames(r *os.Root) []string {
	f, err := r.Open(".")
	if err != nil {
		return nil
	}
	names, _ := f.Readdirnames(-1)
	f.Close()
	sort.Strings(names)
	return names
}
