package run

import "fmt"

// attachskew.go says out loud that an attached jail is running an OLDER yolo than
// the host launcher driving it.
//
// WHY THIS BECAME NECESSARY. Before flake-bundle generations, a `just install`
// deleted a running jail's binaries out from under it, so an old jail did not
// keep running — it broke, loudly and permanently (internal/flakebundle). Now it
// keeps working, which is the right outcome and also the one that can be silently
// wrong: the container's yolo-entrypoint is frozen at the generation it launched
// with, while the host `yolo` emitting its argv has moved on. That is exactly the
// host<->jail skew AGENTS.md warns about ("leaves the machine skewed by default,
// and it stays that way silently"), and version.SourceSkew does not cover it —
// that gate compares the BINARY to the TREE before a build, and here there is no
// build and no tree, just two halves of a contract that were shipped weeks apart.
//
// ONE DIM LINE, and never a refusal. The overwhelmingly common case is a
// maintainer who installs several times a day and whose jails are hours older
// than the launcher; refusing that, or shouting about it, would make the honest
// outcome (the jail still works) feel like a failure. What the line has to carry
// is the comparison and the remedy, because the launch banner already prints the
// baked version and it is the DIFFERENCE that is actionable.

// warnIfJailIsOlderThanTheLauncher prints the skew notice, or nothing.
//
// Silent unless it can name both halves and they actually differ: an absent baked
// version means a container older than the variable, and an "unknown" host
// version means an unstamped binary. Neither is evidence of skew, and a warning
// that fires on "I cannot tell" is one a user learns to scroll past.
func (o *Options) warnIfJailIsOlderThanTheLauncher(rt, cname, baked string) {
	host := o.yoloVersion("")
	if baked == "" || host == "" || host == "unknown" || baked == host {
		return
	}
	o.pr(o.Stderr).print(fmt.Sprintf(
		"[yellow]⚠ This jail runs yolo %s; this launcher is %s.[/yellow] "+
			"[dim]A jail keeps the binaries it launched with, so fixes and contract "+
			"changes since then are not in it. %s "+
			"(finishing its running sessions), then relaunch, to pick them up.[/dim]",
		baked, host, stopRemedy(rt, cname)))
}

// stopRemedy names the command that stops cname's jail on rt, for a message that prescribes a
// restart. `yolo stop` everywhere but Apple Container, where it is `container stop`, the stop
// restartJailForAttach runs there. It was chosen while `yolo stop` read an AC jail through a
// Go-template inspect that runtime does not take, and so said "No jail running" while the jail ran
// (G11, docs/plans/setup-support-gaps.md); `yolo stop` asks `container ls` since JL-D79
// (docs/design/jail-lifetime-last-session-wins.md), and `container stop` still ends the jail.
func stopRemedy(rt, cname string) string {
	if rt == "container" { // parity: Honored — the stop a restart runs there, which ends the jail as `yolo stop` does
		return "'container stop " + cname + "'"
	}
	return "'yolo stop' from this workspace"
}
