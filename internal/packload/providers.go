package packload

// providers.go composes the PROVIDERS table a launch feeds its derives: the packs'
// shipped `kind: "provider"` service facts, laid UNDER the user's `providers` config
// entries (profiles-as-pack-variants.md §4.1 as ruled, OQ-12).
//
// The composition happens HERE, in the host CLI, and exactly once per launch: its output
// is what crosses to the jail as YOLO_PROVIDERS, and the in-jail side reads that table
// verbatim (entrypoint.LoadProviders → liveTables → ctx.providers). Composing anywhere
// later would mean a second implementation of the merge in the entrypoint — the drift the
// one-composition rule exists to prevent — and composing nowhere would make the kind a
// schema the derived configs never see.

import (
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// ComposeProviders returns the providers table for a launch: every selected pack's
// shipped providers, then the user's own `providers` entries composed OVER them per
// field. Nil when NEITHER side declares anything, so the caller's empty-object encoding
// is byte-identical to a launch with no provider at all.
//
// The merge is per FIELD, not per provider: a user who wants z.ai with one more model
// alias overrides `models.fast` and keeps the pack's endpoints, which is the whole point
// of shipping the facts (zai-plumbing.md §7 — "overrides, not authoring"). Objects merge
// recursively; every other value replaces. A null user entry DROPS the provider outright,
// the same convention the `providers` config key already has for a null entry.
//
// A provider NAME claimed by two packs is refused by the launch pre-flight (the kind is
// sole-owned by name; the claim target is the bare name, so packload.Collisions' generic
// exclusive loop reports it). This compose keeps the FIRST and never overwrites, so a
// caller that skipped the pre-flight degrades to a stable table rather than to whichever
// pack happened to sort last.
func ComposeProviders(user *jsonx.OrderedMap, packs []*Pack) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, p := range packs {
		for _, prov := range p.Decl.Providers() {
			if _, seen := out.Get(prov.Name); seen {
				continue
			}
			out.Set(prov.Name, shippedProviderEntry(prov))
		}
	}
	if user == nil {
		return orderedOrNil(out)
	}
	for _, name := range user.Keys() {
		v, _ := user.Get(name)
		if v == nil {
			// A null entry disables — including a pack-shipped one. The user's config is
			// the override layer, and "override" has always included "no".
			out.Delete(name)
			continue
		}
		u, ok := v.(*jsonx.OrderedMap)
		if !ok {
			out.Set(name, v) // malformed; the config validator already reported the shape
			continue
		}
		cur, seen := out.Get(name)
		if !seen {
			out.Set(name, u)
			continue
		}
		if cm, ok := cur.(*jsonx.OrderedMap); ok {
			mergeUnder(cm, u)
			continue
		}
		out.Set(name, u)
	}
	return orderedOrNil(out)
}

// ProviderFor returns the provider the variant active at CLI name `bin` delivers: the
// requires_provider of a profile declared under that name by a selected pack, or — when
// no pack declares one — the profile's own name, which is the convention the composed
// table has always keyed on (pack_profiles.claude = "bedrock" reaching providers.bedrock).
//
// The declaration need NOT live on the pack that installs the bin. Profile names are
// free-form and global (profiles-as-pack-variants.md §3.3), and a provider pack usually
// installs no CLI at all — so keying this on the bin's owner would make its
// requires_provider unreachable and, with it, the whole deliver-the-provider-to-the-agent
// shape the kind exists for. The bin owner's own declaration wins when both declare (the
// agent's variant is the more specific intent), then the first pack in delivery order, so
// the answer is stable rather than map-order.
//
// Empty when no variant is active, and the bare name when nothing requires anything:
// agentenv treats both as inert, because escalating a provider that is missing or
// unhydrated is the §6.2 preflight's business, not this lookup's.
func ProviderFor(packs []*Pack, bin, profile string) string {
	if profile == "" {
		return ""
	}
	for _, p := range packs {
		if !p.installsBin(bin) {
			continue
		}
		if prof := p.Decl.ProfileFor(profile); prof != nil && prof.RequiresProvider != "" {
			return prof.RequiresProvider
		}
	}
	for _, p := range packs {
		if prof := p.Decl.ProfileFor(profile); prof != nil && prof.RequiresProvider != "" {
			return prof.RequiresProvider
		}
	}
	return profile
}

// installsBin reports whether this pack puts bin on PATH.
func (p *Pack) installsBin(bin string) bool {
	for _, b := range p.InstallBins() {
		if b == bin {
			return true
		}
	}
	return false
}

