package run

// patchfailures.go is A JAIL LAUNCH'S PATCH FAILURE (docs/design/patched-forks.md §8, PF-D81, PF-D83):
// as soon as the fork-build slot ends, before any line the launch prints about what the slot
// delivered and before the image step and the boot, the launch prints the error block of each selected patched fork
// and patched extension whose series does not apply — the slot's advances hand the typed failure and
// print no block of their own (cli's advance.launchSaysPatchFailure), so the block is said once, here,
// where no build's progress follows it — and then decides:
//
//   - WITH AN INTACT ADMITTED BUILD TO RUN (the slot handed one beside the failure), the launch
//     REFUSES, whatever the command: a serving older build does not turn a patch failure into a
//     warning (PF-D81). paths.AllowPatchFailuresEnv=1 is the bypass: the launch runs that build for
//     this one launch, saying so in a CONTINUING line under the block. paths.AllowMissingProgramsEnv
//     does not waive it, since leaving the program out is not what the block offers when a build of
//     it is here (PF-D83).
//   - WITH NOTHING TO RUN, the build is missing, and missingbuilds.go's refusal follows, which
//     paths.AllowMissingProgramsEnv waives by starting without it (PF-D77, PF-D83); the block's
//     Bypass line names that, or cached-good recovery (PF-D82) when an older recorded good build of a
//     fork is here.
//   - A PATCHED EXTENSION WITH A FALLBACK and no build gets its fallback, as XB-D7 rules, with the
//     failure as the fallback line's reason (noteTreeDeliveries); one no selected agent pack loads in
//     a jail stops nothing, its block saying so.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// launchPatchFailure is one patched build whose series this launch found does not apply.
type launchPatchFailure struct {
	label, owner string
	failure      *packsrc.PatchFailure
	bypass       packsrc.PatchBypass
}

// launchPatchFailures are this launch's patch failures, in the order the slot ran them: the patched
// forks, then the patched extensions.
func (o *Options) launchPatchFailures(rt string) []launchPatchFailure {
	// EVERY RUNTIME THE SLOT RAN ON, not only those that build: below Apple Container's read-only
	// floor the tree arm builds nothing yet still hands a good build with its recorded failure, and
	// its advance printed no block (cli's advance.launchSaysPatchFailure). Only a jail runs no slot.
	if config.InJail() {
		return nil
	}
	var out []launchPatchFailure
	for _, p := range o.forkPinned {
		if !p.Fork.Patched() || forkUnpublishedHere(p.Fork) != "" {
			continue
		}
		d, ok := o.forkDelivered[p.Fork.Bin]
		if !ok || d.PatchFailure == nil {
			continue
		}
		out = append(out, launchPatchFailure{label: "fork " + p.Fork.Key(), owner: p.Fork.Key(), failure: d.PatchFailure,
			bypass: forkPatchBypass(p.Fork.Key(), d)})
	}
	for _, f := range o.patchedTrees {
		d, ok := o.treeDelivered[f.Key()]
		if !ok || d.PatchFailure == nil || d.Dir == "" && f.Fallback != "" {
			continue
		}
		b := packsrc.PatchBypass{Command: "yolo"}
		switch {
		case f.Owner == "" || !f.ListedInJail:
			b.Unneeded = true
		case d.Dir != "":
			b.Runs = d.label()
		default:
			b.Missing = true
		}
		out = append(out, launchPatchFailure{label: f.Label(), owner: f.Key(), failure: d.PatchFailure, bypass: b})
	}
	return out
}

// forkPatchBypass is a patched fork's Bypass line at a jail launch (PF-D83): the build the slot
// handed, else the older recorded good build cached-good recovery runs, else leaving it out.
func forkPatchBypass(owner string, d entrypoint.ForkDelivery) packsrc.PatchBypass {
	b := packsrc.PatchBypass{Command: "yolo"}
	switch {
	case d.Key != "":
		b.Runs = d.Runs
		if b.Runs == "" {
			b.Runs = "in store entry " + d.Key
		}
	case d.CachedGood != "":
		b.CachedGood, b.CachedGoodLabel = owner, d.CachedGood
	default:
		b.Missing = true
	}
	return b
}

// refusePatchFailures prints every patch failure's error block, then refuses the launch when a
// failure has a build to run and the bypass is not set: true when this launch refuses. A failure
// with nothing to run is left to refuseMissingBuilds, after it.
func (o *Options) refusePatchFailures(rt string) bool {
	failures := o.launchPatchFailures(rt)
	if len(failures) == 0 {
		return false
	}
	out := o.pr(o.Stderr)
	bypassed := o.Getenv(paths.AllowPatchFailuresEnv) == "1"
	var blocking []launchPatchFailure
	for _, f := range failures {
		lines := strings.Split(strings.TrimSuffix(f.failure.Block(f.label, f.owner, f.bypass), "\n"), "\n")
		out.print("[bold red]" + richtext.Escape(lines[0]) + "[/bold red]")
		for _, l := range lines[1:] {
			out.print(richtext.Escape(l))
		}
		if f.bypass.Runs == "" || f.bypass.Unneeded {
			continue
		}
		if bypassed {
			out.print("[yellow]" + richtext.Escape("CONTINUING: "+paths.AllowPatchFailuresEnv+"=1 is set — this launch runs "+
				"the intact admitted build "+f.bypass.Runs+" of "+f.label+" in place of the series that does not apply; "+
				"the series is not repaired, and the next launch without the variable stops here again.") + "[/yellow]")
			continue
		}
		blocking = append(blocking, f)
	}
	if len(blocking) == 0 {
		return false
	}
	labels := make([]string, len(blocking))
	for i, f := range blocking {
		labels[i] = f.label
	}
	out.print("[bold red]" + richtext.Escape("Refusing to launch: the patch series of "+entrypoint.JoinAnd(labels)+" "+
		plural(len(blocking), "does", "do")+" not apply (the ERROR above), and a launch does not run an older build "+
		"in its place unless asked.") + "[/bold red]")
	out.print(richtext.Escape("  Repair: as the ERROR's Repair line says; the next fresh launch builds the repaired series."))
	out.print(richtext.Escape("  Bypass: " + paths.AllowPatchFailuresEnv + "=1 yolo — runs the intact admitted " +
		plural(len(blocking), "build", "builds") + " named above for this one launch."))
	if o.Getenv(paths.AllowMissingProgramsEnv) != "" {
		out.print("[dim]" + richtext.Escape("  "+paths.AllowMissingProgramsEnv+" does not leave "+
			plural(len(blocking), "it", "them")+" out: an intact build of "+plural(len(blocking), "it", "each")+
			" is on this machine, and only "+paths.AllowPatchFailuresEnv+"=1 runs it.") + "[/dim]")
	}
	return true
}
