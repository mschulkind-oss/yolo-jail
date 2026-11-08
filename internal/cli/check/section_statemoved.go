package check

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// stateMovedFindings is the running jails' identity-stamp check for a workspace
// (run.StateMovedFindings); a seam so a test can name a mismatch without a running keeper.
var stateMovedFindings = run.StateMovedFindings

// sectionStateMoved reports a running jail whose `.yolo` is not the directory it started on
// (docs/design/workspace-state-safety.md §3.4, SD-D5): a warning naming the step that keeps the
// agents' homes. It prints nothing when no running jail of the workspace has moved state, which is
// every check of a healthy workspace, and every check inside a jail, which holds no keeper records.
func (o *Options) sectionStateMoved(r *reporter, workspace string) {
	lines := stateMovedFindings(workspace)
	if len(lines) == 0 {
		return
	}
	r.sectionHeader("Workspace state")
	r.warn(lines[0], strings.Join(lines[1:], "\n"))
	r.blank()
}
