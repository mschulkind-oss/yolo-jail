package cli

// configrunningjail.go is the one ordering condition a host-side write to a JAIL's surfaces
// has ([OQ-CR4](docs/reference/config-target-resolution.md#oq-cr4), ruled (a) *with the
// running-jail refusal*).
//
// # What the fourth disposition is, and why it needs this
//
// Host-side there was no way to discard a jail's captured edits: the matrix had three cells,
// and the only shipped exit from a captured edit was `yolo config reset` INSIDE the owning
// jail — so discarding a stopped jail's captures required launching it, and a launch renders
// and captures first. The undo was reachable only by performing the act being undone.
//
// The store was always workspace-keyed and the surface file was only ever a resolver away
// (<workspace>/.yolo/home/… and /home/agent/… are the same inode, measured), so what the
// design added is the resolution, not a path. The hazard the host-side write guard names —
// *"these surfaces resolve against a real home"* — is true of a REAL home and false of a
// workspace's own home overlay, which is why that guard now lets this one case through.
//
// # The condition it replaces the guard with
//
// A running jail's copy of the file is LIVE: the agent inside may read it at any moment, and
// the jail captures on terminate, which would fold the host-side truncation back in as an
// edit — the discard undone by the discard. So a write refuses while a jail for that
// workspace is running, and the refusal costs nothing, because the in-jail command is
// available exactly then.
//
// # THE PROBE IS TRI-STATE, and the third answer refuses too
//
// "The runtime says no jail is running" and "the runtime could not be asked" are the same
// empty answer to a naive reader, and this repo's standing rule is that a sweeper which
// cannot ask DECLINES (AGENTS.md, the generation reaper; `yolo ps` prints a red line rather
// than "No running jails" for the same reason). A write is the case that rule is strongest
// for, so an unqueryable runtime refuses.
//
// ⚠ `--force` REACHES BOTH, and that is a decision rather than an omission (the plan's
// Blocker 4). The flag's shipped meaning is *"I really mean to write these files"*, it
// already reaches every other arm of the guard, and a host-side `reset --force` on a
// workspace target is something this CLI has always permitted — narrowing it here would
// retract a shipped hatch on the way to widening the default. The unqueryable case in
// particular MUST stay forceable: there the remedy the running refusal offers (run it inside
// the jail) may be unreachable precisely because the runtime is broken.

import (
	"fmt"
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// jailLiveness is what the runtime can say about the jail for a workspace. THREE answers,
// because collapsing the last two is the failure [P3] names: *"unknown is not empty"*.
type jailLiveness int

const (
	// jailNotRunning: the runtime answered and this workspace's jail is not among the live
	// containers. A measurement.
	jailNotRunning jailLiveness = iota
	// jailRunning: the runtime answered and it is live (running, paused or restarting —
	// runtime.ParsePodmanLive's set, the same one the prune sweep guards on).
	jailRunning
	// jailLivenessUnknown: the runtime could not be asked. Not an answer, and never read as
	// "no jail is running".
	jailLivenessUnknown
)

// jailLivenessProbe runs a runtime probe and returns (stdout, ok), with ok=false for a spawn
// error OR a non-zero exit — the tri-state `yolo ps` is built around and must not collapse.
//
// A package var because it is the one seam a test cannot supply otherwise: a bare runner has
// no container runtime, so without it every test of this refusal would exercise the
// unqueryable arm and the other two would be unreachable. It is the shape psDeps.RunCmd
// already is, one function wide because this asks one question.
var jailLivenessProbe = psRunCmd

// workspaceJailLiveness asks the runtime whether a jail for this target's workspace is live,
// and returns the container name either way so a refusal can name it.
//
// The NAME comes from runtime.FromWorkspace — the frozen naming contract a launch uses — so
// this asks about the same container `yolo` would start here rather than about a name
// derived twice. The RUNTIME comes off the target, which resolved it the way `yolo ps` does
// (env > the workspace's `runtime` key > platform probe), so a report and a launch agree
// about which backend this workspace uses.
func workspaceJailLiveness(t configTarget) (jailLiveness, string) {
	name := runtime.FromWorkspace(t.workspace)
	rt := t.runtime
	if rt == "" {
		rt = detectListingRuntime(t.workspace)
	}
	var live map[string]struct{}
	if rt == "container" {
		out, ok := jailLivenessProbe([]string{"container", "ls"})
		if !ok {
			return jailLivenessUnknown, name
		}
		live = runtime.ParseContainerLsLive(out)
	} else {
		out, ok := jailLivenessProbe([]string{rt, "ps", "-a", "--format", "{{.Names}} {{.State}}"})
		if !ok {
			return jailLivenessUnknown, name
		}
		live = runtime.ParsePodmanLive(out)
	}
	if _, running := live[name]; running {
		return jailRunning, name
	}
	return jailNotRunning, name
}

// refuseWhileJailRuns is the ordering condition itself: true when this host-side write must
// not proceed, with the refusal already printed.
//
// Both refusals name the in-jail command, because that is the remedy and it is available
// exactly when the first one fires.
func refuseWhileJailRuns(t configTarget, cmd string, errw io.Writer) bool {
	switch state, name := workspaceJailLiveness(t); state {
	case jailRunning:
		fmt.Fprintf(errw, "yolo config %s: refusing — the jail for %s is running (%s), and it "+
			"is composing and capturing these very files. A write from out here would be "+
			"folded back in as a captured edit when that jail terminates, which is the "+
			"discard undoing itself.\n"+
			"  Run `yolo config %s <agent>` INSIDE that jail — it is available exactly now — "+
			"or stop the jail. --force overrides.\n", cmd, t.workspace, name, cmd)
		return true
	case jailLivenessUnknown:
		fmt.Fprintf(errw, "yolo config %s: refusing — could not ask the container runtime "+
			"whether the jail for %s (%s) is running, and a write to a live jail's surfaces "+
			"is folded back in as a captured edit on terminate. \"Could not ask\" is not "+
			"\"nothing is running\".\n"+
			"  Run `yolo config %s <agent>` inside that jail if it is up, or re-run with "+
			"--force if you know it is down.\n", cmd, t.workspace, name, cmd)
		return true
	default:
		return false
	}
}
