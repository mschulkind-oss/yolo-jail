package run

// missingbuilds.go is THE LAUNCH'S REFUSAL OF A MISSING PATCHED BUILD (docs/design/patched-extensions.md
// PPX-D40, docs/design/patched-forks.md PF-D77; OQ-PPX3, ruled in review 2026-10-05: "if a build
// fails, of course that's fatal. we should offer solutions and workarounds when that happens for
// happy path, but it's fatal").
//
// Right after the fork-build slot, before the image step and the boot, a fresh launch with NOTHING
// TO DELIVER for a patched fork it selects, or for a patched extension a selected agent pack loads,
// refuses, whatever its command: yolo never reads the command line (HP-DIR2). "Nothing to deliver"
// is no good build on this machine once the slot is done — the first build failed, never ran its
// build line, was skipped for another build's cause, or the user's failed edit leaves none — and
// never a NEWER build that failed while a good build serves, which the slot's own lines hold as
// a warning. A patched extension is needed when its owning agent pack's list entry reaches a jail
// (packload.Fork.ListedInJail), the condition on which that pack's launchers would stop
// (patchedTreesWire's Stop, PPX-D18), which stay as the backstop for an attach and a nested launch.
//
// NOT FOR AN EXTENSION WITH A FALLBACK (docs/design/pi-extension-store-builds.md XB-D7): the agent
// installs its raw entry in the tree's place, so nothing is missing; noteTreeDeliveries says the
// fallback once, with the build's cause in plain words under it.
//
// NOT WHERE NO TREE OR FORK IS BUILT: below Apple Container's read-only floor, inside a jail, and
// for a patched fork that builds for none of the jail's platform (forkUnpublishedHere) nothing was
// built, so nothing failed; those keep their warning line (noteTreeDeliveries, and the fork's
// launcher). macos-user returns above the slot and says so itself (noteMacosUserTrees).
//
// THE REFUSAL SAYS EACH CAUSE ONCE, with every build it left without one (entrypoint.BuildCause,
// PPX-D42), then who can fix it and the ways back: what the cause names, `yolo capture <key>` to
// retry, `yolo pack series check` and `yolo pack rebase <key>` for a series that no longer applies,
// dropping the list entry or the pack, and paths.AllowMissingProgramsEnv, the bypass
// jail-notch-readiness.md JR-D3 names for the readiness act, so one variable covers both. With the
// bypass set the same text is a warning and the launch goes on.
//
// A MISSING BUILD WHOSE CAUSE IS A PATCH FAILURE (patched-forks.md PF-D81) also names that failure's
// own bypass, paths.AllowPatchFailuresEnv, and why it does not start this launch: it runs only an intact
// admitted build, and a missing build has none.
//
// An extension NO agent pack loads has nothing to stop and nothing to refuse, but the slot's act
// says nothing of a build that leaves nothing serving — a jail that stopped before its build line, a
// build that failed, a series no upstream version takes (TreeDelivery.Unsaid) — so that one is said
// here as a warning.

