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
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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

// deliverTreesForLaunch is run.Options.BuildTrees: every tree's delivery at once, in a PARALLEL
// ADVANCE (treepool.go, XB-D10) — each key's check, build and copy on a lane of its own, the lines in
// declaration order, one Ctrl-C ending every wait.
func deliverTreesForLaunch(req run.TreeBuildRequest, out, errw io.Writer, color bool) map[string]run.TreeDelivery {
	got := make([]run.TreeDelivery, len(req.Trees))
	later := make([]bool, len(req.Trees))
	// EACH BUILD IS ONE PROGRESS LINE on the launch's stream (buildreport.go), as the fork builds'.
	report := newBuildReport(req.Workspace, errw, req.Progress, color)
	runTreesInParallel(req.Trees, req.Runtime, req.Interrupt, out, errw, func(i int, f packload.Fork, lane treeLane) {
		got[i], later[i] = deliverTree(f, req, lane, report, color)
	})
	report.flush()
	m := map[string]run.TreeDelivery{}
	var background []packload.Fork
	for i, f := range req.Trees {
		m[f.Key()] = got[i]
		if later[i] {
			background = append(background, f)
		}
	}
	if len(background) > 0 {
		backgroundTreeAdvance(background, req, errw, color)
	}
	return m
}

// updateTiming is when a key's advance runs (docs/design/pi-extension-store-builds.md §7.6): AT THE
// LAUNCH, which waits for it, bounded — the default — or FOR THE NEXT LAUNCH, in a background advance
// that leaves this launch on the build it already has (XB-D19, XB-D28).
type updateTiming int

const (
	timingAtLaunch updateTiming = iota
	timingNextLaunch
)

// treeUpdateTiming is when f updates. THE SEAM for XB-D28's background mode: the refresh-timing
// option's per-program value (`agent_updates`' "next-launch", XB-D17) is not built in this tree, so
// every key updates at the launch. Its reader replaces this body; the tree arm reads nothing else.
var treeUpdateTiming = func(packload.Fork) updateTiming { return timingAtLaunch }

// backgroundTreeAdvance starts the BACKGROUND ADVANCE of the keys a launch handed what it had
// (XB-D19): a detached host process that checks and builds them for the next launch. THE SEAM's
// other half: not built in this tree (treeUpdateTiming never answers next-launch), so it says which
// keys wait and that the next launch at the default timing checks them.
var backgroundTreeAdvance = func(trees []packload.Fork, _ run.TreeBuildRequest, errw io.Writer, color bool) {
	pr := richtext.Printer{W: errw, Color: color}
	for _, f := range trees {
		pr.Printf("[dim]%s[/dim]", richtext.Escape(f.Label()+": updates for the next launch, and this yolo runs no "+
			"background advance — a fresh launch at the default timing checks it"))
	}
}

// deliverTree is one built tree's delivery on its lane: what serves, then its per-launch copy, with
// the one re-read a reaped entry gets; and whether its advance waits for a background one (later).
//
// FOR THE NEXT LAUNCH (treeUpdateTiming, XB-D19): a key with a good build is handed it with no check,
// one with none but a fallback takes the fallback this once, and only a key with neither builds in
// front, since there is nothing to hand; the first two are left to the background advance.
func deliverTree(f packload.Fork, req run.TreeBuildRequest, lane treeLane, report *buildReport,
	color bool) (_ run.TreeDelivery, later bool) {
	errw := lane.errw
	pr := richtext.Printer{W: errw, Color: color}
	o := lane.options(advanceOptions{platform: req.Platform, runtime: req.Runtime, workspace: req.Workspace,
		color: color, launch: true, act: req.Interrupt, report: report})
	nextLaunch := req.Build && treeUpdateTiming(f) == timingNextLaunch
	for attempt := 0; ; attempt++ {
		var r advanceResult
		switch {
		case nextLaunch:
			if r = servingTree(f, o, ""); r.delivery.Key != "" {
				later = true
				break
			}
			if f.Fallback != "" {
				return run.TreeDelivery{Reason: f.Label() + " has no build on this machine yet, and updates for " +
					"the next launch — a background advance builds it"}, true
			}
			r = treeAdvance(f, o)
		case req.Build:
			r = treeAdvance(f, o)
		default:
			r = servingTree(f, o, req.BuildFloor)
		}
		if r.delivery.Key == "" {
			return run.TreeDelivery{Reason: r.delivery.Reason, Cause: r.delivery.Cause, Unsaid: r.delivery.Unsaid}, later
		}
		if req.CopyRoot == "" {
			return run.TreeDelivery{Reason: f.Label() + " has a build on this machine, and this launch staged no pack " +
				"tree to copy it beside — a fresh launch delivers it"}, later
		}
		dir, err := copyTreeForLaunch(f, r.delivery.Key, req.CopyRoot, errw)
		switch {
		case err == nil:
			d := run.TreeDelivery{Dir: dir, Entry: r.delivery.Key}
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
				"the next fresh launch delivers the good build"}, later
		default:
			// THE COPY FAILED (a full disk, say): nothing for this launch, and the store is untouched.
			pr.Printf("[yellow]%s[/yellow]", richtext.Escape(fmt.Sprintf("⚠ %s: could not copy its build %s for "+
				"this jail: %v — the store is untouched; free the space and launch again", f.Label(),
				r.delivery.Key, err)))
			return run.TreeDelivery{Reason: fmt.Sprintf("%s's build could not be copied for this jail (%v)", f.Label(), err)}, later
		}
	}
}

// servingTree is what serves f with no check and no build: the good build, when its series and
// recipe are the manifest's and its entry is in the store, or why there is none (floor names why
// this launch builds nothing).
func servingTree(f packload.Fork, o advanceOptions, floor string) advanceResult {
	a, early := newAdvance(f, o)
	if early != nil {
		return *early
	}
	if a.serving == nil {
		why := f.Label() + " has no build on this machine"
		if floor != "" {
			why += ", and this runtime builds none (" + floor + ")"
		}
		return advanceResult{delivery: entrypoint.ForkDelivery{Reason: why}}
	}
	good := *a.rec.Good
	return advanceResult{delivery: entrypoint.ForkDelivery{Key: a.serving.Key}, good: &good}
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