// shippedProviderEntry renders one pack's provider declaration as an entry of the
// providers table — the SAME shape a user-written entry has, because what consumes the
// table (the three derives) reads one schema.
func shippedProviderEntry(prov packdecl.ProviderContribution) *jsonx.OrderedMap {
	entry := jsonx.NewOrderedMap()
	if prov.APIKeyEnvName != "" {
		entry.Set("api_key_env_name", prov.APIKeyEnvName)
	}
	if prov.Region != "" {
		entry.Set("region", prov.Region)
	}
	if len(prov.Models) > 0 {
		models := jsonx.NewOrderedMap()
		for _, alias := range sortedMapKeys(prov.Models) {
			models.Set(alias, prov.Models[alias])
		}
		entry.Set("models", models)
	}
	if len(prov.Endpoints) > 0 {
		endpoints := jsonx.NewOrderedMap()
		for _, proto := range sortedEndpointProtocols(prov.Endpoints) {
			e := prov.Endpoints[proto]
			ep := jsonx.NewOrderedMap()
			if e.BaseURL != "" {
				ep.Set("base_url", e.BaseURL)
			}
			if e.WireAPI != "" {
				ep.Set("wire_api", e.WireAPI)
			}
			endpoints.Set(proto, ep)
		}
		entry.Set("endpoints", endpoints)
	}
	if len(prov.EnvShape) > 0 {
		shape := jsonx.NewOrderedMap()
		for _, proto := range sortedStringMapKeys(prov.EnvShape) {
			vars := jsonx.NewOrderedMap()
			for _, name := range sortedMapKeys(prov.EnvShape[proto]) {
				vars.Set(name, prov.EnvShape[proto][name])
			}
			shape.Set(proto, vars)
		}
		entry.Set("env_shape", shape)
	}
	return entry
}

// mergeUnder folds src into dst IN PLACE, recursively: an object on both sides merges,
// anything else replaces. Used for the user's override of a shipped provider, where a
// whole-entry replacement would force the user to restate the endpoints they did not
// want to change.
func mergeUnder(dst, src *jsonx.OrderedMap) {
	for _, k := range src.Keys() {
		v, _ := src.Get(k)
		cur, ok := dst.Get(k)
		if ok {
			if dm, isMap := cur.(*jsonx.OrderedMap); isMap {
				if sm, isMap := v.(*jsonx.OrderedMap); isMap {
					mergeUnder(dm, sm)
					continue
				}
			}
		}
		dst.Set(k, v)
	}
}

// sortedEndpointProtocols returns an endpoint map's protocols sorted — a Go map has no
// order, and the composed table is serialized into an env var the derives read, so the
// order must be the same on every launch.
func sortedEndpointProtocols(endpoints map[string]packdecl.ProviderEndpoint) []string {
	out := make([]string, 0, len(endpoints))
	for proto := range endpoints {
		out = append(out, proto)
	}
	sort.Strings(out)
	return out
}

// sortedStringMapKeys is sortedEndpointProtocols for the env_shape's
// protocol → {VAR → placeholder} map.
func sortedStringMapKeys(m map[string]map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// providerClaimDetail describes a shipped provider in one footprint line: the protocols
// it names, how many model aliases, and — spelled out, because it is the fact a reader
// is checking for — that the credential is a variable NAME the user supplies.
func providerClaimDetail(endpoints map[string]packdecl.ProviderEndpoint, models map[string]string, apiKeyEnvName string) string {
	protos := sortedEndpointProtocols(endpoints)
	var parts []string
	if len(protos) > 0 {
		parts = append(parts, strconv.Itoa(len(protos))+" endpoint(s): "+strings.Join(protos, ", "))
	}
	if n := len(models); n > 0 {
		parts = append(parts, strconv.Itoa(n)+" model alias(es)")
	}
	if apiKeyEnvName != "" {
		parts = append(parts, "key from $"+apiKeyEnvName+" (user-supplied)")
	}
	if len(parts) == 0 {
		return "name only (no endpoints declared)"
	}
	return strings.Join(parts, "; ")
}

// orderedOrNil returns m, or nil when it is empty — so a launch with no provider from
// either source encodes exactly as it did before the kind existed.
func orderedOrNil(m *jsonx.OrderedMap) *jsonx.OrderedMap {
	if m.Len() == 0 {
		return nil
	}
	return m
}