import (
	"slices"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// missingBuild is one patched build this launch has nothing of.
type missingBuild struct {
	// label is what it is, as the lines name it: "extension matt/pi-subagents", "fork pi-fork/pi".
	label string
	// capture is `yolo capture`'s argument for it.
	capture string
	reason  string
	cause   *entrypoint.BuildCause
	// unsaid says the build act said nothing of it, leaving it to this launch (TreeDelivery.Unsaid).
	unsaid bool
	// owner is the agent pack that loads an extension, "" for a fork.
	owner string
	// needed says the launch refuses without it.
	needed bool
	// patchFailure says its series could not be applied (PF-D81): the refusal names the patch
	// failure's own bypass beside the missing-program one.
	patchFailure bool
}

// missingGroup is the missing builds one cause left: said once, with every label.
type missingGroup struct {
	builds []missingBuild
}

// patchedBuildsHere reports whether a launch on rt builds patched forks and extensions at all: on
// the host, above Apple Container's read-only floor.
func (o *Options) patchedBuildsHere(rt string) bool {
	return !config.InJail() && o.roBindsUnsupported(rt) == ""
}

// missingBuilds are this launch's patched builds with nothing to deliver, in the order the slot ran
// them: the patched forks, then the patched extensions.
func (o *Options) missingBuilds(rt string) []missingBuild {
	if !o.patchedBuildsHere(rt) {
		return nil
	}
	var out []missingBuild
	if o.BuildForks != nil || o.BuildSlot != nil {
		for _, p := range o.forkPinned {
			if !p.Fork.Patched() {
				continue // a plain fork's failed build is the fork route's (forked-programs-as-packs.md §9)
			}
			if forkUnpublishedHere(p.Fork) != "" {
				continue // built for none of this jail's platform: no build was tried, so none failed
			}
			if d, ok := o.forkDelivered[p.Fork.Bin]; ok && d.Key == "" {
				out = append(out, missingBuild{label: "fork " + p.Fork.Key(), capture: p.Fork.CaptureArg(),
					reason: patchFailureReason(d.Reason, d.PatchFailure != nil), cause: d.Cause, unsaid: d.Unsaid,
					needed: true, patchFailure: d.PatchFailure != nil})
			}
		}
	}
	if o.BuildTrees != nil || o.BuildSlot != nil {
		for _, f := range o.patchedTrees {
			if f.Fallback != "" {
				continue // its fallback is installed in its place: noteTreeDeliveries says it, and its cause (XB-D7)
			}
			if d, ok := o.treeDelivered[f.Key()]; ok && d.Dir == "" {
				out = append(out, missingBuild{label: f.Label(), capture: f.CaptureArg(),
					reason: patchFailureReason(d.Reason, d.PatchFailure != nil), cause: d.Cause,
					unsaid: d.Unsaid, owner: f.Owner, needed: f.Owner != "" && f.ListedInJail, patchFailure: d.PatchFailure != nil})
			}
		}
	}
	return out
}

// patchFailureReason is a missing build's reason when its cause is a patch failure, whose error
// block the launch printed just above (patchfailures.go): a pointer to it, so the target and the
// patch are said once.
func patchFailureReason(reason string, patchFailure bool) string {
	if patchFailure {
		return "its patch series does not apply (the ERROR above), and no build of it is on this machine"
	}
	return reason
}

// refuseMissingBuilds is the refusal, said before the image step: true when this launch refuses.
// With the bypass set it warns instead, and a missing build no pack needs is a warning when its act
// left its cause to the launch.
func (o *Options) refuseMissingBuilds(rt string) bool {
	var needed, unneeded []missingBuild
	for _, m := range o.missingBuilds(rt) {
		switch {
		case m.needed:
			needed = append(needed, m)
		case m.cause != nil || m.unsaid:
			unneeded = append(unneeded, m)
		}
	}
	out := o.pr(o.Stderr)
	// PF-D21's Apple Container line, which the act leaves to the launch with the cause: said once,
	// after the last group that has a cause, needed or not.
	withCause := func(m missingBuild) bool { return m.cause != nil }
	acLimit := rt == "container" && (slices.ContainsFunc(needed, withCause) || slices.ContainsFunc(unneeded, withCause)) // parity: Warned — Apple Container starts no build jail beside a running jail, so the refusal or warning names the capture that builds it once the others stop (PF-D21)
	if len(unneeded) > 0 {
		out.print("[yellow]" + richtext.Escape("Warning: "+countOf(len(unneeded), "patched build")+" no selected "+
			"agent pack loads "+plural(len(unneeded), "has", "have")+" no build on this machine:") + "[/yellow]")
		o.printMissingGroups(unneeded)
	}
	if len(needed) == 0 {
		if acLimit {
			o.printAppleContainerLimit()
		}
		return false
	}
	held := o.Getenv(paths.AllowMissingProgramsEnv) != ""
	lacks := missingNeededPhrase(needed) + " " + plural(len(needed), "has", "have") + " no build on this machine"
	head := "Refusing to launch: " + lacks + "."
	if held {
		head = "Warning: " + paths.AllowMissingProgramsEnv + " is set — CONTINUING, though " + lacks + ": what loads " +
			plural(len(needed), "it", "them") + " stops in the jail, and the shell is unaffected."
		out.print("[yellow]" + richtext.Escape(head) + "[/yellow]")
	} else {
		out.print("[bold red]" + richtext.Escape(head) + "[/bold red]")
	}
	o.printMissingGroups(needed)
	if acLimit {
		o.printAppleContainerLimit()
	}
	if held {
		return false
	}
	them := "them"
	if len(needed) == 1 {
		them = "it"
	}
	if slices.ContainsFunc(needed, func(m missingBuild) bool { return m.patchFailure }) {
		// A PATCH FAILURE'S OWN BYPASS (PF-D81) runs an intact admitted build in the failed series'
		// place; a missing build has none on this machine, so the bypass is named with why it does
		// not start this launch, and the repair above is the way back.
		out.print("[dim]" + richtext.Escape("  "+paths.AllowPatchFailuresEnv+"=1 does not start this launch: it runs only an "+
			"intact admitted build in place of a series that fails to apply, and none is on this machine. "+
			"Repair the series as its error says.") + "[/dim]")
	}
	out.print("[dim]" + richtext.Escape("  To launch without "+them+" now: "+paths.AllowMissingProgramsEnv+"=1, and what "+
		"loads "+them+" stops in the jail while the shell works.") + "[/dim]")
	out.print("[dim]" + richtext.Escape("  To run without one for good: drop the list entry naming it, or its pack.") +
		"[/dim]")
	return true
}

// printAppleContainerLimit is PF-D21's line: on Apple Container a build jail does not start beside a
// running jail, the likeliest reason one stopped there.
func (o *Options) printAppleContainerLimit() {
	o.pr(o.Stderr).print("[dim]" + richtext.Escape("  On Apple Container a build jail cannot start beside a running "+
		"jail: if that is what stopped it, `yolo capture <key>` builds it once the other jails stop.") + "[/dim]")
}

// missingNeededPhrase names what the launch lacks: "3 patched extensions pack pi loads", with a
// fork's and another pack's counted beside it.
func missingNeededPhrase(needed []missingBuild) string {
	var forks int
	owners := map[string]int{}
	var order []string
	for _, m := range needed {
		if m.owner == "" {
			forks++
			continue
		}
		if owners[m.owner] == 0 {
			order = append(order, m.owner)
		}
		owners[m.owner]++
	}
	var parts []string
	if forks > 0 {
		parts = append(parts, countOf(forks, "selected patched fork"))
	}
	for _, owner := range order {
		parts = append(parts, countOf(owners[owner], "patched extension")+" pack "+owner+" loads")
	}
	return entrypoint.JoinAnd(parts)
}

// countOf is "1 thing" or "N things".
func countOf(n int, thing string) string {
	return strconv.Itoa(n) + " " + plural(n, thing, thing+"s")
}

// printMissingGroups says each cause once, with every build it left without one, then who can fix
// it; a build whose act found no cause in plain words says its reason on its own line.
func (o *Options) printMissingGroups(builds []missingBuild) {
	out := o.pr(o.Stderr)
	var groups []*missingGroup
	var series bool
	for _, m := range builds {
		if m.cause == nil {
			groups = append(groups, &missingGroup{builds: []missingBuild{m}})
			series = series || m.patchFailure || strings.Contains(m.reason, "series")
			continue
		}
		joined := false
		for _, g := range groups {
			if g.builds[0].cause != nil && g.builds[0].cause.Same(m.cause) {
				g.builds = append(g.builds, m)
				joined = true
				break
			}
		}
		if !joined {
			groups = append(groups, &missingGroup{builds: []missingBuild{m}})
		}
	}
	for _, g := range groups {
		first := g.builds[0]
		labels := make([]string, len(g.builds))
		for i, m := range g.builds {
			labels[i] = m.label
		}
		if first.cause == nil {
			// A reason that names its build already ("extension a/x's build of v1 failed …") is said as
			// it is; any other follows the build's label.
			line := first.label + ": " + first.reason
			if strings.HasPrefix(first.reason, first.label) {
				line = first.reason
			}
			out.print("  " + richtext.Escape(line))
			continue
		}
		head := missingHeadline(first.reason)
		if len(g.builds) > 1 {
			head = strings.Replace(head, "its build jail", "their build jail", 1)
		}
		out.print("  " + richtext.Escape(labelsPhrase(labels)+": "+head+colonIf(len(first.cause.Lines) > 0)))
		for _, l := range first.cause.Lines {
			out.print("      " + richtext.Escape(l))
		}
		for _, l := range first.cause.WhoFixes(missingRetry(g.builds)) {
			out.print("[dim]    " + richtext.Escape(l) + "[/dim]")
		}
		if first.cause.Log != "" && !first.cause.YoloBug {
			out.print("[dim]    " + richtext.Escape("Its whole output: "+first.cause.Log) + "[/dim]")
		}
	}
	if series {
		out.print("[dim]  " + richtext.Escape("Where a series no longer applies, `yolo pack series check` says where it "+
			"stops, and `yolo pack rebase <key>` sets up the fix.") + "[/dim]")
	}
}

// missingHeadline is a reason as a group's headline: its own words, without the act that tries
// again, which missingRetry names.
func missingHeadline(reason string) string {
	if i := strings.Index(reason, " — "); i >= 0 {
		return reason[:i]
	}
	return reason
}

// colonIf is ":" when lines follow.
func colonIf(more bool) string {
	if more {
		return ":"
	}
	return ""
}

// missingRetry is what builds a group's builds once their cause is fixed.
func missingRetry(builds []missingBuild) string {
	if len(builds) == 1 {
		return "`yolo capture " + builds[0].capture + "` builds it, or the next fresh launch does"
	}
	captures := make([]string, len(builds))
	for i, m := range builds {
		captures[i] = "`yolo capture " + m.capture + "`"
	}
	return "the next fresh launch builds them, or " + strings.Join(captures, ", ") + " now"
}

// labelsPhrase is labels as one phrase, a kind they share said once: "extensions a/x and a/y".
func labelsPhrase(labels []string) string {
	if len(labels) < 2 {
		return entrypoint.JoinAnd(labels)
	}
	kind, _, _ := strings.Cut(labels[0], " ")
	names := make([]string, len(labels))
	for i, l := range labels {
		k, name, ok := strings.Cut(l, " ")
		if !ok || k != kind {
			return entrypoint.JoinAnd(labels)
		}
		names[i] = name
	}
	return kind + "s " + entrypoint.JoinAnd(names)
}
