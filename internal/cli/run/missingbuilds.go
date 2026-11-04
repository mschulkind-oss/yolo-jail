package run

// missingbuilds.go is THE LAUNCH'S REFUSAL OF A MISSING PATCHED BUILD (docs/reference/pi-extensions.md
// PPX-D40, docs/reference/patched-forks.md PF-D77; OQ-PPX3, ruled in review 2026-10-05: "if a build
// fails, of course that's fatal. we should offer solutions and workarounds when that happens for
// happy path, but it's fatal").
//
// Right after the fork-build slot, before the image step and the boot, a fresh launch with NOTHING
// TO DELIVER for a patched fork it selects, or for a patched extension a selected agent pack loads,
// refuses, whatever its command: yolo never reads the command line (HP-DIR2). "Nothing to deliver"
// is no good build on this machine once the slot is done — the first build failed, never ran its
// build line, was skipped for another build's cause, or the user's failed edit leaves none. A
// NEWER build that failed while a good build is here is not missing: the slot hands that failure
// beside the good build, and patchfailures.go refuses it as it does a patch failure (OQ-PPX3, the
// maintainer's ruling of 2026-10-16). A patched extension is needed when its owning agent pack's list entry reaches a jail
// (packload.Fork.ListedInJail), the condition on which that pack's launchers would stop
// (patchedTreesWire's Stop, PPX-D18), which stay as the backstop for an attach and a nested launch.
//
// NOT FOR AN EXTENSION WITH A FALLBACK (docs/design/pi-extension-store-builds.md XB-D7): the agent
// installs its raw entry in the tree's place, so nothing is missing; noteTreeDeliveries says the
// fallback once, with the build's cause in plain words under it. NOR FOR ONE
// patchrecovery.SkipExtensionsEnv covers (docs/reference/pi-extensions.md §9.1): this launch
// starts its owner without it.
//
// NOT WHERE NO TREE OR FORK IS BUILT: below Apple Container's read-only floor, inside a jail, and
// for a patched fork that builds for none of the jail's platform (forkUnpublishedHere) nothing was
// built, so nothing failed; those keep their warning line (noteTreeDeliveries, and the fork's
// launcher). macos-user runs both halves of the slot (PF-D97, pi-extensions.md §11), each built for
// darwin, so a needed build it has nothing of refuses there exactly as on podman, and a newer build that
// fails beside a good one stops it too (OQ-PPX3, PD-NF1: no silent fallback).
//
// THE REFUSAL'S SHAPE is a normal tool's (docs/reference/patched-forks.md §8.7, the maintainer's ruling of
// 2026-10-16): one ERROR line, each cause once with every build it left without one
// (entrypoint.BuildCause, PPX-D42) as terse fact lines, then `Retry:` with each build's `yolo
// capture` command, then `Start anyway:` (patchrecovery.Section) — the union of each missing build's
// Recovery list, which the advance decides (PBR-D5); a delivery without one falls back to
// paths.AllowMissingProgramsEnv for a fork and patchrecovery.SkipExtensionsEnv for an extension.
// With paths.AllowMissingProgramsEnv set the launch goes on: one Warning line and the build lines.
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
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// missingBuild is one patched build this launch has nothing of.
type missingBuild struct {
	// label is what it is, as the lines name it: "extension matt/pi-subagents", "fork pi-fork/pi".
	label string
	// key is its owner key, the value a scoped hatch names.
	key string
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
	// patchFailure says its series could not be applied (PF-D81), its error block said above.
	patchFailure bool
	// recovery is the advance's hatch list for it, nil when the advance set none.
	recovery []patchrecovery.Hatch
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
			if forkUnpublishedHere(p.Fork, rt) != "" {
				continue // built for none of this jail's platform: no build was tried, so none failed
			}
			if d, ok := o.forkDelivered[p.Fork.Bin]; ok && d.Key == "" {
				out = append(out, missingBuild{label: "fork " + p.Fork.Key(), key: p.Fork.Key(), capture: p.Fork.CaptureArg(),
					reason: patchFailureReason(d.Reason, d.PatchFailure), cause: d.Cause, unsaid: d.Unsaid,
					needed: true, patchFailure: d.PatchFailure != nil, recovery: d.Recovery})
			}
		}
	}
	if o.BuildTrees != nil || o.BuildSlot != nil {
		skip := patchrecovery.Read(o.Getenv, patchrecovery.SkipExtensionsEnv)
		for _, f := range o.patchedTrees {
			if f.Fallback != "" {
				continue // its fallback is installed in its place: noteTreeDeliveries says it, and its cause (XB-D7)
			}
			if skip.Covers(patchrecovery.Subject{Key: f.Key(), Pack: f.Pack, Name: f.Bin}) {
				continue // this launch starts its owner without it (docs/reference/pi-extensions.md §9.1)
			}
			if d, ok := o.treeDelivered[f.Key()]; ok && d.Dir == "" && !d.Skipped {
				out = append(out, missingBuild{label: f.Label(), key: f.Key(), capture: f.CaptureArg(),
					reason: patchFailureReason(d.Reason, d.PatchFailure), cause: d.Cause,
					unsaid: d.Unsaid, owner: f.Owner, needed: f.Owner != "" && f.ListedInJail, patchFailure: d.PatchFailure != nil,
					recovery: d.Recovery})
			}
		}
	}
	return out
}

