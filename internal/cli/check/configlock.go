package check

import (
	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// writableReal is Options.Writable's default: access(2) with W_OK, as the process's own uid.
func writableReal(path string) bool { return unix.Access(path, unix.W_OK) == nil }

// reportWorkspaceConfigLock says, inside a jail, that the workspace config cannot be written from
// here, before the agent tries. A launch with any `workspace_readonly` entry locks that file
// (run.workspaceConfigLockTarget, macosuser.workspaceReadonlyRels) so an agent cannot edit its own
// protection out, and the error it would otherwise meet is a bare EROFS from its editor.
//
// It MEASURES rather than predicting from the config: what the agent will hit is the mount, and an
// Apple Container below the read-only floor binds the file writable whatever the config says. Not a
// warning — the lock is the configuration working — so a dim line and the next step.
//
// It names no way round the lock (an unlocked `yolo-jail.local.jsonc`), for the reason the
// briefing's lockedConfigRequest gives.
func (o *Options) reportWorkspaceConfigLock(r *reporter, workspace string) {
	if !o.inJail() {
		return
	}
	path, name := config.ResolveWorkspaceConfigPath(workspace, config.WorkspaceConfigName)
	if !o.PathExists(path) || o.Writable(path) {
		return
	}
	r.dim(name + " is read-only in this jail (`workspace_readonly` locks it): you cannot edit it here")
	r.note("Write out the edit and ask the human to apply it on the host, run `yolo check` there, and restart the jail.")
}
