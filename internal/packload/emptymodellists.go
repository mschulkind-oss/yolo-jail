package packload

import (
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// EmptyModelListReason distinguishes a supplied list with no rows from a non-empty list whose
// declared makers all fall outside the program's pack-owned filter.
type EmptyModelListReason string

const (
	EmptyModelListSource   EmptyModelListReason = "source-empty"
	EmptyModelListFiltered EmptyModelListReason = "maker-filtered-empty"
)

// EmptyModelList is one opted-in program/provider pair whose supplied list contributes no
// callable rows, together with the first reachable configured profile that governs it.
type EmptyModelList struct {
	Program, Pack, Provider, Profile string
	Reason                           EmptyModelListReason
}

// EmptyModelListInput carries the selected set's composed providers and its configured profile
// resolution. Presence comes from ComposeProviders' out-of-band observer; it is deliberately not
// inferred from HasModelList or from the wire table, where empty lists may have been dropped.
type EmptyModelListInput struct {
	Packs     []*Pack
	Providers *jsonx.OrderedMap
	Profiles  map[string]string
	Sets      map[string][]string
	Resolved  map[string]ResolvedProfile
	Presence  map[string]bool
}

// EmptyModelLists reports opted-in programs that reach a provider with a supplied list but no
// callable rows. It is a pure preflight evaluation: no program, catalog, credential or service is
// read. Each program/provider is governed by its first reachable configured profile entry.
func EmptyModelLists(in EmptyModelListInput) []EmptyModelList {
	agents := make([]string, 0, len(in.Profiles)+len(in.Sets))
	seenAgent := map[string]bool{}
	for agent := range in.Profiles {
		seenAgent[agent] = true
		agents = append(agents, agent)
	}
	for agent := range in.Sets {
		if !seenAgent[agent] {
			agents = append(agents, agent)
		}
	}
	sort.Strings(agents)

	var out []EmptyModelList
	for _, agent := range agents {
		owner := binOwner(in.Packs, agent)
		if owner == nil || owner.Decl == nil {
			continue
		}
		check, optedIn := owner.Decl.ProgramModelListCheck(agent)
		if !optedIn {
			continue
		}
		profiles := in.Sets[agent]
		if len(profiles) == 0 {
			if profile := in.Profiles[agent]; profile != "" {
				profiles = []string{profile}
			}
		}
		seenProvider := map[string]bool{}
		for _, profile := range profiles {
			reach := profileReach(ProfileDisclosureInput{
				Packs: in.Packs, Providers: in.Providers, Resolved: in.Resolved,
			}, agent, profile)
			if reach.Provider == "" || reach.Route == "" || seenProvider[reach.Provider] {
				continue
			}
			seenProvider[reach.Provider] = true
			if !in.Presence[reach.Provider] {
				continue
			}
			entry := providerEntry(in.Providers, reach.Provider)
			if entry == nil {
				continue
			}
			via := ViaURLFor(in.Resolved[profile], agent) != ""
			rule := modelListRuleFor(check, entryString(entry, "platform"), via)
			rows := callableModelRows(entry)
			if len(rows) == 0 {
				if rule == nil || !rule.NativeEmptyOK {
					out = append(out, EmptyModelList{Program: agent, Pack: owner.Name, Provider: reach.Provider,
						Profile: profile, Reason: EmptyModelListSource})
				}
				continue
			}
			callable := false
			for _, row := range rows {
				if row.vendor == "" || rule == nil || len(rule.Makers) == 0 || modelMakerAccepted(rule.Makers, row.vendor) {
					callable = true
					break
				}
			}
			if !callable {
				out = append(out, EmptyModelList{Program: agent, Pack: owner.Name, Provider: reach.Provider,
					Profile: profile, Reason: EmptyModelListFiltered})
			}
		}
	}
	return out
}

type callableModelRow struct {
	id, vendor string
}

// callableModelRows mirrors the derives' callableModels expansion for the only fact this check
// needs: unique wire ids and each row's maker. Id-spelled aliases absorb facts first; remaining
// aliases follow bytewise order, and only non-empty strings fill missing facts. An empty alias is
// excluded from the id-spelled pass but retained in the sorted second pass, as in the derives.
// Model-only facts therefore cannot synthesize rows, and a missing maker remains callable.
func callableModelRows(entry *jsonx.OrderedMap) []callableModelRow {
	if entry == nil {
		return nil
	}
	models := childMap(entry, "models")
	if models == nil {
		return nil
	}
	options := childMap(entry, "model_options")
	aliases := make([]string, 0, models.Len())
	aliases = append(aliases, models.Keys()...)
	sort.Strings(aliases)
	rows := map[string]*callableModelRow{}
	absorb := func(id, alias string) {
		row := rows[id]
		if row == nil {
			row = &callableModelRow{id: id}
			rows[id] = row
		}
		facts := subOrderedOrNil(options, alias)
		if row.vendor == "" {
			if vendor, ok := stringAt(facts, "vendor"); ok && vendor != "" {
				row.vendor = vendor
			}
		}
	}
	for _, alias := range aliases {
		id, ok := stringAt(models, alias)
		if alias != "" && ok && id == alias {
			absorb(alias, alias)
		}
	}
	for _, alias := range aliases {
		id, ok := stringAt(models, alias)
		if ok && id != "" && id != alias {
			absorb(id, alias)
		}
	}
	out := make([]callableModelRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, *row)
	}
	return out
}

func modelListRuleFor(check *packdecl.ModelListCheck, platform string, via bool) *packdecl.ModelListCheckRule {
	if check == nil {
		return nil
	}
	for i := range check.Rules {
		rule := &check.Rules[i]
		if rule.Platform != platform || rule.Via != nil && *rule.Via != via {
			continue
		}
		return rule
	}
	return nil
}

func modelMakerAccepted(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