// patchFailureReason is a missing build's reason when its cause is a patch failure, or a failed
// newer build (OQ-PPX3), whose error block the launch printed just above (patchfailures.go): a
// pointer to it, so the target and the patch are said once.
func patchFailureReason(reason string, pf *packsrc.PatchFailure) string {
	switch {
	case pf == nil:
		return reason
	case pf.Kind == packsrc.PatchFailureBuild:
		return "its newer build failed (the ERROR above)"
	}
	return "its patch series does not apply (the ERROR above)"
}

// refuseMissingBuilds is the refusal, said before the image step: true when this launch refuses.
// With the bypass set it warns instead, and a missing build no pack needs is a warning when its act
// left its cause to the launch.
func (o *Options) refuseMissingBuilds(rt string) bool {
	var needed, unneeded []missingBuild
	// A needed build whose cause is a patch failure was refused above, by its own error block and
	// hatch list (patchfailures.go): one block and one list, not a second ERROR for it here — unless
	// the missing-program hatch starts the launch without it, which the warning below says.
	refusedAbove := false
	starting := o.Getenv(paths.AllowMissingProgramsEnv) != ""
	for _, m := range o.missingBuilds(rt) {
		switch {
		case m.needed && m.patchFailure && !starting:
			refusedAbove = true
		case m.needed:
			needed = append(needed, m)
		case m.cause != nil || m.unsaid:
			unneeded = append(unneeded, m)
		}
	}
	out := o.pr(o.Stderr)
	if len(needed)+len(unneeded) > 0 && (refusedAbove ||
		slices.ContainsFunc(append(slices.Clone(needed), unneeded...), func(m missingBuild) bool { return m.patchFailure })) {
		out.print("") // set off from the error block above (patchfailures.go)
	}
	// PF-D21's Apple Container line, which the act leaves to the launch with the cause: said once,
	// after the last group that has a cause, needed or not.
	withCause := func(m missingBuild) bool { return m.cause != nil }
	acLimit := rt == "container" && (slices.ContainsFunc(needed, withCause) || slices.ContainsFunc(unneeded, withCause)) // parity: Warned — Apple Container starts no build jail beside a running jail, so the refusal or warning names the capture that builds it once the others stop (PF-D21)
	if len(unneeded) > 0 {
		out.print("[yellow]" + richtext.Escape("Warning: "+countOf(len(unneeded), "patched build")+" no selected "+
			"agent pack loads "+plural(len(unneeded), "has", "have")+" no build on this machine") + "[/yellow]")
		o.printMissingGroups(unneeded, true)
	}
	if len(needed) == 0 {
		if acLimit {
			o.printAppleContainerLimit()
		}
		return refusedAbove
	}
	lacks := missingNeededPhrase(needed) + " " + plural(len(needed), "has", "have") + " no build on this machine"
	if o.Getenv(paths.AllowMissingProgramsEnv) != "" {
		out.print("[yellow]" + richtext.Escape("Warning: "+lacks+"; starting without "+plural(len(needed), "it", "them")+
			" ("+paths.AllowMissingProgramsEnv+")") + "[/yellow]")
		o.printMissingGroups(needed, true)
		if acLimit {
			o.printAppleContainerLimit()
		}
		return false
	}
	out.print("[bold red]" + richtext.Escape("ERROR: "+lacks) + "[/bold red]")
	o.printMissingGroups(needed, false)
	if acLimit {
		o.printAppleContainerLimit()
	}
	out.print("")
	out.print("[bold]Retry:[/bold]")
	for _, m := range needed {
		out.print("  [bold cyan]" + richtext.Escape("yolo capture "+m.capture) + "[/bold cyan]")
	}
	_, _ = out.rt.W.Write([]byte(patchrecovery.Section(patchrecovery.StartAnyway,
		o.withContainerBackends(rt, missingRecovery(needed)), out.rt.Color)))
	return true
}

