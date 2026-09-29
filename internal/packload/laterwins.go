package packload

// laterwins.go is THE RULE FOR A SOLE-OWNED CLAIM TWO PACKS DECLARE: the LATER declaration holds
// it, the same "later wins" every other key follows (docs/plans/notch-convergence.md OQ-NC4 and
// NC-D59, the orchestrator's decision of 2026-09-28).
//
// "Later" is the order of the packs a composer is handed, which is the one precedence order
// (config.PackSelection.Packs: config order, then the selection closure's additions, then the
// conventional local pack last), then declaration order inside a pack. So the user's local pack
// holds an adapter pair a pack pulled in through `needs` also declares, as it holds every other
// key.
//
// It used to be FIRST wins at each composer that met a duplicate (Adaptations, ComposeProviders,
// packShippedProfiles, the provider attributions), chosen so a caller that skipped a pre-flight
// degraded to "a stable table rather than to whichever pack happened to sort last". The order is
// no longer an accident of sorting, so later is as stable as first, and it is the one answer the
// rest of composition gives. The pre-flights are untouched: a jail launch still refuses a
// provider name two packs ship (run.packProviderNameConflicts), and `yolo pack footprint`
// reports every duplicated sole-owned claim (Collisions).

import "github.com/mschulkind-oss/yolo-jail/internal/packdecl"

// laterWins reports, for n claims in precedence order whose keys key(i) gives, which of them
// holds its key: the last one declaring it. Every composer that keeps one claim per key asks this
// and nothing else, so no composer can hold a rule of its own.
func laterWins[K comparable](n int, key func(i int) K) []bool {
	last := make(map[K]int, n)
	for i := 0; i < n; i++ {
		last[key(i)] = i
	}
	holds := make([]bool, n)
	for _, i := range last {
		holds[i] = true
	}
	return holds
}

// ServiceNamed is the `service` contribution named name that the given packs hold, under the
// same rule: the LAST declaration in packs' order, ok=false when no pack declares one. A service
// name is sole-owned (Collisions reports two), and no launch pre-flight refuses a second
// declarer, so a reader of one service by name asks this rather than taking the first hit.
func ServiceNamed(packs []*Pack, name string) (packdecl.ServiceContribution, bool) {
	var out packdecl.ServiceContribution
	found := false
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, s := range p.Decl.Services() {
			if s.Name == name {
				out, found = s, true
			}
		}
	}
	return out, found
}

// lastDeclarer is the last of packs, in the order given, for which declares reports true: the
// pack holding a sole-owned claim under laterWins, nil when none declares it.
func lastDeclarer(packs []*Pack, declares func(*Pack) bool) *Pack {
	var out *Pack
	for _, p := range packs {
		if p != nil && p.Decl != nil && declares(p) {
			out = p
		}
	}
	return out
}
