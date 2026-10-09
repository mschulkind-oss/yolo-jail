package cli

// treedelivery.go is run.Options.BuildTrees: a fresh launch's TREE ARM for its PATCHED EXTENSIONS
// (docs/design/patched-extensions.md §6.1, §8.1, §9; PPX-D7). For each one: its advance — the check,
// the replay down the walk's list and the build of the newest fit, through the one implementation
// patched forks use (patchedadvance.go, keyed by the extension key) — and then a PER-LAUNCH COPY of
// the good build's tree beside the launch's pack tree, which the `files` emitter mounts read-only.
//
// THE COPY IS CHECKED AGAINST THE STORE ENTRY'S COMPLETION MARKER once it ends (§8.1). A move reaps
// every other build of the key at once, marker first (capture.Store.ReapEntry), because no jail
// reads the store for a tree; so a copy another workspace's move cut short is told apart from a
// whole one by the marker alone, even when the copy reported no error. A copy whose marker is gone
// is removed, and the record re-read once, which here is the advance run again: it finds the good
// build the move left, or rebuilds the one that went.
//
// BELOW APPLE CONTAINER'S READ-ONLY FLOOR nothing is checked or built (the request's Build is
// false), and a good build already on this machine is still copied (§11).

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// errTreeEntryGone is a per-launch copy whose store entry was reaped before it began or while it ran.
var errTreeEntryGone = errors.New("its capture store entry was reaped")

// treeAdvance runs a patched extension's advance: a var so a test can stand in for the advance.
var treeAdvance = advancePatchedFork

// treeCopied runs once a per-launch copy of entry key has ended, before its completion marker is
// checked: a seam for a test to reap the entry there, as another workspace's move would.
var treeCopied = func(key string) {}

// treeFailureAfterCopy rereads the owning record through CheckRecord.CurrentPatchFailure. A reaped
// copy does not erase an advance's local evidence: only a newer, successful check bound to this
// owner and these inputs may establish that the retained failure is irrelevant.
func treeFailureAfterCopy(f packload.Fork, previous *packsrc.PatchFailure) (*packsrc.PatchFailure, bool) {
	if previous == nil {
		return nil, false
	}
	series, err := f.ReadSeries()
	if err != nil {
		return nil, false
	}
	inputs, _, _, err := f.CheckWant(series).Inputs()
	if err != nil {
		return nil, false
	}
	record, err := patchedAdvanceStore(true).LoadCheckRecord(f.Key())
	if err != nil {
		return nil, false
	}
	if current := record.CurrentPatchFailure(inputs, series.Digest); current != nil {
		return current, false
	}
	if previous.Owner != f.Key() || record.Owner != f.Key() || record.Read != inputs || record.Check == nil ||
		record.Seq <= previous.Seq || record.Check.Seq != record.Seq || record.Check.FetchErr != "" || record.Check.Problem != "" {
		return previous, false
	}
	assessment := record
	assessment.PatchFailure = previous
	if current := assessment.CurrentPatchFailure(inputs, series.Digest); current != nil {
		return current, false
	}
	return nil, true
}

// deliverTreesForLaunch is run.Options.BuildTrees.
//
// Every extension runs at once in the slot's pool (buildpool.go, XB-D10), which prints on errw; a
// launch with BuildSlot wired runs them there beside the forks.
func deliverTreesForLaunch(req run.TreeBuildRequest, _, errw io.Writer, color bool) map[string]run.TreeDelivery {
	_, trees := runBuildSlot(run.BuildSlotRequest{Trees: &req}, errw, color)
	return trees
}

// updateTiming is when a key's advance runs (docs/design/pi-extension-store-builds.md §7.6): AT THE
// LAUNCH, which waits for it, bounded — the default — or FOR THE NEXT LAUNCH, in a background advance
// that leaves this launch on the build it already has (XB-D19, XB-D28).
type updateTiming int

const (
	timingAtLaunch updateTiming = iota
	timingNextLaunch
)

// treeUpdateTiming is when f updates, from `agent_updates` (XB-D17, XB-D18): a tree it holds updates
// at the launch, which checks nothing for it; otherwise its owning agent pack's own entry, then its
// contributing pack's, then "*", then a top-level value (config.PackTimingDecision). A var so a test
// can stand a timing in.
var treeUpdateTiming = func(f packload.Fork) updateTiming {
	if run.PatchedForkHold(f) != "" {
		return timingAtLaunch
	}
	packs := []string{f.Pack}
	if f.Owner != "" {
		packs = []string{f.Owner, f.Pack}
	}
	if config.PackTimingDecision(config.AgentUpdatesWire(), packs...) == config.AgentUpdatesNextLaunch {
		return timingNextLaunch
	}
	return timingAtLaunch
}

