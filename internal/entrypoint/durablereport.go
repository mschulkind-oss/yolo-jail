package entrypoint

import (
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ReportDurableDir is the launch line for the durable dir (docs/design/durable-scratch-space.md
// §5.4, OQ-DS2): yolo never deletes a durable worktree, so every fresh launch says how many
// there are, how big they are and how long the oldest has sat idle — and, in a second line,
// how many worktree registrations point at directories that are gone, which is the /tmp
// incident class caught at the next launch.
//
// COMPUTED HERE, IN THE JAIL, once per fresh launch: by the container's main process as it
// boots, and by the macos-user bootstrap, which has no attach. An attach DOES run the
// entrypoint, as every session of a hold-main jail does, the first included, and its pass
// skips this step (the table's notSessionPass), since the launch already said it and the walk
// was every pass's largest step (DS-D11). Here the registrations may be checked against the
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
	// container jail that is YOLO_HOST_DIR, and the same directory is /workspace here. One made
	// in a container jail records /workspace/…, which on macos-user is the real path.
	aliases := map[string]string{"/workspace": ws}
	if host := e.Getenv("YOLO_HOST_DIR"); host != "" && host != ws {
		aliases[host] = ws
	}
	// A registration is judged gone only below the workspace or the per-launch set, which is
	// where a restart deletes trees: one the host made beside the repository is merely
	// invisible from a container jail, and calling it gone at every launch was a false alarm.
	sc := durable.ScanDir(durable.ScanOptions{Workspace: ws, Aliases: aliases,
		GoneRoots: append([]string{ws}, paths.ScratchDests()...)})
	sz, err := durable.Measure(ws, durable.WalkBudget, now)
	lines := durable.LaunchLines(sc, sz, err, durable.WalkBudget, now())
	for _, l := range lines {
		e.warn(l)
	}
	if len(lines) == 0 {
		e.note("durable dir: " + dir + " holds nothing to report")
	}
}
