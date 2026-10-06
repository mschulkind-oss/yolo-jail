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

// HeldService is one `service` contribution and the pack that declared it.
type HeldService struct {
	Pack    string
	Service packdecl.ServiceContribution
}

// ShadowedService is a `service` declaration another declaration of the same name holds over:
// Pack declared Name, and HeldBy, later in the pack order, holds it.
type ShadowedService struct {
	Name   string
	Pack   string
	HeldBy string
}

// HeldServices splits packs' `service` contributions under laterWins: held is the one
// declaration each service name keeps, the LAST in packs' order, in declaration order; shadowed
// is every other declaration of a held name, in declaration order. A service name is sole-owned
// (Collisions reports two, so `yolo pack footprint` names the pair), and no launch pre-flight
// refuses a second declarer, so every reader of the services asks this: among them the
// jail-daemon payload (launchservice.ServiceJailDaemons, which run.serviceJailDaemons and `yolo
// check`'s prediction both read), the host half's admission (launchservice.Admit), the pure
// workers a launch starts beside its command, the name-keyed reader (ServiceNamed), and the
// launch's disclosure of what it set aside (run.noteShadowedServices).
func HeldServices(packs []*Pack) (held []HeldService, shadowed []ShadowedService) {
	var all []HeldService
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, s := range p.Decl.Services() {
			all = append(all, HeldService{Pack: p.Name, Service: s})
		}
	}
	holds := laterWins(len(all), func(i int) string { return all[i].Service.Name })
	holder := map[string]string{}
	for i, s := range all {
		if holds[i] {
			held = append(held, s)
			holder[s.Service.Name] = s.Pack
		}
	}
	for i, s := range all {
		if !holds[i] {
			shadowed = append(shadowed, ShadowedService{
				Name: s.Service.Name, Pack: s.Pack, HeldBy: holder[s.Service.Name]})
		}
	}
	return held, shadowed
}

// ServiceNamed is the `service` contribution named name that the given packs hold, under the
// same rule (HeldServices), ok=false when no pack declares one. A reader of one service by name
// asks this rather than taking the first hit.
func ServiceNamed(packs []*Pack, name string) (packdecl.ServiceContribution, bool) {
	held, _ := HeldServices(packs)
	for _, s := range held {
		if s.Service.Name == name {
			return s.Service, true
		}
	}
	return packdecl.ServiceContribution{}, false
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