// runtimeName is a runtime as a line names it.
func runtimeName(rt string) string {
	switch rt {
	case "container":
		return "Apple Container"
	case "":
		return "this runtime"
	}
	return rt
}

// deliverTree is one built tree's delivery, the pool's key it: what serves, then its per-launch copy,
// with the one re-read a reaped entry gets; and whether its advance waits for a background one
// (later).
//
// FOR THE NEXT LAUNCH (treeUpdateTiming, XB-D19): a key with a good build is handed it with no check,
// one with none but a fallback takes the fallback this once, and only a key with neither builds in
// front, since there is nothing to hand; the first two are left to the background advance.
func deliverTree(f packload.Fork, req run.TreeBuildRequest, report *buildReport, it *poolItem,
	color bool) (_ run.TreeDelivery, later bool) {
	errw := it.stream()
	pr := richtext.Printer{W: errw, Color: color}
	// THE LAST BACKGROUND ADVANCE'S OUTCOME, said once at whichever launch reads it first (XB-D20),
	// whatever this launch's timing.
	again := noteBackgroundOutcome(f, pr)
	o := advanceOptions{platform: req.Platform, runtime: req.Runtime, workspace: req.Workspace, out: errw,
		errw: errw, color: color, launch: true, act: req.Interrupt, report: report, slot: it}
	nextLaunch := false
	if req.Build && treeUpdateTiming(f) == timingNextLaunch {
		nextLaunch = run.BackgroundBuildsOn(req.Runtime)
		if !nextLaunch {
			// THIS RUNTIME RUNS NO BACKGROUND ADVANCE (Apple Container, macos-user): the key updates at
			// this launch, as XB-D21 has the next-launch option do there whenever a build happens.
			pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf("%s: `agent_updates` asks for the next launch, "+
				"and on %s this yolo runs no background advance — it updates at this launch", f.Label(),
				runtimeName(req.Runtime))))
		}
	}
	var patchFailure *packsrc.PatchFailure
	for attempt := 0; ; attempt++ {
		var r advanceResult
		if !req.Build {
			r = servingTree(f, o, req.BuildFloor)
		} else {
			switch {
			case nextLaunch:
				var a *advance
				if r, a = servingTreeOf(f, o, "", true); r.delivery.Key != "" {
					if r.delivery.PatchFailure == nil {
						// THE SPAWN CONDITION (XB-D19): its check due, a candidate pending, or the last
						// background advance unfinished; a key with nothing to do starts none.
						later = again || a.wantsBackground()
					}
					break
				}
				if r.delivery.PatchFailure != nil {
					return run.TreeDelivery{Reason: r.delivery.Reason, PatchFailure: r.delivery.PatchFailure}, false
				}
				if f.Fallback != "" {
					return run.TreeDelivery{Reason: f.Label() + " has no build on this machine yet, and updates for " +
						"the next launch — a background advance builds it"}, true
				}
				r = treeAdvance(f, o)
			case currentPatchFailure(f) != nil:
				r = treeAdvance(f, o)
			default:
				r = treeAdvance(f, o)
			}
		}
		if r.delivery.PatchFailure != nil {
			patchFailure = r.delivery.PatchFailure
		}
		if attempt > 0 && req.Build && patchFailure != nil {
			if current, cleared := treeFailureAfterCopy(f, patchFailure); current != nil {
				patchFailure = current
			} else if cleared {
				patchFailure = nil
			}
		}
		if r.delivery.Key == "" {
			return run.TreeDelivery{Reason: r.delivery.Reason, Cause: r.delivery.Cause, Unsaid: r.delivery.Unsaid,
				PatchFailure: patchFailure}, later
		}
		if req.CopyRoot == "" {
			return run.TreeDelivery{Reason: f.Label() + " has a build on this machine, and this launch staged no pack " +
				"tree to copy it beside — a fresh launch delivers it", PatchFailure: patchFailure}, later
		}
		dir, err := copyTreeForLaunch(f, r.delivery.Key, req.CopyRoot, errw)
		switch {
		case err == nil:
			d := run.TreeDelivery{Dir: dir, Entry: r.delivery.Key, PatchFailure: patchFailure}
			if g := r.good; g != nil {
				d.Commit, d.Tag, d.Patches, d.Series = g.Commit, g.Tag, g.Patches, g.Series
			}
			return d, later
		case errors.Is(err, errTreeEntryGone) && attempt == 0:
			// THE ONE RE-READ (§8.1): another launch's move reaped the entry before or during the copy.
			pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf("%s: the build %s was reaped while this launch "+
				"copied it — reading the check record again", f.Label(), r.delivery.Key)))
			continue
		case errors.Is(err, errTreeEntryGone):
			return run.TreeDelivery{Reason: f.Label() + "'s build was reaped twice while this launch copied it — " +
				"the next fresh launch delivers the good build", PatchFailure: patchFailure}, later
		default:
			// THE COPY FAILED (a full disk, say): nothing for this launch, and the store is untouched.
			pr.Printf("[yellow]%s[/yellow]", richtext.Escape(fmt.Sprintf("⚠ %s: could not copy its build %s for "+
				"this jail: %v — the store is untouched; free the space and launch again", f.Label(),
				r.delivery.Key, err)))
			return run.TreeDelivery{Reason: fmt.Sprintf("%s's build could not be copied for this jail (%v)", f.Label(), err),
				PatchFailure: patchFailure}, later
		}
	}
}