// withContainerBackends is hatches with, on macos-user, the two container backends a launch can
// take instead (docs/reference/patched-forks.md PF-D97): `YOLO_SETUP=apple-container-self yolo`
// and `YOLO_SETUP=podman-self yolo`, each of which builds the fork or extension for linux in its
// own build jail. They stand before the missing-program hatch, the ruling's order, or last when
// the list has none. Every other runtime keeps hatches as they are.
func (o *Options) withContainerBackends(rt string, hatches []patchrecovery.Hatch) []patchrecovery.Hatch {
	if rt != "macos-user" { // parity: HonoredBy — only macos-user builds a patched fork or extension in its own act; the container backends' refusal needs no other backend to finish
		return hatches
	}
	backends := []patchrecovery.Hatch{
		{Env: setup.EnvVar, Value: "apple-container-self", Command: "yolo", Label: "launch on Apple Container"},
		{Env: setup.EnvVar, Value: "podman-self", Command: "yolo", Label: "launch on podman"},
	}
	i := slices.IndexFunc(hatches, func(h patchrecovery.Hatch) bool { return h.Env == patchrecovery.AllowMissingProgramsEnv })
	if i < 0 {
		i = len(hatches)
	}
	return slices.Insert(slices.Clone(hatches), i, backends...)
}

// missingRecovery is the union of the needed builds' hatch lists, in patchrecovery.Scoped's order
// then the missing-program hatch: each build's Recovery, or for one with none,
// paths.AllowMissingProgramsEnv for a fork and patchrecovery.SkipExtensionsEnv for an extension. A
// hatch two builds share is listed once, its names joined in one value.
func missingRecovery(needed []missingBuild) []patchrecovery.Hatch {
	var all []patchrecovery.Hatch
	for _, m := range needed {
		switch {
		case len(m.recovery) > 0:
			all = append(all, m.recovery...)
		case m.owner == "":
			all = append(all, patchrecovery.Hatch{Env: patchrecovery.AllowMissingProgramsEnv, Value: "1", Command: "yolo",
				Label: "start without it"})
		default:
			all = append(all, patchrecovery.Hatch{Env: patchrecovery.SkipExtensionsEnv, Value: m.key, Command: "yolo",
				Label: "start " + m.owner + " without it"})
		}
	}
	order := append(slices.Clone(patchrecovery.Scoped), patchrecovery.AllowMissingProgramsEnv)
	rank := func(env string) int {
		if i := slices.Index(order, env); i >= 0 {
			return i
		}
		return len(order)
	}
	var out []patchrecovery.Hatch
	for _, h := range all {
		i := slices.IndexFunc(out, func(o patchrecovery.Hatch) bool { return o.Env == h.Env && o.Command == h.Command })
		if i < 0 {
			out = append(out, h)
			continue
		}
		cur := &out[i]
		switch {
		case cur.Value == "1" || h.Value == "1":
			cur.Value = "1"
		case !slices.Contains(strings.Split(cur.Value, ","), h.Value):
			cur.Value += "," + h.Value
		}
		if cur.Label != h.Label {
			cur.Label = mergedHatchLabels[cur.Env]
		}
		// One label covering several builds speaks of them: "start pi without them".
		if lead, ok := strings.CutSuffix(cur.Label, " without it"); ok {
			cur.Label = lead + " without them"
		}
	}
	slices.SortStableFunc(out, func(a, b patchrecovery.Hatch) int { return rank(a.Env) - rank(b.Env) })
	return out
}

