package paths

import (
	"os"
	"path/filepath"
)

// UnifiedHomeName is the directory under <workspace>/.yolo that holds the state every container
// setup of the workspace shares (docs/design/unified-workspace-home.md, UH-D1).
const UnifiedHomeName = "unified-home"

// UnifiedHome is <workspace>/.yolo/unified-home, the workspace's unified home, and the only
// function that names it. It mirrors the in-jail path (the jail sees it at
// /workspace/.yolo/unified-home), dotted like a home, and it is deliberately NOT one of the
// workspace's homes (HomeStateRel, WorkspaceHomeStates): no reaper walks it as a home.
func UnifiedHome(workspace string) string {
	return filepath.Join(WorkspaceStateDir(workspace), UnifiedHomeName)
}

// OpenUnifiedHomeRoot opens the existing unified home as an os.Root, refusing a symbolic link at
// `.yolo` or at the unified home with a *LinkedStateDirError naming it. It creates nothing.
func OpenUnifiedHomeRoot(workspace string) (*os.Root, error) {
	return OpenWorkspaceStateRel(workspace, UnifiedHomeName)
}
