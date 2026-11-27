package run

import (
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
	"github.com/mschulkind-oss/yolo-jail/internal/unifiedhome"
)

// UnifiedHomeWait is `yolo check`'s read-only plan of the merge a launch would make
// (docs/design/unified-workspace-home.md §9.4): the container and guest homes of the workspace
// (the yolo account's only once UHG-D8's gate opens) that still hold shared state a launch would move into the unified home, and whether a jail of the
// project is running, which is what makes them wait.
type UnifiedHomeWait struct {
	Homes   []string // labels ("legacy", a setup's name), in WorkspaceHomeStates order
	Running bool     // a jail of the project runs: the merge waits for `yolo stop`
	Unknown bool     // the container runtime could not be asked: read as running
}

// UnifiedHomeWaiting lists what waits to merge, with the launch's own selection of homes and of
// what stays in a slice (mergeOtherHomes, stayPaths), and the launch's question about running jails
// (projectJails). It moves, creates and writes nothing. A home counts only while it holds a regular
// file the merge would carry out (unifiedhome.Pending), so a slice a launch has already composed,
// which holds links and its own rendered files, does not wait.
func UnifiedHomeWaiting(workspace string, packs []*packload.Pack,
	lookPath func(string) (string, bool), exec func([]string, string, []string, time.Duration) ExecResult) UnifiedHomeWait {
	var wait UnifiedHomeWait
	dirs := packload.WritableDirs(packs)
	if len(dirs) == 0 {
		return wait
	}
	name, user, _ := config.SelectedSetupFromConfig()
	selected, _ := setup.Lookup(name, user)
	o := &Options{Workspace: workspace, Setup: selected, LookPath: lookPath, Exec: exec}
	stay := stayPaths(packs)
	for _, home := range paths.WorkspaceHomeStates(workspace) {
		label := homeLabel(workspace, home)
		if label != "legacy" {
			if s, err := setup.Lookup(label, user); err == nil && isYoloAccountSetup(s) && !unifiedHomeJoinsYoloAccount {
				continue // the gated account's home stays private (UHG-D8)
			}
		}
		root, err := paths.OpenWorkspaceHomeRoot(workspace, home)
		if err != nil {
			continue
		}
		pending := 0
		for _, dir := range dirs {
			pending += unifiedhome.Pending(unifiedhome.Source{Label: label, Root: root,
				Dirs: stateSpellings(dir), Stay: stay[dir]})
		}
		root.Close()
		if pending > 0 {
			wait.Homes = append(wait.Homes, label)
		}
	}
	if len(wait.Homes) > 0 {
		switch o.projectJails() {
		case projectJailsRunning:
			wait.Running = true
		case projectJailsUnknown:
			wait.Unknown = true
		}
	}
	return wait
}
