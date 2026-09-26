package packload

import (
	"fmt"
	"sort"
)

// ResolveVias is the `via` half of the selection closure (OQ-WG6/WG7 (c),
// docs/design/wire-bridge-gateway.md): selecting a profile whose `via` names a service
// pack adds that pack to the launch the way a live `needs` entry does, so a pi-only jail
// with a via profile gets the service's daemon. Core knows only "the pack a via names";
// which pack that is, and that it runs a bridge, are the manifests' facts.
//
// active is the launch's CLI-name → profile-name table (the effective use_profiles);
// user is the user's profile declarations, whose `via` wins over a pack-shipped one's,
// as it does in ResolveProfiles. embedded looks a name up in the embedded official set.
//
// The rules, each a refusal with the pack and profile named:
//
//   - A via may name only an EMBEDDED official pack, for needs' reason (WB-D9): a
//     profile — which a user config can declare — must not pull unreviewed code into a
//     launch. A pack the user already selected is a join, not an addition.
//   - The named pack must declare a service with a `via_address`; a pack that serves no
//     via route would leave the agent pointed at an address nothing binds.
//
// It returns the packs added (not yet in selected), one cause line per addition in the
// needs closure's shape, and the error. The caller re-runs ResolveNeeds over the grown
// set, since an added pack may declare needs of its own.
func ResolveVias(selected []*Pack, active map[string]string, user map[string]UserProfile,
	embedded func(name string) (*Pack, bool)) (added []*Pack, causes []string, err error) {
	set := make(map[string]*Pack, len(selected))
	for _, p := range selected {
		if p != nil {
			set[p.Name] = p
		}
	}
	shipped := packShippedProfiles(selected)

	agents := make([]string, 0, len(active))
	for agent := range active {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	for _, agent := range agents {
		name := active[agent]
		via := shipped[name].Via
		if u, ok := user[name]; ok && u.Via != "" {
			via = u.Via
		}
		if via == "" {
			continue
		}
		p, have := set[via]
		if !have {
			target, ok := embedded(via)
			if !ok {
				return nil, nil, fmt.Errorf("profile %q (active for %s) names via %q, which is "+
					"not an embedded official pack — a via may name only packs yolo ships, so a "+
					"profile cannot pull unreviewed code into a launch (the needs rule, WB-D9)",
					name, agent, via)
			}
			p = target
			set[via] = p
			added = append(added, p)
			causes = append(causes, "+ "+via+" (via of profile "+name+", active for "+agent+")")
		}
		if addr, _ := ViaServiceAddress([]*Pack{p}, via); addr == "" {
			return nil, nil, fmt.Errorf("profile %q (active for %s) names via %q, whose pack "+
				"declares no service with a via_address — nothing would serve %s's route, so "+
				"its agent would be pointed at an address nothing binds", name, agent, via, agent)
		}
	}
	return added, causes, nil
}
