package packload

// unnarrowedmenus.go is the rule behind `yolo check`'s line that a profile's `enforce_models`
// switch leaves an agent's model menu unnarrowed (docs/design/model-lists-and-pickers.md MM-D5:
// "opencode cannot shape its menu without refusing, so with the switch off it gets no whitelist,
// and `yolo check` says its menu is then not narrowed"; MM-D29 the mechanism).
//
// WHICH AGENTS: the programs whose pack declares `exact_menu_refuses` (packdecl.ExactMenuRefusal),
// never a name core knows. WHICH LISTS: a list a `models` `only` narrowed (ModelsOnlyKey), and the
// whole list of a provider the declaration names. WHOSE SWITCH: the one a derive reads for that
// provider's row, the rule packs/opencode's opencodeEnforceFor states and every set-capable derive
// follows: the agent's primary profile for the provider it selects, else the first later entry of
// its active set on that provider, each entry's own profile's (ModelsEnforced). The set is the one
// surfaceSelectionFor hands a derive: the agent's `profile` set when it starts with the primary,
// else the primary alone.
//
// It never sees `-p`: its caller passes the configured selection, as every `yolo check` prediction
// does.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// UnnarrowedMenu is one agent's model menu for one provider that yolo does not narrow to the
// provider's list, because Profile, the profile governing that provider for Agent, turns
// `enforce_models` off. Pack is the pack declaring Agent's `exact_menu_refuses`.
type UnnarrowedMenu struct {
	Agent, Pack, Provider, Profile string
}

// UnnarrowedMenus is every UnnarrowedMenu of the launch: each program among packs declaring
// `exact_menu_refuses`, in pack order, and each provider of its active set, in set order, whose
// composed list is not empty, is one that declaration covers, names an endpoint or a platform
// (firstPartyOnly), and is governed by a profile with the switch off. profiles is the agent → primary profile table and sets the agent → active set
// table the launch lowers (ProfileTable, ProfileSets); resolved and providers are the resolved
// profiles and the composed providers table. Nil when there is none.
func UnnarrowedMenus(packs []*Pack, providers *jsonx.OrderedMap, resolved map[string]ResolvedProfile,
	profiles map[string]string, sets map[string][]string) []UnnarrowedMenu {
	var out []UnnarrowedMenu
	seenAgent := map[string]bool{}
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindProgram || c.ExactMenuRefuses == nil || c.Bin == "" || seenAgent[c.Bin] {
				continue
			}
			seenAgent[c.Bin] = true
			whole := map[string]bool{}
			for _, name := range c.ExactMenuRefuses.Providers {
				whole[name] = true
			}
			seenProvider := map[string]bool{}
			for _, profile := range governingSet(profiles[c.Bin], sets[c.Bin]) {
				provider := ProviderFor(resolved, profile)
				if provider == "" || seenProvider[provider] {
					continue
				}
				// The first entry on a provider is the one that governs it, whatever its switch.
				seenProvider[provider] = true
				entry := providerEntry(providers, provider)
				if entry == nil || ModelsEnforced(resolved[profile]) || !hasModels(entry) || firstPartyOnly(entry) {
					continue
				}
				if narrowed, _ := entry.Get(ModelsOnlyKey); narrowed != true && !whole[provider] {
					continue
				}
				out = append(out, UnnarrowedMenu{Agent: c.Bin, Pack: p.Name, Provider: provider, Profile: profile})
			}
		}
	}
	return out
}

// firstPartyOnly reports whether a composed provider entry names no endpoint and no platform: it
// repoints nothing, and means the agent's own first-party API (docs/reference/protocol-resolution.md
// OQ-PR2). yolo's table points no row of the agent's anywhere for it, so there is no row for a
// derive to write a filter on, with the switch on or off, and the switch decides nothing there.
func firstPartyOnly(entry *jsonx.OrderedMap) bool {
	if len(providerProtocols(entry)) > 0 {
		return false
	}
	platform, _ := entry.Get("platform")
	s, _ := platform.(string)
	return s == ""
}

// governingSet is the active set a derive is handed for an agent whose primary profile is
// primary: set when it starts with primary, else primary alone, else nothing
// (surfaceSelectionFor's rule).
func governingSet(primary string, set []string) []string {
	if primary == "" {
		return nil
	}
	if len(set) == 0 || set[0] != primary {
		return []string{primary}
	}
	return set
}

// hasModels reports whether a composed provider entry lists at least one model.
func hasModels(entry *jsonx.OrderedMap) bool {
	v, _ := entry.Get("models")
	models, _ := v.(*jsonx.OrderedMap)
	return models != nil && models.Len() > 0
}
