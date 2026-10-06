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

// deliverTreesForLaunch is run.Options.BuildTrees.
func deliverTreesForLaunch(req run.TreeBuildRequest, out, errw io.Writer, color bool) map[string]run.TreeDelivery {
	got := map[string]run.TreeDelivery{}
	// EACH BUILD IS ONE PROGRESS LINE on the launch's stream (buildreport.go), as the fork builds'.
	report := newBuildReport(req.Workspace, errw, req.Progress, color)
	for _, f := range req.Trees {
		got[f.Key()] = deliverTree(f, req, report, out, errw, color)
	}
	report.flush()
	return got
}

// deliverTree is one patched extension's delivery: what serves, then its per-launch copy, with the
// one re-read a reaped entry gets.
func deliverTree(f packload.Fork, req run.TreeBuildRequest, report *buildReport, out, errw io.Writer,
	color bool) run.TreeDelivery {
	pr := richtext.Printer{W: errw, Color: color}
	o := advanceOptions{platform: req.Platform, runtime: req.Runtime, workspace: req.Workspace, out: out,
		errw: errw, color: color, launch: true, act: req.Interrupt, report: report}
	for attempt := 0; ; attempt++ {
		var r advanceResult
		if req.Build {
			r = treeAdvance(f, o)
		} else {
			r = servingTree(f, o, req.BuildFloor)
		}
		if r.delivery.Key == "" {
			return run.TreeDelivery{Reason: r.delivery.Reason, Cause: r.delivery.Cause}
		}
		if req.CopyRoot == "" {
			return run.TreeDelivery{Reason: f.Label() + " has a build on this machine, and this launch staged no pack " +
				"tree to copy it beside — a fresh launch delivers it"}
		}
		dir, err := copyTreeForLaunch(f, r.delivery.Key, req.CopyRoot, errw)
		switch {
		case err == nil:
			d := run.TreeDelivery{Dir: dir, Entry: r.delivery.Key}
			if g := r.good; g != nil {
				d.Commit, d.Tag, d.Patches, d.Series = g.Commit, g.Tag, g.Patches, g.Series
			}
			return d
		case errors.Is(err, errTreeEntryGone) && attempt == 0:
			// THE ONE RE-READ (§8.1): another launch's move reaped the entry before or during the copy.
			pr.Printf("[dim]%s[/dim]", richtext.Escape(fmt.Sprintf("%s: the build %s was reaped while this launch "+
				"copied it — reading the check record again", f.Label(), r.delivery.Key)))
			continue
		case errors.Is(err, errTreeEntryGone):
			return run.TreeDelivery{Reason: f.Label() + "'s build was reaped twice while this launch copied it — " +
				"the next fresh launch delivers the good build"}
		default:
			// THE COPY FAILED (a full disk, say): nothing for this launch, and the store is untouched.
			pr.Printf("[yellow]%s[/yellow]", richtext.Escape(fmt.Sprintf("⚠ %s: could not copy its build %s for "+
				"this jail: %v — the store is untouched; free the space and launch again", f.Label(),
				r.delivery.Key, err)))
			return run.TreeDelivery{Reason: fmt.Sprintf("%s's build could not be copied for this jail (%v)", f.Label(), err)}
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
