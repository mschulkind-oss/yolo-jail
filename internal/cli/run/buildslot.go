package run

// buildslot.go is THE FORK-BUILD SLOT as one act (docs/design/pi-extension-store-builds.md §5.2,
// XB-D10): a fresh launch hands every key its slot serves — each plain fork's missing build, each
// patched fork's advance, each patched extension's advance and copy — to the build act at once
// (Options.BuildSlot), whose pool runs them together, and starts the image's own build beside it
// (imageprewarm.go), which nothing the slot does feeds.
//
// The slot's two halves stay the triggers they were: forkBuildPlan and treeBuildPlan decide each
// key's reason, or that the act readies it, and finishForkDeliveries and finishTreeDeliveries merge
// the act's answers. Only the act's call is one call, so one pool and one interrupt scope cover both
// halves: a Ctrl-C in any build ends every wait of the slot (PF-D57), and the keys print in
// declaration order, the forks' before the extensions', as the two halves always did (PPX-D13).

import (
	goruntime "runtime" // stdlib; this package's `runtime` is yolo's own (run.go)
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// BuildSlotRequest is the fork-build slot's whole act (Options.BuildSlot): the fork builds' request
// and the tree arm's, either nil when its half has nothing for the act, and how many checks and
// sealed build jails the act runs at once.
type BuildSlotRequest struct {
	Forks *ForkBuildRequest
	Trees *TreeBuildRequest
	// MaxChecks and MaxBuilds bound the pool (SlotChecks, SlotBuildJails); 0 takes those.
	MaxChecks, MaxBuilds int
}

// SlotChecks is how many upstream checks a launch's fork-build slot runs at once (XB-D10): a check
// is network-bound and cheap (§5.2, MEASURED in the research pass).
const SlotChecks = 8

// SlotBuildJails is how many sealed build jails a launch's fork-build slot runs at once on rt
// (XB-D10): min(4, max(1, CPUs/2)), a build being CPU-bound and 4 being pi's own
// GIT_UPDATE_CONCURRENCY; and one on Apple Container, where a capture jail cannot start beside a
// running jail (INFERRED, program-delivery.md OQ-PD25) and a second build jail is one.
func SlotBuildJails(rt string) int {
	return SlotBuildJailsOn(rt, goruntime.NumCPU())
}

// SlotBuildJailsOn is SlotBuildJails on a machine of cpus CPUs: the one statement of the rule, which
// a caller that reads its CPU count through a seam of its own (cli's pools) applies to that count.
func SlotBuildJailsOn(rt string, cpus int) int {
	if !BuildJailsSideBySide(rt) {
		return 1
	}
	return min(4, max(1, cpus/2))
}

// handedForksMu guards Options.handedForks, which every advance of the slot's pool appends to at
// once through ForkBuildRequest.Hand.
var handedForksMu sync.Mutex

// runForkBuildSlot is the slot: both halves planned, one call of the act for every key they ready,
// and both answered and recorded. With no BuildSlot (a caller that injects the halves alone) each
// half calls its own act, the forks' first.
func (o *Options) runForkBuildSlot(cfg *jsonx.OrderedMap, rt, repoRoot string) {
	if o.BuildSlot == nil {
		o.forkDelivered = o.forkDeliveriesFor(rt)
		o.treeDelivered = o.treeDeliveriesFor(rt)
		return
	}
	forks, fReq := o.forkBuildPlan(rt)
	trees, tReq := o.treeBuildPlan(rt)
	if fReq != nil || tReq != nil {
		// THE IMAGE'S OWN BUILD, BESIDE THE POOL: nothing the slot does feeds it, so it starts now
		// rather than once the slowest key has ended.
		o.startImagePrewarm(cfg, rt, repoRoot)
		fAns, tAns := o.BuildSlot(BuildSlotRequest{Forks: fReq, Trees: tReq, MaxChecks: SlotChecks,
			MaxBuilds: SlotBuildJails(rt)})
		if fReq != nil {
			o.finishForkDeliveries(forks, fReq, fAns)
		}
		if tReq != nil {
			o.finishTreeDeliveries(trees, tAns)
		}
	}
	if forks != nil {
		o.recordHandedForks(forks)
	}
	if trees != nil {
		o.recordHandedTrees(trees)
	}
	o.forkDelivered, o.treeDelivered = forks, trees
}
