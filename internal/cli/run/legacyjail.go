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
	answer, how := o.jailRuns(legacy)
	if answer != projectJailsRunning {
		return false
	}
	out := o.pr(o.Stderr)
	out.print("[bold red]Refusing to launch: a jail an earlier yolo started for this workspace (" +
		richtext.Escape(legacy) + ") is still running. It holds this workspace's home, and this " +
		"yolo names each setup's jail apart, so it would start a second jail beside it.[/bold red]")
	out.print("Stop it, then launch again: " + legacyJailStop(legacy, how) + ".")
	return true
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