// mergedHatchLabels label one hatch line that covers several builds whose own labels differ.
var mergedHatchLabels = map[string]string{
	patchrecovery.AllowPatchFailuresEnv:   "run their admitted builds",
	patchrecovery.UseCachedGoodEnv:        "run their older builds",
	patchrecovery.AllowOlderUpstreamEnv:   "build on older releases",
	patchrecovery.SkipPatchesEnv:          "run upstream without the patches",
	patchrecovery.SkipExtensionsEnv:       "start without them",
	patchrecovery.AllowMissingProgramsEnv: "start without them",
}

// printAppleContainerLimit is PF-D21's line: on Apple Container a build jail does not start beside a
// running jail, the likeliest reason one stopped there.
func (o *Options) printAppleContainerLimit() {
	o.pr(o.Stderr).print(richtext.Escape("  Apple Container: a build jail cannot start beside a running jail; " +
		"stop the other jails, then retry"))
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
// it; a build whose act found no cause in plain words says its reason on its own line. inline says
// no Retry list follows, so the fix line names the capture itself.
func (o *Options) printMissingGroups(builds []missingBuild, inline bool) {
	out := o.pr(o.Stderr)
	var groups []*missingGroup
	var series bool
	for _, m := range builds {
		// A series no longer applying is repaired by the recipe; its own block printed it above.
		series = series || !m.patchFailure && m.cause == nil && strings.Contains(m.reason, "series")
		if m.cause == nil {
			groups = append(groups, &missingGroup{builds: []missingBuild{m}})
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
			reason := missingHeadline(first.reason)
			line := first.label + ": " + reason
			if strings.HasPrefix(reason, first.label) {
				line = reason
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
		retry := "retry"
		if inline {
			retry = missingRetry(g.builds)
		}
		for _, l := range first.cause.WhoFixes(retry) {
			out.print("    " + richtext.Escape(l))
		}
		if first.cause.Log != "" && !first.cause.YoloBug {
			out.print("    " + richtext.Escape("Log: "+first.cause.Log))
		}
	}
	if series {
		out.print("  " + richtext.Escape("Rebase: yolo pack series check"))
	}
}

// missingHeadline is a reason as a group's headline: its own words, without the act that tries
// again, which the Retry list or missingRetry names. Cut where entrypoint.CutReason cuts, never inside
// the parentheses that carry a failed build's error and its log's path.
func missingHeadline(reason string) string {
	head, _, _ := entrypoint.CutReason(reason)
	return head
}

// colonIf is ":" when lines follow.
func colonIf(more bool) string {
	if more {
		return ":"
	}
	return ""
}

// missingRetry is the commands that build a group's builds once their cause is fixed.
func missingRetry(builds []missingBuild) string {
	captures := make([]string, len(builds))
	for i, m := range builds {
		captures[i] = "`yolo capture " + m.capture + "`"
	}
	return entrypoint.JoinAnd(captures)
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
