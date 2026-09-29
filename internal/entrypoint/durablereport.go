package entrypoint

import (
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
)

// ReportDurableDir is the launch line for the durable dir (docs/design/durable-scratch-space.md
// §5.4, OQ-DS2): yolo never deletes a durable worktree, so every fresh launch says how many
// there are, how big they are and how long the oldest has sat idle — and, in a second line,
// how many worktree registrations point at directories that are gone, which is the /tmp
// incident class caught at the next launch.
//
// COMPUTED HERE, IN THE JAIL, on every boot, which is every fresh launch and never an attach
// (an attach runs no entrypoint). Here the registrations may be checked against the
// filesystem the agent sees, and the host never stats a path a jail-written admin file named
// (DS-D3). It never refuses or fails the boot, and the size walk stops at durable.WalkBudget.
// A launch with no durable dir exported no variable, and the launcher has already said why.
func ReportDurableDir(e *Env) { reportDurableDir(e, time.Now) }

func reportDurableDir(e *Env, now func() time.Time) {
	dir := e.Getenv(durable.EnvVar)
	if dir == "" {
		e.note("durable dir: none this launch ($" + durable.EnvVar + " unset)")
		return
	}
	ws := e.WorkspaceDir()
	// A worktree made on the host records the host's spelling of the workspace; in a
	// container jail that is YOLO_HOST_DIR, and the same directory is /workspace here.
	aliases := map[string]string{}
	if host := e.Getenv("YOLO_HOST_DIR"); host != "" && host != ws {
		aliases[host] = ws
	}
	sc := durable.ScanDir(durable.ScanOptions{Workspace: ws, Durable: dir, Aliases: aliases, CheckGone: true})
	sz, err := durable.Measure(dir, durable.WalkBudget, now)
	lines := durable.LaunchLines(sc, sz, err, durable.WalkBudget, now())
	for _, l := range lines {
		e.warn(l)
	}
	if len(lines) == 0 {
		e.note("durable dir: " + dir + " holds nothing to report")
	}
}