// servingTree is what serves f with no check and no build: the good build, when its series and
// recipe are the manifest's and its entry is in the store, or why there is none (floor names why
// this launch builds nothing).
func servingTree(f packload.Fork, o advanceOptions, floor string) advanceResult {
	r, _ := servingTreeOf(f, o, floor, false)
	return r
}

// servingTreeOf is servingTree with the advance it read, nil when the series could not be read.
func servingTreeOf(f packload.Fork, o advanceOptions, floor string, probeLegacy bool) (advanceResult, *advance) {
	a, early := newAdvance(f, o)
	if early != nil {
		return *early, nil
	}
	patch := a.deliveryPatchFailure(probeLegacy)
	if patch.State == "unavailable" {
		return a.unavailableDetachedAuthority(patch), a
	}
	if patch.State == "failure" && patch.Failure != nil {
		a.patchFailure = patch.Failure
		a.reportPatchFailure(patch.Failure, a.patchFailureText(patch.Failure))
		delivery := entrypoint.ForkDelivery{PatchFailure: patch.Failure}
		result := advanceResult{delivery: delivery, patchFailure: patch.Failure, failed: true}
		if a.serving != nil {
			delivery.Key = a.serving.Key
			result.good = a.goodBuild()
		} else {
			delivery.Reason = patch.Failure.Error()
		}
		result.delivery = delivery
		return result, a
	}
	if a.serving == nil {
		why := f.Label() + " has no build on this machine"
		if floor != "" {
			why += ", and this runtime builds none (" + floor + ")"
		}
		return advanceResult{delivery: entrypoint.ForkDelivery{Reason: why}}, a
	}
	good := *a.rec.Good
	return advanceResult{delivery: entrypoint.ForkDelivery{Key: a.serving.Key}, good: &good}, a
}

// copyTreeForLaunch copies f's built tree out of entry key into its per-launch directory under
// copyRoot — reflink or copy, never a hardlink (capture.CopyTree) — and checks the entry's
// completion marker once the copy ends: gone, the copy is removed and errTreeEntryGone returned.
func copyTreeForLaunch(f packload.Fork, key, copyRoot string, errw io.Writer) (string, error) {
	store := &capture.Store{Dir: paths.CapturesDir()}
	dest := run.PatchedTreeCopyDir(copyRoot, f.Key())
	_ = os.RemoveAll(dest)
	entry, err := store.Resolve(key)
	if err != nil {
		return "", errTreeEntryGone
	}
	_, cerr := capture.CopyTree(capture.CopyTreeOptions{Entry: entry, Prefix: packdecl.TreeReservedDir(f.Bin),
		Dest: dest, Stderr: errw})
	treeCopied(key)
	if _, err := store.Resolve(key); err != nil {
		_ = os.RemoveAll(dest)
		return "", errTreeEntryGone
	}
	if cerr != nil {
		_ = os.RemoveAll(dest)
		return "", cerr
	}
	return dest, nil
}
