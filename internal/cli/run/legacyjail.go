package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// legacyjail.go is SUP-D10 (docs/design/named-setups-plan.md): a jail an older yolo started,
// keyed by the project key alone (legacyJailName), refuses the launch. Its name is not this
// launch's jail name (jailname.go), so nothing else in the launch would find it: the launch would
// start a second jail of the project beside it, on the home the first one still binds.
//
// It asks jailRuns, the same three ways the legacy home's move asks (legacyhome.go): the keeper of
// the name and of its macos-user and guest keys, a live session record under any of them, and a
// container of the name in each container runtime on PATH. A probe that could not be answered
// does not refuse: the move that follows reads it as running and leaves the home in place, and a
// launch is not refused on a question nobody answered.

// refuseLegacyJail refuses the launch, naming how to stop it, when a jail runs under the legacy
// jail name. A launch whose jail name IS the legacy name (a caller that resolved no setup) asks
// nothing: that jail is its own.
func (o *Options) refuseLegacyJail() bool {
	legacy := o.legacyJailName()
	if o.jailName() == legacy {
		return false
	}
	out := o.pr(o.Stderr)
	// Each pass removes at most one leftover container (one per runtime), so the passes are bounded.
	for range len(projectContainerRuntimes) + 1 {
		answer, how := o.jailRuns(legacy)
		if answer != projectJailsRunning {
			return false
		}
		if leftover := o.legacyLeftoverContainer(legacy, how); leftover {
			// A STOPPED LEFTOVER IS NOT A RUNNING JAIL (review M2): an older yolo's container that
			// exited without being removed holds the name and nothing else, as one under this
			// launch's own name would, and the launch removes it the same way (removeStaleContainer).
			if o.removeStaleContainer(legacy, how) {
				out.print("Removed the stopped jail an earlier yolo left for this workspace (" +
					richtext.Escape(legacy) + ").")
				continue
			}
			out.print("[bold red]Refusing to launch: a stopped jail an earlier yolo left for this workspace (" +
				richtext.Escape(legacy) + ") could not be removed. It holds this workspace's home.[/bold red]")
			out.print("Remove it, then launch again: `" + legacyJailRemove(legacy, how) + "`.")
			return true
		}
		out.print("[bold red]Refusing to launch: a jail an earlier yolo started for this workspace (" +
			richtext.Escape(legacy) + ") is still running. It holds this workspace's home, and this " +
			"yolo names each setup's jail apart, so it would start a second jail beside it.[/bold red]")
		out.print("Stop it, then launch again: " + legacyJailStop(legacy, how) + ".")
		return true
	}
	return false
}

// legacyLeftoverContainer reports whether what jailRuns found under the legacy name is only a
// stopped container on runtime how: no keeper of any of its keys is alive or unknown, no session
// of them is live, and the runtime answers that no container of the name runs. Any doubt is not a
// leftover.
func (o *Options) legacyLeftoverContainer(cname, how string) bool {
	if how != "podman" && how != "container" {
		return false
	}
	for _, key := range []string{cname, keeperKey(cname, keeperNotchMacosUser), keeperKey(cname, keeperNotchGuest)} {
		if probeKeeper(key) != keeperGone || len(liveKeyedSessions(key)) > 0 {
			return false
		}
	}
	id, known := o.probeRunningContainer(cname, how, trackingProbeTimeout)
	return known && id == ""
}

// legacyJailRemove is the runtime's own removal of a stopped legacy container.
func legacyJailRemove(cname, rt string) string {
	if rt == "container" {
		return "container rm " + richtext.Escape(cname)
	}
	return "podman rm " + richtext.Escape(cname)
}

// legacyJailStop is the stop a refusal names for the legacy jail, by what found it running:
// `yolo stop` from the workspace stops a jail under the legacy name (this yolo's stop asks that name
// too, and so does the yolo that started it), and a container is also stopped by its runtime's own
// command.
func legacyJailStop(cname, how string) string {
	switch how {
	case "podman":
		return "`yolo stop` from this workspace, or `podman stop " + richtext.Escape(cname) + "`"
	case "container":
		return "`yolo stop` from this workspace, or `container stop " + richtext.Escape(cname) + "`"
	}
	return "`yolo stop` from this workspace"
}
