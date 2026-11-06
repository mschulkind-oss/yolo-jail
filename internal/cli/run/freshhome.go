package run

// freshhome.go is `yolo --fresh-home` (docs/design/workspace-state-safety.md §3.6, SD-D14): a
// launch of the selected setup on a new, empty home at
// <workspace>/.yolo/setups/<setup>/home.fresh-<stamp> (paths.NewFreshHome), bound where the
// setup's home would be. The workspace's usual home, its legacy home and their captured edits are
// never read, moved or written by such a launch. The fresh home is kept after the jail stops;
// `yolo stores` lists it with the `rm -r` that deletes it, and no reaper touches it.
//
// THE CALL SITE is Run, in place of selectHome: every reader of the launch's home asks o.homeDir(),
// so choosing the home here is the whole switch, on every backend that binds a home (podman, Apple
// Container, macos-user). TestFreshHomeLaunchBindsANewEmptyHome fails if the call is deleted.
//
// AN ATTACH NEVER SWITCHES HOMES. A jail that is running already has the home it booted on, and a
// launch of its setup would join it there, so --fresh-home is refused while this launch's own
// jail runs (o.jailRuns of its setup-qualified name, the probe the legacy-home move trusts),
// naming `yolo stop`; one the runtime could not ask about is refused too, since it may run.
// Another setup's jail is never joined, so it does not refuse (named-setups-plan.md SUP-D45); a
// jail under the legacy name is refused before this, by refuseLegacyJail. The check is made under
// this jail's launch lock, and attachExisting refuses the flag again, for the jail a concurrent
// launch started after it. The other way round, a plain launch that attaches to a jail running on
// a fresh home says so, and delivers into the jail's home rather than the usual one
// (adoptRunningFreshHome).
//
// `yolo host` has no jail home to replace: the flag is refused there by name (cli's
// jailOnlyRunFlags), with the jail launch named as the next step.

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// FreshHomeFlag is the flag's spelling, which every refusal and disclosure names.
const FreshHomeFlag = "--fresh-home"

// selectFreshHome is the --fresh-home half of the home selection: it refuses while a jail of the
// project runs, makes the new home (a dry run only names the one it would make), records it as the
// home this launch binds (o.homeState) and discloses it. false means the launch is refused and the
// refusal is printed.
func (o *Options) selectFreshHome() bool {
	out := o.pr(o.Stderr)
	o.holdLaunchLock(o.jailName())
	switch j, _ := o.jailRuns(o.jailName()); j {
	case projectJailsRunning:
		o.refuseFreshHomeWhileRunning()
		return false
	case projectJailsUnknown:
		out.print("[bold red]Refusing " + FreshHomeFlag + ": yolo could not ask whether a jail of this " +
			"workspace is running, and a launch beside one would join it on the home it already uses." +
			"[/bold red]")
		out.print("Run `yolo stop` in this workspace to end any jail that is running, then run " +
			"`yolo " + FreshHomeFlag + "` again.")
		return false
	}
	if o.DryRun {
		o.homeState = filepath.Join(paths.SetupStateDir(o.Workspace, o.Setup.Name), paths.FreshHomeName(o.Now()))
		out.print("[bold]Fresh home:[/bold] a launch would run on a new, empty home at " +
			richtext.Escape(o.homeState) + "; the dry run made nothing.")
		return true
	}
	home, err := paths.NewFreshHome(o.Workspace, o.Setup.Name, o.Now())
	if err != nil {
		out.print("[bold red]Refusing " + FreshHomeFlag + ": could not make the new home (" +
			richtext.Escape(err.Error()) + ").[/bold red]")
		out.print("Nothing was changed. Run `yolo check` to see what is wrong with this workspace's " +
			"`.yolo`, then run `yolo " + FreshHomeFlag + "` again.")
		return false
	}
	o.homeState = home
	o.logRecord("decision: fresh home %s (--fresh-home)", home)
	out.print("[bold]Fresh home:[/bold] this launch runs on a new, empty home at " +
		richtext.Escape(home) + ". The workspace's usual home is not used or changed.")
	out.print("[dim]The new home is kept after the jail stops; `yolo stores` lists it with the " +
		"command that deletes it.[/dim]")
	return true
}

// refuseFreshHomeWhileRunning prints the refusal of --fresh-home in a workspace whose jail runs.
func (o *Options) refuseFreshHomeWhileRunning() {
	out := o.pr(o.Stderr)
	out.print("[bold red]Refusing " + FreshHomeFlag + ": a jail of this workspace is running, and a " +
		"launch here joins it on the home it already uses.[/bold red]")
	out.print("Stop it with `yolo stop`, then run `yolo " + FreshHomeFlag + "` again.")
}

// adoptRunningFreshHome is an attach's half: when the running jail booted on a fresh home (the
// home its environment names, paths.HomeStateEnv, read from the container's inspected env), this
// entry uses that home for everything it delivers, and says the jail runs on it. A jail on the
// usual home changes nothing.
func (o *Options) adoptRunningFreshHome(envLines []string, envKnown bool) {
	if !envKnown {
		return
	}
	rel := ""
	for _, line := range envLines {
		if v, ok := strings.CutPrefix(line, paths.HomeStateEnv+"="); ok {
			rel = strings.TrimSpace(v)
		}
	}
	if !paths.IsFreshHomeRel(rel) {
		return
	}
	home := filepath.Join(paths.WorkspaceStateDir(o.Workspace), filepath.FromSlash(rel))
	o.homeState = home
	o.pr(o.Stderr).print("[bold]Fresh home:[/bold] this jail runs on the new home " +
		richtext.Escape(home) + ", which a `yolo " + FreshHomeFlag + "` launch made, not on the " +
		"workspace's usual home. To use the usual home, run `yolo stop`, then `yolo`.")
}
