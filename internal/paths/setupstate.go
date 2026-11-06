package paths

import "path/filepath"

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
// successor of WorkspaceHomeState.
func SetupHomeState(workspace, name string) string {
	return filepath.Join(SetupStateDir(workspace, name), "home")
}
