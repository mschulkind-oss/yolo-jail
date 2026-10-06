package packload

// fetchedmodels.go is the composition's half of the FETCHED LIST (docs/design/model-lists-and-pickers.md
// OQ-MM6, ruled 2026-10-05; the term is coined there): the model list yolo reads from a provider's
// platform itself where no selected pack and no user config supplies one. This file says which
// providers of a launch want one and for whom, and writes one onto the composed table; fetching it
// is the launch's (internal/cli/run's bedrockmodels.go), which asks the platform's credential
// service.
//
// A LIST OF ITS OWN KEY, NEVER `models`. A fetched list is not a list a pack or the user chose: an
// agent with a catalog of its own keeps that catalog (MM-D32), so the derives that read `models`
// to narrow a menu or pin a start (pi, codex, opencode, claude) must not see it. It rides the same
// composed table, YOLO_PROVIDERS, under FetchedModelsKey, read only by what has nothing of its
// own to fall back on: the wire bridge, which takes each model's maker from it
// (wirebridged.anthropicModelIDs), and copilot's derive.
//
// WHO WANTS ONE. An agent whose active set reaches a provider that declares a `platform` and
// carries no `models` wants that provider's list when its pack declares `needs_model_list` for the
// platform (it has nothing else to start on), or when its profile sends it through a service at
// the anthropic address composed for that service, the one route where the wire bridge reads a
// model's maker (readsMakersAtTheBridge). An agent wanting none, such as pi on `-p bedrock` or on
// the bridge's via route, costs the launch no fetch.

import (
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// FetchedModelsKey is the composed provider entry's key for a fetched list: an array of
// {"id", "vendor", "name"} objects in the order the fetch returned them.
const FetchedModelsKey = "fetched_models"

// FetchedModel is one entry of a fetched list.
type FetchedModel struct {
	ID     string
	Vendor string
	Name   string
}

// ListWant is one provider of a launch that wants a fetched list.
type ListWant struct {
	Provider string
	Platform string
	// Region is the region the list is fetched for: the provider's own `region`, else the first
	// of the platform's region variables delivered to the first wanting agent. "" when neither
	// gives one.
	Region string
	// Service is the loophole serving the platform's credentials (an `env` contribution's
	// `served_by` on that platform: aws-auth for "aws-bedrock"), "" when no selected pack
	// declares one.
	Service string
	// Agents wants it, sorted.
	Agents []string
	// Needs is the agents among Agents that have nothing else to start on: the pack declares
	// `needs_model_list` for the platform and the profile names no `model`. Sorted. Each maps to
	// the profile that selected the provider for it.
	Needs map[string]string
}

// HasModelList reports whether a composed provider entry carries a list a pack or the user's
// config supplied: a non-empty `models` map.
func HasModelList(entry *jsonx.OrderedMap) bool {
	if entry == nil {
		return false
	}
	v, _ := entry.Get("models")
	m, _ := v.(*jsonx.OrderedMap)
	return m != nil && m.Len() > 0
}

// ListWants is every provider the launch should fetch a list for, in provider order.
func ListWants(packs []*Pack, providers *jsonx.OrderedMap, resolved map[string]ResolvedProfile,
	scope *CredentialScope) []ListWant {
	byProvider := map[string]*ListWant{}
	reqs := regionRequirements(packs)
	for _, agent := range scope.Agents() {
		d := scope.Agent(agent)
		set, setProviders := d.Set, d.setProviders()
		if len(set) == 0 && d.Profile != "" {
			set = []string{d.Profile}
		}
		for i, provider := range setProviders {
			if i >= len(set) {
				break
			}
			entry := providerEntry(providers, provider)
			platform := entryString(entry, "platform")
			if entry == nil || platform == "" || HasModelList(entry) {
				continue
			}
			profile := set[i]
			owner := binOwner(packs, agent)
			needs := false
			if owner != nil && owner.Decl != nil && owner.Decl.NeedsModelList(agent, platform) {
				needs = resolved[profile].Options["model"] == ""
			}
			if !needs && !readsMakersAtTheBridge(owner, agent, entry, resolved[profile]) {
				continue
			}
			w := byProvider[provider]
			if w == nil {
				w = &ListWant{Provider: provider, Platform: platform, Service: platformService(packs, platform),
					Region: entryString(entry, "region"), Needs: map[string]string{}}
				byProvider[provider] = w
			}
			w.Agents = append(w.Agents, agent)
			if needs {
				w.Needs[agent] = profile
			}
			if w.Region == "" {
				for _, v := range reqs[platform].vars {
					if r, ok := scope.DeliveredTo(agent, v); ok {
						w.Region = r
						break
					}
				}
			}
		}
	}
	var out []ListWant
	for _, name := range providers.Keys() {
		if w := byProvider[name]; w != nil {
			sort.Strings(w.Agents)
			out = append(out, *w)
		}
	}
	return out
}

// readsMakersAtTheBridge reports whether agent reaches entry through a service at the anthropic
// address composed for that service (ForViaKey), speaking anthropic: the one route where the wire
// bridge reads a model's maker, passing an Anthropic model to runtime's Messages route untranslated
// and refusing another maker's on Bedrock's own invoke routes. An agent on the via routes (pi,
// codex, opencode, oh-omp) is relayed unchanged, maker unread.
func readsMakersAtTheBridge(owner *Pack, agent string, entry *jsonx.OrderedMap, r ResolvedProfile) bool {
	if via, _ := r.ViaFor(agent); via == "" || owner == nil || owner.Decl == nil {
		return false
	}
	eps := omapAt(entry, "endpoints")
	if entryString(omapAt(eps, "anthropic"), ForViaKey) == "" {
		return false
	}
	for _, p := range owner.Decl.SpokenProtocols(agent) {
		if p == "anthropic" {
			return true
		}
	}
	return false
}

// platformService is the loophole serving platform's credentials: the `served_by` of the first
// selected `env` contribution declaring that platform, "" for none.
func platformService(packs []*Pack, platform string) string {
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindEnv && c.Platform == platform && c.ServedBy != "" {
				return c.ServedBy
			}
		}
	}
	return ""
}

// SetFetchedModels writes list onto provider's composed entry under FetchedModelsKey, replacing
// any there. An empty list writes nothing, so "fetched nothing" and "fetched no list" read alike.
func SetFetchedModels(providers *jsonx.OrderedMap, provider string, list []FetchedModel) {
	entry := providerEntry(providers, provider)
	if entry == nil || len(list) == 0 {
		return
	}
	rows := make([]any, 0, len(list))
	for _, m := range list {
		row := jsonx.NewOrderedMap()
		row.Set("id", m.ID)
		row.Set("vendor", m.Vendor)
		if m.Name != "" {
			row.Set("name", m.Name)
		}
		rows = append(rows, row)
	}
	entry.Set(FetchedModelsKey, rows)
}

// FetchedModelsOf reads a composed entry's fetched list, nil for none. Rows without an id are
// skipped.
func FetchedModelsOf(entry *jsonx.OrderedMap) []FetchedModel {
	if entry == nil {
		return nil
	}
	v, _ := entry.Get(FetchedModelsKey)
	rows, _ := v.([]any)
	var out []FetchedModel
	for _, r := range rows {
		row, _ := r.(*jsonx.OrderedMap)
		if row == nil {
			continue
		}
		m := FetchedModel{ID: entryString(row, "id"), Vendor: entryString(row, "vendor"), Name: entryString(row, "name")}
		if m.ID != "" {
			out = append(out, m)
		}
	}
	return out
}
