package packload

// Provider composition and preflight: docs/reference/providers.md

// providers.go composes the PROVIDERS table a launch feeds its derives: the packs'
// shipped `kind: "provider"` service facts, laid UNDER the user's `providers` config
// entries (docs/reference/providers.md#how-the-table-composes, #pv-oq-12).
//
// The composition happens HERE, in the host CLI, and exactly once per launch: its output
// is what crosses to the jail as YOLO_PROVIDERS, and the in-jail side reads that table
// verbatim (entrypoint.LoadProviders → liveTables → ctx.providers). Composing anywhere
// later would mean a second implementation of the merge in the entrypoint — the drift the
// one-composition rule exists to prevent — and composing nowhere would make the kind a
// schema the derived configs never see.

import (
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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
// the same convention the `providers` config key already has for a null entry — and a null
// NESTED in an entry is a delete too, at every depth (see mergeUnder), so an alias can be
// removed without restating the aliases around it.
//
// The merge REFUSES a composed entry that ends up carrying both `base_url` and
// `endpoints` (docs/reference/providers.md, OQ-PT2). Each half is legal alone — the
// config validator takes them one at a time — but composed they are the pair it refuses,
// and the consumers genuinely disagree about which wins: the derives prefer the shorthand
// and fall back to `endpoints`, agentenv reads `endpoints` only. Per-field composition
// would hand a user who wrote `base_url` over z.ai two different addresses, split by
// consumer, silently. The refusal names both sources — the pack that shipped the
// endpoints and the config key that shipped the shorthand — rather than picking a winner
// and leaving the two consumers to disagree; the override the user wanted is still
// spellable, as `endpoints.<protocol>.base_url`. A pack cannot start this: the manifest
// schema has no entry-level `base_url` to ship (ProviderContribution), so a pack-only
// entry can never carry the pair and only a user key can add the shorthand.
//
// THE OUTPUT IS OWNED BY THE TABLE. No object reachable from the returned map is one the
// caller passed in or one a pack's declaration holds — the user layer is deep-copied on
// the way in (below) and shippedProviderEntry allocates every level it emits. That is what
// makes the adapter pass, which writes into the finished table, a writer of this table
// alone; it was not true, and the consequence was a bound port
// (docs/reference/wire-bridge.md#the-invariant-that-keeps-the-adapters-address-out-of-the-users-map).
//
// A provider NAME claimed by two packs is refused by the launch pre-flight (the kind is
// sole-owned by name; the claim target is the bare name, so packload.Collisions' generic
// exclusive loop reports it). A caller that skipped the pre-flight (the host) gets the LATER
// shipper's entry in packs' order, the one rule for a duplicated sole-owned claim (laterWins,
// notch-convergence NC-D59); the entry stands at its holder's position.
func ComposeProviders(user *jsonx.OrderedMap, packs []*Pack, opts ...ComposeOption) (*jsonx.OrderedMap, error) {
	cfg := composeOpts{}
	for _, opt := range opts {
		opt(&cfg)
	}
	out := jsonx.NewOrderedMap()
	shipper := map[string]string{}
	providerSupplied := map[string]bool{}
	type shipped struct {
		pack string
		prov packdecl.ProviderContribution
	}
	var all []shipped
	for _, p := range packs {
		for _, prov := range p.Decl.Providers() {
			all = append(all, shipped{pack: p.Name, prov: prov})
		}
	}
	holds := laterWins(len(all), func(i int) string { return all[i].prov.Name })
	for i, s := range all {
		if !holds[i] {
			continue
		}
		out.Set(s.prov.Name, shippedProviderEntry(s.prov))
		shipper[s.prov.Name] = s.pack
		providerSupplied[s.prov.Name] = s.prov.Models != nil
	}
	// THE `models` CONTRIBUTIONS, between the packs' own providers and the user's entries, so
	// the engineer's own `providers.<name>.models` writes last (OQ-BR12; modellists.go).
	contributed := applyModelContributions(out, user, packs, cfg.modelNote)
	for name := range contributed {
		providerSupplied[name] = true
	}
	reportPresence := func() {
		if cfg.modelListPresence == nil {
			return
		}
		for _, name := range out.Keys() {
			v, _ := out.Get(name)
			entry, ok := v.(*jsonx.OrderedMap)
			if !ok {
				continue
			}
			supplied := providerSupplied[name]
			if user != nil {
				if raw, ok := user.Get(name); ok {
					if userEntry, ok := raw.(*jsonx.OrderedMap); ok {
						if models, has := userEntry.Get("models"); has {
							// A final explicit null removes prior presence unless an `only` still
							// marks the list as deliberately narrowed to nothing.
							supplied = models != nil
						}
					}
				}
			}
			if modelOnly, _ := entry.Get(ModelsOnlyKey); modelOnly == true {
				supplied = true
			}
			if models, has := entry.Get("models"); has && models != nil {
				supplied = true
			}
			cfg.modelListPresence(name, supplied)
		}
	}
	if user == nil {
		// The adapter pass runs on EVERY return, not only the one with a user layer: a
		// launch whose providers are entirely pack-shipped is the common bridged case, and
		// an early return that skipped it would leave exactly that launch unresolved.
		liftModelFacts(out)
		adaptEndpoints(out, packs, cfg)
		reportPresence()
		return orderedOrNil(out), nil
	}
	for _, name := range user.Keys() {
		v, _ := user.Get(name)
		if v == nil {
			// A null entry disables — including a pack-shipped one. The user's config is
			// the override layer, and "override" has always included "no".
			out.Delete(name)
			continue
		}
		// THE COMPOSED TABLE HOLDS NOTHING THE CALLER OWNS. Every call site hands this
		// function a sub-map of a config map it goes on reading — run.composedProviders
		// and cli.composedHostProviders pass `cfg`'s own `providers` entry, and
		// check.protocolPairingGap passes the merged map's — so a composed value stored
		// by reference makes this a WRITER of its caller's config. adaptEndpoints runs
		// LAST over the finished table and writes endpoints.<protocol>.base_url through
		// any such alias, which is how a user's provider entry acquired the wire bridge's
		// own 127.0.0.1:8214 and run.localProviderForwards then read it back as a
		// host-loopback forward the user had asked for — a port bound in the jail four
		// lines before the bridge tried to bind it (docs/reference/wire-bridge.md#oq-pc1:
		// the ruling is to fix the mutation, not the read).
		//
		// The copy is DEEP because the value is a tree: a one-level clone would leave
		// `endpoints` shared and addEndpoint would write through it unchanged. One copy
		// here covers all four sinks below — the malformed passthrough, both
		// out.Set(name, u) paths, and mergeUnder, which sets sub-values of u into the
		// pack-shipped entry.
		v = jsonx.DeepCopy(v)
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
		cm, ok := cur.(*jsonx.OrderedMap)
		if !ok {
			out.Set(name, u)
			continue
		}
		dropRepointedVendors(cm, u)
		mergeUnder(cm, u)
		if err := addressConflict(name, cm, shipper[name]); err != nil {
			return nil, err
		}
	}
	// LAST, over the finished table (protocol-resolution.md, outcome 2). An adapter
	// contributes an address for a protocol a provider does not offer, and it is applied
	// here rather than at delivery because the composed table is what every consumer of an
	// address reads — each agent's derive, and the adapter's own daemon deciding where to
	// listen. Below the user layer so an explicit `endpoints.<protocol>.base_url` always
	// wins: an adapter fills a hole, and a user who wrote an address did not leave one.
	liftModelFacts(out)
	adaptEndpoints(out, packs, cfg)
	reportPresence()
	return orderedOrNil(out), nil
}

// liftModelFacts lowers every OBJECT-form `models.<alias>` entry in the finished table into
// the two things its consumers already read: `models.<alias>` becomes the bare wire id
// (the stable alias->id contract all six derives and the selection code were written
// against), and the entry's facts move to a sibling `model_options.<alias>` map in the same
// FLAT, string-valued option vocabulary as the provider's `options` — so a per-alias fact
// and a provider-wide fallback are one parser (packs/pi/derive.lua), and no reader learns
// two shapes. It runs on EVERY return path, the adapter pass's rule: a pack-only launch
// still composes through here, and normalization that only ran on the user path would be a
// shape the pack layer could never produce anyway. When a pack already shipped
// per-alias facts, lowering merges the user's object facts over them one field
// at a time, preserving facts for other aliases.
//
// This is the ONE lowering site. A string alias is left untouched (the shorthand), and a
// provider whose objects declare only `id` gets no `model_options` key at all.
func liftModelFacts(providers *jsonx.OrderedMap) {
	if providers == nil {
		return
	}
	for _, name := range providers.Keys() {
		v, _ := providers.Get(name)
		entry, ok := v.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		mv, ok := entry.Get("models")
		if !ok || mv == nil {
			continue
		}
		models, ok := mv.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		var lifted *jsonx.OrderedMap
		if prior, ok := entry.Get("model_options"); ok {
			lifted, _ = prior.(*jsonx.OrderedMap)
		}
		// model_routing is the one SIBLING map, not a member of the flat model_options: its
		// values are OBJECTS (pi's openRouterRouting, sent verbatim), where model_options is
		// the string-valued vocabulary every scalar fact lowers into. Keyed by the same alias,
		// which is where the pi model-lists derive reads a row's facts.
		var routing *jsonx.OrderedMap
		if prior, ok := entry.Get("model_routing"); ok {
			routing, _ = prior.(*jsonx.OrderedMap)
		}
		for _, alias := range models.Keys() {
			raw, _ := models.Get(alias)
			obj, ok := raw.(*jsonx.OrderedMap)
			if !ok {
				continue
			}
			id, _ := obj.Get("id")
			if _, isString := id.(string); !isString {
				// Malformed (config validation refuses a missing/non-string id); leave the
				// entry alone rather than lowering it to a null model id.
				continue
			}
			if rv, ok := obj.Get("openrouter_routing"); ok {
				if rm, isMap := rv.(*jsonx.OrderedMap); isMap && rm.Len() > 0 {
					if routing == nil {
						routing = jsonx.NewOrderedMap()
					}
					routing.Set(alias, jsonx.DeepCopy(rm))
				}
			}
			models.Set(alias, id)
			facts := flattenModelFacts(obj)
			if facts.Len() == 0 {
				continue
			}
			if lifted == nil {
				lifted = jsonx.NewOrderedMap()
			}
			if prior, ok := lifted.Get(alias); ok {
				if defaults, ok := prior.(*jsonx.OrderedMap); ok {
					mergeUnder(defaults, facts)
					continue
				}
			}
			lifted.Set(alias, facts)
		}
		if lifted != nil {
			entry.Set("model_options", lifted)
		}
		if routing != nil {
			entry.Set("model_routing", routing)
		}
	}
}

// flattenModelFacts renders one object-form model's facts as the flat option vocabulary
// (`reasoning: "true"`, `input: "text,image"`, `cost_input: "0.3"`, ...) so the consuming
// derive parses them with the same helpers it uses on `options`. `id` is the caller's, and
// every value here is already schema-checked (config.validateModelEntry); a field the
// object omits stays out, keeping the map additive.
func flattenModelFacts(obj *jsonx.OrderedMap) *jsonx.OrderedMap {
	facts := jsonx.NewOrderedMap()
	if v, ok := obj.Get("reasoning"); ok {
		if b, isBool := v.(bool); isBool {
			if b {
				facts.Set("reasoning", "true")
			} else {
				facts.Set("reasoning", "false")
			}
		}
	}
	if v, ok := obj.Get("input"); ok {
		if list, isList := v.([]any); isList {
			parts := make([]string, 0, len(list))
			for _, item := range list {
				if s, isString := item.(string); isString {
					parts = append(parts, s)
				}
			}
			if len(parts) > 0 {
				facts.Set("input", strings.Join(parts, ","))
			}
		}
	}
	if v, ok := obj.Get("cost"); ok {
		if cost, isMap := v.(*jsonx.OrderedMap); isMap {
			for _, row := range []struct{ key, option string }{
				{"input", "cost_input"},
				{"output", "cost_output"},
				{"cache_read", "cost_cache_read"},
				{"cache_write", "cost_cache_write"},
			} {
				if rate, has := cost.Get(row.key); has {
					facts.Set(row.option, numberString(rate))
				}
			}
		}
	}
	for _, key := range []string{"context_window", "max_tokens"} {
		if v, ok := obj.Get(key); ok {
			facts.Set(key, numberString(v))
		}
	}
	for _, key := range []string{"name", "description"} {
		if v, ok := obj.Get(key); ok {
			facts.Set(key, v)
		}
	}
	// The model's maker (docs/design/bedrock-plumbing.md OQ-BR9), lowered to the same flat key
	// a pack's model_options declares it under, so a user's entry and a shipped one are read by
	// one helper in each derive that filters on it.
	if v, ok := obj.Get("vendor"); ok {
		facts.Set("vendor", v)
	}
	// The pi CATALOG base a variant row inherits from (docs/reference/providers.md §"Per-model
	// OpenRouter routing"): a string, so it rides the same flat map the derives read.
	if v, ok := obj.Get("base"); ok {
		if s, isString := v.(string); isString && s != "" {
			facts.Set("base", s)
		}
	}
	return facts
}

// dropRepointedVendors removes, from a pack-shipped entry about to take the user layer, the
// `vendor` of every alias the user points at a DIFFERENT model id. A shipped vendor names
// the maker of the id the pack put under that alias, not of the alias: kept over the user's
// id, it would declare that id the pack model's maker's, and both its readers act on it. The
// wire bridge routes on it (wirebridged.anthropicModelIDs sends an id declared "anthropic"
// untranslated to Bedrock's Anthropic Messages route, so a DeepSeek id there fails at AWS
// instead of being translated), and each derive's callableModels offers an entry that declares
// a maker only to the agents whose client can call that maker.
//
// The user's own entry is the one that can say whose model the new id is: an object-form
// `models.<alias>` takes `vendor` (config.knownModelKeys, bedrock-plumbing.md OQ-BR9), and
// liftModelFacts lowers it into model_options AFTER this drop, so a vendor the user declares
// is the one the alias ends with. A string-form alias declares none, and the id then carries
// no vendor at all: translated by the bridge, and offered to every agent.
//
// Only the vendor goes. The other shipped facts keep the per-field rule liftModelFacts
// states: a context window or a price roughly describes a replacement too, while a maker
// carried to another maker's id is simply false. Restating the pack's own id, as a string or
// as an object's `id`, is no re-pointing and keeps everything. The comparison is on the id
// exactly as spelled: core does not interpret an id, so a different spelling is a different
// id (the pack's X re-pointed to claude's X[1m] loses the vendor too). That errs the safe
// way: an id missing its vendor is only translated, where an id carrying a wrong one fails
// the request.
func dropRepointedVendors(shipped, user *jsonx.OrderedMap) {
	userModels := childMap(user, "models")
	shippedModels := childMap(shipped, "models")
	options := childMap(shipped, "model_options")
	if userModels == nil || shippedModels == nil || options == nil {
		return
	}
	for _, alias := range userModels.Keys() {
		raw, _ := userModels.Get(alias)
		if raw == nil {
			continue // the alias is deleted; mergeUnder drops it, and no id is left to misdescribe
		}
		userID, _ := raw.(string)
		if obj, isMap := raw.(*jsonx.OrderedMap); isMap {
			id, _ := obj.Get("id")
			userID, _ = id.(string)
		}
		shippedRaw, had := shippedModels.Get(alias)
		shippedID, _ := shippedRaw.(string)
		if !had || userID == shippedID {
			continue
		}
		facts := childMap(options, alias)
		if facts == nil {
			continue
		}
		facts.Delete("vendor")
		if facts.Len() == 0 {
			options.Delete(alias)
		}
	}
	if options.Len() == 0 {
		shipped.Delete("model_options")
	}
}

// childMap is m's object-valued member k, or nil for an absent or non-object one.
func childMap(m *jsonx.OrderedMap, k string) *jsonx.OrderedMap {
	v, _ := m.Get(k)
	c, _ := v.(*jsonx.OrderedMap)
	return c
}

// numberString renders a decoded JSON number as the decimal string the flat option
// vocabulary carries: an integer literal keeps its verbatim form (so 1048576 stays
// integral), a float uses jsonx's Python-repr form so 0.006 does not become 0.00600000001.
func numberString(v any) string {
	if lit, ok := jsonx.AsIntLiteral(v); ok {
		return lit
	}
	if f, ok := v.(float64); ok {
		return jsonx.FormatFloatRepr(f)
	}
	return ""
}

// adaptEndpoints applies every selected pack's declared adaptations to the composed table:
// for each provider, a protocol some selected AGENT speaks but the provider does not offer
// gains the adapter's address, when a selected adapter turns one of the provider's own
// protocols into it.
//
// IT IS WHAT A PACK AUTHOR WRITES BY HAND TODAY, COMPUTED. `packs/cerebras` used to
// declare `endpoints.anthropic: http://127.0.0.1:8214` — not a Cerebras address at all, but
// the loopback yolo's bridge listens on — so the provider manifest asserted a fact yolo
// then orchestrated a listener to make true. The output here is byte-identical and the
// WRITER moved: the adapter states its own address once, and every provider it can front
// gets it without naming it. That is what lets a USER-declared provider be bridged, which
// the hand-written form could not do at all (§2.3).
//
// GATED ON THE AGENTS THIS LAUNCH SELECTED, as a union over their declared protocols. The
// table is one table for every agent, so per-agent injection is not a thing it can express;
// the union is the honest form of "some agent here needs this wire". The consequence worth
// knowing is that a launch selecting only openai-speaking agents composes no anthropic
// address for anyone — which is right, and is what the adapter pack's own `needs` gate
// already says (`when_bins`).
//
// NOTHING IS OVERWRITTEN. A provider that offers the protocol itself keeps its own address:
// an adapter is never preferred over a native endpoint (§4.1), so a provider with a real
// anthropic route is reached directly even in a jail where the bridge is running.
func adaptEndpoints(table *jsonx.OrderedMap, packs []*Pack, cfg composeOpts) {
	addresses := cfg.adapterAddresses
	var adapters []Adaptation
	for _, a := range Adaptations(packs) {
		// A NOTCH THAT DOES NOT SERVE THE ADAPTATION'S SERVICE composes no address it serves
		// (WithServed): nothing there listens on it.
		if !cfg.adaptationServed(a) {
			continue
		}
		adapters = append(adapters, a)
	}
	if len(adapters) == 0 {
		return
	}
	wanted := spokenProtocols(packs)
	if len(wanted) == 0 {
		return
	}
	for _, name := range table.Keys() {
		v, _ := table.Get(name)
		entry, ok := v.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		offered := providerProtocols(entry)
		platform := entryString(entry, "platform")
		for _, a := range adapters {
			// A PROVIDER OF A PLATFORM THE ADAPTATION FRONTS offers its From by what it is, not
			// by an address (docs/design/wire-bridge-gateway.md WG-I39): the shipped `bedrock`
			// names a region and no endpoint, because a pack cannot know the region, and the wire
			// bridge composes runtime's URL from it. The address it gets here is marked for a
			// profile routing through the adaptation's service (ForViaKey), since every agent
			// with its own client for the platform keeps that client on any other profile.
			fronted := !offered[a.From] && platform != "" && a.Service != "" &&
				slices.Contains(a.FromPlatforms, platform)
			if !wanted[a.To] || offered[a.To] || (!offered[a.From] && !fronted) {
				continue
			}
			// THE USER'S ADDRESS WINS, and it is the only field of an adaptation they may
			// set (§6): the pair is the declaring pack's claim, and what a user needs to
			// move is the PORT — harmless on a container's private loopback, a real
			// collision on macos-user, where there is no network namespace and an adapter's
			// ports are host ports.
			address := a.Address
			if override, ok := addresses[AdapterKey(a.From, a.To)]; ok && override != "" {
				address = override
			} else if a.Service != "" && cfg.servedSet {
				// A service-served address answers at its SERVED address (ServedDaemons): the
				// declared one on a private namespace, a port the launcher picked on a shared
				// one. The user's override is not a declared address, so it never moves.
				address = cfg.served.ServedURL(address)
			}
			addEndpoint(entry, a.To, address, serviceCredentialEnv(a))
			if fronted {
				markForVia(entry, a.To, a.Pack)
			}
			offered[a.To] = true
		}
	}
}

// serviceCredentialEnv is the credential an agent sends to adaptation a's address: the
// caller token of the service that serves it (paths.ServiceCallerTokenEnv), "" for an
// adapter whose pack runs no service — a remote gateway or a proxy the user runs, whose
// credential is whatever the provider's own is.
//
// A SERVICE-SERVED ADDRESS TAKES THE SERVICE'S CREDENTIAL, NEVER THE PROVIDER'S. The service
// holds the provider's key itself (the wire bridge reads it from the served agent's env file)
// and needs none from the agent, and what it DOES need is proof that the caller is one of this
// launch's agents: on a jail sharing the host's loopback (`network.mode: host`, macos-user, a
// nested podman) the address is reachable from every host process (wire-bridge.md WB-D18). So
// the pointer is composed WITH the address, by the same pass, and a derive that reads the
// endpoint's credential sends the right one without knowing that a service exists (WB-D2).
func serviceCredentialEnv(a Adaptation) string {
	if a.Service == "" {
		return ""
	}
	return paths.ServiceCallerTokenEnv(a.Service)
}

// addEndpoint writes endpoints.<protocol>.base_url on a composed entry, creating the
// `endpoints` map when the provider had none of its own. It writes the base_url and, when
// credentialEnv is set, `api_key_env_name` beside it: the NAME of the variable holding the
// credential this endpoint takes, which a derive's hydrated table resolves into the
// endpoint's own `api_key` (hydrateProviders) and which wins over the provider's for an agent
// sent to this address. A name, never a value, so the relayed table stays secret-free (D8).
//
// No `wire_api`: that would be the adapter asserting which dialect it speaks, and what the
// adapter serves is the protocol it declared — the same shape a provider entry that names
// a protocol and leaves the dialect to the consumer's default already has.
func addEndpoint(entry *jsonx.OrderedMap, protocol, address, credentialEnv string) {
	v, ok := entry.Get("endpoints")
	endpoints, isMap := v.(*jsonx.OrderedMap)
	if !ok || !isMap {
		endpoints = jsonx.NewOrderedMap()
		entry.Set("endpoints", endpoints)
	}
	ep := jsonx.NewOrderedMap()
	ep.Set("base_url", address)
	if credentialEnv != "" {
		ep.Set("api_key_env_name", credentialEnv)
	}
	endpoints.Set(protocol, ep)
}

// markForVia writes ForViaKey, naming pack, on the endpoint adaptEndpoints just composed for
// protocol: the address is for a profile whose `via` names that pack alone.
func markForVia(entry *jsonx.OrderedMap, protocol, pack string) {
	v, _ := entry.Get("endpoints")
	endpoints, _ := v.(*jsonx.OrderedMap)
	if endpoints == nil {
		return
	}
	if ep, ok := endpoints.Get(protocol); ok {
		if m, isMap := ep.(*jsonx.OrderedMap); isMap {
			m.Set(ForViaKey, pack)
		}
	}
}

// forViaService is the pack an endpoint of entry is marked for (ForViaKey), the value a
// profile's `via` names, "" for an endpoint that is the provider's own or an ordinary adapter's.
func forViaService(entry *jsonx.OrderedMap, protocol string) string {
	v, _ := entry.Get("endpoints")
	endpoints, _ := v.(*jsonx.OrderedMap)
	if endpoints == nil {
		return ""
	}
	ep, _ := endpoints.Get(protocol)
	m, _ := ep.(*jsonx.OrderedMap)
	return entryString(m, ForViaKey)
}

// EndpointsForProfile is entry as an agent its profile routes through viaService (a pack name,
// ResolvedProfile.ViaFor's answer for that agent) sees it: entry itself when no endpoint is marked
// for a via (ForViaKey) other than viaService, and otherwise a copy without those endpoints, which
// are no address at all for that agent. An agent routed through no service passes "", so every
// marked endpoint goes. Used by the profile line (profileReach), which says where an agent's
// profile reaches it.
func EndpointsForProfile(entry *jsonx.OrderedMap, viaService string) *jsonx.OrderedMap {
	if entry == nil {
		return nil
	}
	v, _ := entry.Get("endpoints")
	endpoints, _ := v.(*jsonx.OrderedMap)
	if endpoints == nil {
		return entry
	}
	var drop []string
	for _, proto := range endpoints.Keys() {
		if s := forViaService(entry, proto); s != "" && s != viaService {
			drop = append(drop, proto)
		}
	}
	if len(drop) == 0 {
		return entry
	}
	out := jsonx.NewOrderedMap()
	for _, k := range entry.Keys() {
		val, _ := entry.Get(k)
		out.Set(k, val)
	}
	kept := jsonx.NewOrderedMap()
	for _, proto := range endpoints.Keys() {
		if !slices.Contains(drop, proto) {
			val, _ := endpoints.Get(proto)
			kept.Set(proto, val)
		}
	}
	if len(kept.Keys()) == 0 {
		out.Delete("endpoints")
	} else {
		out.Set("endpoints", kept)
	}
	return out
}

// spokenProtocols is the union of every protocol the selected packs' programs declare —
// the set of wires this launch has an agent for. A protocol nothing speaks is one no
// adapter needs to produce, which is what keeps the injection from putting an address in
// front of a jail that has no consumer for it.
func spokenProtocols(packs []*Pack) map[string]bool {
	out := map[string]bool{}
	for _, p := range packs {
		for _, bin := range p.InstallBins() {
			for _, proto := range p.Decl.SpokenProtocols(bin) {
				out[proto] = true
			}
		}
	}
	return out
}

// addressConflict refuses a COMPOSED entry that carries both the base_url shorthand and
// an endpoints map.
//
// ITS POPULATION SHRANK TO ONE PATH and it is not dead. The shorthand is REMOVED
// (protocol-resolution.md), so a config the HOST validated can no longer carry it at
// all — but a retired key is an ERROR ON THE HOST AND A WARNING IN A JAIL, because in-jail
// the config is the host-generated snapshot and refusing there would stop every nested
// launch over a key the in-jail user cannot fix at its source. A nested launch therefore
// still composes an entry whose user half carries the shorthand, and this is what refuses
// the ambiguity rather than handing two consumers two different addresses.
//
// shipper is the pack that shipped the entry the user's key merged under, "" when none
// did. It is what makes the refusal name both sources; a provider no pack shipped cannot
// reach here through a launch (the user-layer validator refuses the whole-entry pair),
// so an empty shipper names only what the composer can prove.
func addressConflict(name string, entry *jsonx.OrderedMap, shipper string) error {
	base, hasBase := entry.Get("base_url")
	ends, hasEnds := entry.Get("endpoints")
	if !(hasBase && base != nil && hasEnds && ends != nil) {
		return nil
	}
	msg := "composing the providers table produced an entry the config validator refuses:\n" +
		"  providers." + quoted(name) + ": " + packdecl.ProviderAddressConflictMessage
	if shipper != "" {
		msg += "\n  The endpoints are pack " + shipper + "'s; the base_url shorthand came " +
			"from your config's providers." + name + ".base_url."
	}
	msg += "\n  To re-point one protocol, write the URL under it: " +
		"providers." + name + ".endpoints.<protocol>.base_url"
	return errors.New(msg)
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

// NativeCapabilities returns the capability set declared for agent's BUILT-IN
// authentication source — the first-party login its CLI uses when no profile selects a
// provider for it (packdecl.Manifest.NativeCapabilities is the declaration).
//
// Discovered by BIN OWNERSHIP, which is the same rule AgentEnv finds an agent's env
// producer by: the one selected pack that installs the agent's CLI is the one that can
// speak for how that CLI authenticates on its own. A pack that installs nothing owns no
// built-in source, so a jail of pure declarative-facts packs answers nothing here.
//
// Returns nil when no selected pack installs agent, which is the same answer as "the
// pack installs it and declares no capabilities" — deliberately. Both mean the source
// claims no job, and a resolver that distinguished them would be deciding what an
// UNDECLARED source does, which is the one thing a declaration-driven rule must not do.
func NativeCapabilities(packs []*Pack, agent string) []string {
	if agent == "" {
		return nil
	}
	for _, p := range packs {
		if p.installsBin(agent) {
			return p.Decl.NativeCapabilities(agent)
		}
	}
	return nil
}

// requiredProviders is what the COMPOSED CATALOG demands of this launch's environment
// (docs/reference/providers.md, OQ-PT4): every entry of the composed providers
// table that is cataloged — present AND carrying at least one endpoint — in catalog order,
// attributed to the pack that shipped it, or to the user's config when no selected pack
// did.
//
// The rule is one sentence — in a dictionary means you need the key; not in one means you
// do not — and it replaces the pack-declaration walk this used to be, which required every
// provider a selected pack SHIPS and so refused a launch whose user had dropped that
// provider with `providers.<name>: null`: the pack still declared it, so the null bought a
// refusal naming the provider it had just removed (docs/reference/providers.md, D4).
// Keyed to the table instead, the null removes the requirement with the entry.
//
// "Carries at least one endpoint" is the launch-level union of the predicate each derive
// applies per agent — a provider enters an AGENT's catalog only when it has an endpoint
// for a protocol that agent speaks, so an entry with no endpoint at all (the shipped
// bedrock, which ships region and model facts and no endpoint) reaches no agent's catalog and
// no key can be demanded of it without refusing launches nothing was wrong with. This
// function cannot know which protocols a launch's agents speak, so it takes the union's
// conservative form: some endpoint is what being in a dictionary means here.
//
// A profile's `provider` creates NO requirement of its own. It selects a provider;
// one the catalog does not hold delivers nothing to any agent — no derive sees an entry,
// and the env derive composes nothing — so demanding its credential would be
// demanding a key for a delivery that cannot happen.
//
// Attribution is kept because it is the only actionable form: "pack zai requires provider
// zai" says where the entry came from, which "provider zai is missing a credential" does
// not; the user-config attribution covers an entry only the user's config put there.
func requiredProviders(packs []*Pack, providers *jsonx.OrderedMap) []providerRequirement {
	// Attributed to the pack whose entry the table holds: the LAST shipper (laterWins, as
	// ComposeProviders keeps it).
	shipper := map[string]string{}
	for _, p := range packs {
		for _, prov := range p.Decl.Providers() {
			shipper[prov.Name] = p.Name
		}
	}
	if providers == nil {
		return nil
	}
	var out []providerRequirement
	for _, name := range providers.Keys() {
		if !hasEndpoint(providerEntry(providers, name)) {
			continue
		}
		out = append(out, providerRequirement{pack: shipper[name], provider: name})
	}
	return out
}

// hasEndpoint reports whether one composed entry is cataloged — carrying at least one
// protocol endpoint. It is the predicate requiredProviders gates on, and the reason a
// region-addressed provider composes without ever becoming a requirement.
func hasEndpoint(entry *jsonx.OrderedMap) bool {
	if entry == nil {
		return false
	}
	v, ok := entry.Get("endpoints")
	if !ok {
		return false
	}
	eps, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return false
	}
	for _, proto := range eps.Keys() {
		if e, _ := eps.Get(proto); e != nil {
			return true
		}
	}
	return false
}

// providerRequirement is one cataloged provider requiredProviders collected: the entry's
// name, and the pack that shipped it — empty when only the user's config did.
type providerRequirement struct {
	pack     string
	provider string
}

// ProviderCredentialGaps is the SELECTED-PACK CREDENTIAL PRE-FLIGHT
// (docs/reference/providers.md#the-credential-preflight, #pv-oq-13; the requirement itself re-ruled
// by OQ-PT4): every provider the composed table CATALOGS — present and carrying an
// endpoint, per requiredProviders — must have the variable its api_key_env_name points at
// set in the launch environment, or the launch is refused. An entry the table does not
// hold is nobody's requirement: the user's null dropped it, or nothing ever put it there,
// and an agent that cannot reach the provider owes nobody a credential.
//
// lookup answers "is this variable set in what this launch would deliver" — the whole
// assembled environment, not one channel of it: on the host notch, the composed process env,
// which includes the shell yolo was launched from. The jail notch asks per agent instead
// (ProviderCredentialGapsTo), because no jail process inherits that shell. A cataloged provider that declares no api_key_env_name is checked
// for EXISTENCE only — an entry can be in an agent's dictionary without naming where its
// key lives — and a provider with no endpoint (Bedrock, whose credential is the ambient
// AWS chain yolo cannot inspect) is not required at all, because it reaches no catalog.
//
// consulted is what the caller asked for credentials — the env_sources entries it walked,
// the invoking environment, whatever this notch actually consults — and is quoted verbatim
// in the facts. Naming it is the providers.md#the-credential-preflight half of the ruling: env_sources fails open (a
// missing file warns and skips), so without this line the reader is told only that a key
// never arrived, not which channel was supposed to bring it.
//
// Returns the FACT lines, empty when every requirement is deliverable. No lead and no
// remedy: ProviderCredentialRefusal wraps them in both, the same at every notch, because
// the escape hatch (paths.AllowMissingProvidersEnv) is one a refusal must name and an
// override notice must not re-offer.
//
// NARROWED WITH THE CREDENTIAL GATE (OQ-CN3, ruled 2026-09-26;
// docs/reference/providers.md, the credential preflight): selected is the set of providers some
// agent's profile selects (CredentialScope.SelectedProviders), and a cataloged provider
// outside it is no requirement. The gate delivers a provider's credential only to an agent
// that selected it, so a key for a provider nobody selected is a key nobody will deliver,
// and a key nobody will deliver is not a missing credential — refusing the launch over one
// was the gate's own defect one layer up. That reopens the pack-scoping ruling above
// (#pv-oq-13) deliberately: what still refuses is a SELECTED provider whose key is absent,
// which is exactly the mysterious-first-request failure that ruling was about.
func ProviderCredentialGaps(packs []*Pack, providers *jsonx.OrderedMap, selected []string,
	lookup func(string) (string, bool), consulted []string) []string {
	return providerCredentialGaps(packs, providers, selected, nil, lookupGap(lookup), consulted)
}

// lookupGap is the launch-wide question: a credential variable is delivered when lookup finds
// it set, non-empty, anywhere in what the launch delivers.
func lookupGap(lookup func(string) (string, bool)) func(provider, keyName string) []string {
	return func(_, keyName string) []string {
		if v, ok := lookup(keyName); ok && v != "" {
			return nil
		}
		return []string{notSetGap}
	}
}

// notSetGap is the fact's tail for a credential no channel of the launch carries.
const notSetGap = "is not set in this launch's environment"

// ProviderCredentialGapsIn is ProviderCredentialGaps over the credential gate's own answer: the
// providers it demands are the gate's SelectedProviders — every entry of every active set
// (docs/design/active-provider-sets.md §4.5) — and a fact about a provider that is a later entry
// of some agent's set names that entry, its position and the agent (CredentialScope.SetPosition),
// so `-p pi=zai,openrouter` with no OPENROUTER_API_KEY says openrouter is second in pi's set
// rather than leaving the reader to wonder why a provider pi does not start on is required.
// The host notch calls this one and every jail arm the per-agent ProviderCredentialGapsTo; the
// list-taking form stays for a caller with no gate.
func ProviderCredentialGapsIn(packs []*Pack, providers *jsonx.OrderedMap, scope *CredentialScope,
	lookup func(string) (string, bool), consulted []string) []string {
	return providerCredentialGaps(packs, providers, scope.SelectedProviders(), scope.SetPosition,
		lookupGap(lookup), consulted)
}

// ProviderCredentialGapsTo is ProviderCredentialGapsIn asked PER AGENT, the form a notch calls
// whose agents do not inherit the environment yolo was launched from: the jail's, on a
// container, an attach and macos-user alike (docs/design/bedrock-plumbing.md BR-D2, "the
// composed jail environment is what reaches the agent"). Each agent whose active set holds a
// provider must receive that provider's key: reaches answers whether the variable reaches the
// agent through a channel of this notch, or its value does through the agent's own env derive
// (a relay, CredentialScope.Relays).
//
// stranded reports whether a variable nothing delivers is set in the environment yolo was
// launched from. Such a key reaches the agent by no channel, and the fact says so rather than
// "is not set", which the user, seeing it in their own shell, would read as false; the launch
// that counted it started opencode, pi and codex with no key, since each reads the variable
// itself and none relays it. Nil names nothing stranded.
func ProviderCredentialGapsTo(packs []*Pack, providers *jsonx.OrderedMap, scope *CredentialScope,
	reaches func(agent, name string) bool, stranded func(string) bool, consulted []string) []string {
	gap := func(provider, keyName string) []string {
		var missing, reached []string
		for _, agent := range scope.Agents() {
			if !slices.Contains(SetProvidersOf(scope.Agent(agent)), provider) {
				continue
			}
			if reaches(agent, keyName) {
				reached = append(reached, agent)
			} else {
				missing = append(missing, agent)
			}
		}
		switch {
		case len(missing) == 0:
			return nil
		case stranded != nil && stranded(keyName):
			return []string{"is set only in the environment yolo was launched from, which no process " +
				"of a jail inherits, and nothing relays it to " + andList(missing),
				"    that environment counts only for an agent whose env derive relays the key; deliver " +
					"it through an env_sources entry, which hands it to the agents on " + quoted(provider) + " alone"}
		case len(reached) > 0:
			return []string{"does not reach " + andList(missing) + ", although it reaches " + andList(reached)}
		default:
			return []string{notSetGap}
		}
	}
	return providerCredentialGaps(packs, providers, scope.SelectedProviders(), scope.SetPosition,
		gap, consulted)
}

// providerCredentialGaps is every form's body; position, when non-nil, names where a provider
// sits in an active set, "" for nowhere worth naming. gap answers nil for a provider whose key
// is delivered, else the fact's tail after "whose credential variable K " and any indented lines
// to print under the fact.
func providerCredentialGaps(packs []*Pack, providers *jsonx.OrderedMap, selected []string,
	position func(string) string, gap func(provider, keyName string) []string, consulted []string) []string {
	isSelected := make(map[string]bool, len(selected))
	for _, name := range selected {
		isSelected[name] = true
	}
	var facts []string
	for _, req := range requiredProviders(packs, providers) {
		if !isSelected[req.provider] {
			continue // nobody selected it, so the gate delivers its key to nobody
		}
		entry := providerEntry(providers, req.provider)
		keyName := KeyEnvName(entry)
		if keyName == "" {
			continue // cataloged and needs no single credential pointer
		}
		tail := gap(req.provider, keyName)
		if len(tail) == 0 {
			continue
		}
		who := "your config declares" // an entry only the user's config put in the table
		if req.pack != "" {
			who = "pack " + req.pack + " requires"
		}
		fact := "  • " + who + " provider " + quoted(req.provider) +
			", whose credential variable " + keyName + " " + tail[0]
		if position != nil {
			if where := position(req.provider); where != "" {
				fact += " (" + where + "; yolo never starts an agent on part of its set)"
			}
		}
		facts = append(facts, fact)
		facts = append(facts, tail[1:]...)
	}
	if len(facts) == 0 {
		return nil
	}
	where := "nothing — no env_sources entries are configured, and no inherited environment was consulted"
	if len(consulted) > 0 {
		where = strings.Join(consulted, ", ")
	}
	return append(facts, "  consulted for credentials: "+where)
}

// ProviderCredentialRefusal is the credential pre-flight's whole message, at EVERY notch: the
// verdict, ProviderCredentialGaps' facts under it, and the remedy — or, when held (the escape
// hatch paths.AllowMissingProvidersEnv is set), the override notice over the same facts. It
// reports whether the launch must stop, which lines alone cannot carry: the hatch turns a
// refusal into a LOUD CONTINUATION, and a caller that only looked at len(lines) would exit on
// the notice. Nil lines for no facts.
//
// ONE RENDERER, so the jail launcher and `yolo host --` print one refusal
// (docs/plans/notch-convergence.md item 14, row C7). The host used to print the first fact as
// its verdict and had no sentence saying what was refused; the two bodies now differ only in
// how each notch names itself (the host prefixes "yolo host: ", the jail bolds the verdict).
// The verdict is the only unindented line, which is what the jail's printProviderRefusal
// renders bold.
func ProviderCredentialRefusal(facts []string, held bool) (lines []string, refuse bool) {
	if len(facts) == 0 {
		return nil, false
	}
	if held {
		return append([]string{"Warning: " + paths.AllowMissingProvidersEnv +
			" is set — CONTINUING, with a selected pack's provider credential still missing. " +
			"Nothing was repaired: the agent's first request against that provider will " +
			"still fail."}, facts...), false
	}
	lines = append([]string{
		"Refusing to launch: a selected pack needs a provider this launch cannot deliver.",
	}, facts...)
	return append(lines, "  Put the variable in one of the consulted channels, or launch anyway with "+
		paths.AllowMissingProvidersEnv+"=1."), true
}

// providerEntry returns the entry m holds at key, or nil when m is nil, the key is
// absent, or the value is not an object — a null entry (the user's "drop this provider")
// and a malformed one read the same to a caller that only wants fields out of it.
func providerEntry(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if m == nil {
		return nil
	}
	v, ok := m.Get(key)
	if !ok || v == nil {
		return nil
	}
	e, _ := v.(*jsonx.OrderedMap)
	return e
}

// quoted wraps a provider name the way a refusal quotes a name from config.
func quoted(s string) string {
	return `"` + s + `"`
}

// shippedProviderEntry renders one pack's provider declaration as an entry of the
// providers table — the SAME shape a user-written entry has, because what consumes the
// table (the three derives) reads one schema.
//
// IT ALLOCATES EVERY LEVEL, and that is load-bearing rather than incidental: the composed
// table is written into afterwards (adaptEndpoints, and mergeUnder folding the user layer
// over this entry), so an entry that handed back a map the DECLARATION holds would let one
// launch's composition corrupt the manifest every later read of Pack.Decl sees. Nothing
// here can: ProviderContribution's fields are strings and maps of strings, copied by value
// into fresh OrderedMaps, and Capabilities is copied into a fresh []any.
func shippedProviderEntry(prov packdecl.ProviderContribution) *jsonx.OrderedMap {
	entry := jsonx.NewOrderedMap()
	// One name composes as the string every consumer has always read; several compose as a
	// list (OQ-CN1), the same shape a user's own entry spells it in — []any, the JSONC
	// decoder's, so the user layer's list REPLACES the pack's in mergeUnder rather than
	// meeting a type only pack defaults ever have.
	switch len(prov.APIKeyEnvName) {
	case 0:
	case 1:
		entry.Set("api_key_env_name", prov.APIKeyEnvName[0])
	default:
		names := make([]any, 0, len(prov.APIKeyEnvName))
		for _, n := range prov.APIKeyEnvName {
			names = append(names, n)
		}
		entry.Set("api_key_env_name", names)
	}
	// What service the provider is (OQ-BR2), under the key a user's own entry spells it, so a
	// user override of a shipped provider's platform is one field with one merge rule, and a
	// derive reads it off the entry it is handed (ctx.selected_platform).
	if prov.Platform != "" {
		entry.Set("platform", prov.Platform)
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
	if len(prov.ModelOptions) > 0 {
		options := jsonx.NewOrderedMap()
		aliases := make([]string, 0, len(prov.ModelOptions))
		for alias := range prov.ModelOptions {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		for _, alias := range aliases {
			facts := jsonx.NewOrderedMap()
			for _, key := range sortedMapKeys(prov.ModelOptions[alias]) {
				facts.Set(key, prov.ModelOptions[alias][key])
			}
			options.Set(alias, facts)
		}
		entry.Set("model_options", options)
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
	// The capability set the provider declares for itself (§6.1 clause 1). It lands under
	// the key a USER's entry already spells — `providers.<name>.capabilities`, validated
	// by internal/config since before anything read it — so the pack default and the user
	// override are one field with one merge rule, not a second channel with a second
	// reader. []any and not []string because that is what the JSONC decoder produces for
	// the user's half: the two layers meet in mergeUnder, where a list REPLACES, and a
	// type the user's side cannot produce would be a shape only pack defaults ever have.
	if len(prov.Capabilities) > 0 {
		caps := make([]any, 0, len(prov.Capabilities))
		for _, c := range prov.Capabilities {
			caps = append(caps, c)
		}
		entry.Set("capabilities", caps)
	}
	// LAST, matching the order the design's own example spells the surface in (§5.2) —
	// the declared options are the profile half of the entry, after its service facts.
	if len(prov.Options) > 0 {
		entry.Set(optionsKey, providerOptionsEntry(prov.Options))
	}
	return entry
}

// mergeUnder folds src into dst IN PLACE, recursively: a null on the right DELETES the key
// it is under, an object on both sides merges, anything else replaces. Used for the user's
// override of a shipped provider, where a whole-entry replacement would force the user to
// restate the endpoints they did not want to change.
//
// The null is a delete, not a value — the same convention ComposeProviders applies to the
// entry itself, one level up. It has to hold at every depth or the override layer speaks
// two dialects of "no": `providers.zai: null` removes a provider, while
// `providers.zai.models.fast: null` composed a literal `"fast": null` — an alias whose
// value is nothing, which no reader of the composed table has a meaning for
// (docs/reference/providers.md, note). Merge-patch's own rule (RFC 7386 §2) is
// the same one, and for the same reason: a null member name defines the member's
// removal.
//
// ONE key steps outside that rule, and it is the one whose null has its own ruling:
// under `options`, a null means *declared, no default*, deliberately NOT the delete
// (docs/reference/providers.md §"How the table composes", the options-map carve-out:
// dropping a default must not un-declare the option a profile may name).
// mergeOptionDefaults is the whole of the exception — the map itself
// (`providers.zai.options: null`) still deletes, like every other field, because the
// ruling is about a value IN the map and not about the map.
func mergeUnder(dst, src *jsonx.OrderedMap) {
	for _, k := range src.Keys() {
		v, _ := src.Get(k)
		if k == optionsKey {
			if sm, isMap := v.(*jsonx.OrderedMap); isMap {
				cur, _ := dst.Get(k)
				if dm, isMap := cur.(*jsonx.OrderedMap); isMap {
					mergeOptionDefaults(dm, sm)
					continue
				}
				dst.Set(k, sm)
				continue
			}
			// Not an object on the right: fall through to the ordinary rule, so
			// `options: null` deletes the block and anything malformed replaces it —
			// the config validator has already reported the malformed shapes.
		}
		if v == nil {
			dst.Delete(k)
			continue
		}
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

// mergeOptionDefaults folds a user's `options` map over a composed one, flat: a string
// replaces, and a null LOWERS THE DEFAULT while keeping the option declared
// (docs/reference/providers.md §"How the table composes", the options-map carve-out).
// There is no recursion because the map is flat by schema — a nested value is refused by
// both producers (packdecl's decoder and config.validateProviderOptions), so it never
// reaches a composition.
//
// The null is the whole reason this is not mergeUnder: the delete convention would
// silently UNDECLARE the option, and a profile that then names it would be refused as
// undeclared — the provider offered it, the user only asked to drop its default, and the
// composition turned one into the other. Setting the null through keeps the census and
// the entry in agreement, which is the property providerOptionsEntry exists for.
func mergeOptionDefaults(dst, src *jsonx.OrderedMap) {
	for _, k := range src.Keys() {
		v, _ := src.Get(k)
		dst.Set(k, v)
	}
}

// optionsKey is the one provider entry key whose VALUE-position null is not a delete.
// It lives beside the merge that has to honour that, because the merge is the only place
// the two spellings of the map meet.
const optionsKey = "options"

// providerOptionsEntry lowers a declaration's options map into the composed entry's
// spelling: option name → string default, or an explicit JSON null for an option
// declared with none (docs/reference/providers.md §"How the table composes": a null inside
// an options map is "declared, no default"). The null has to survive INTO the table rather
// than collapsing to an absent key, because the table is what providerOptions reads back —
// the census and the composed entry would otherwise disagree about whether the provider
// declared the option at all.
func providerOptionsEntry(options map[string]packdecl.OptionDefault) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	for _, name := range sortedOptionNames(options) {
		if d := options[name]; d.Defaulted {
			out.Set(name, d.Value)
			continue
		}
		out.Set(name, nil)
	}
	return out
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

// providerClaimDetail describes a shipped provider in one footprint line: the protocols
// it names, how many model aliases, and — spelled out, because it is the fact a reader
// is checking for — that the credential is a variable NAME the user supplies.
func providerClaimDetail(endpoints map[string]packdecl.ProviderEndpoint, models map[string]string, apiKeyEnvName packdecl.EnvNames) string {
	protos := sortedEndpointProtocols(endpoints)
	var parts []string
	if len(protos) > 0 {
		parts = append(parts, strconv.Itoa(len(protos))+" endpoint(s): "+strings.Join(protos, ", "))
	}
	if n := len(models); n > 0 {
		parts = append(parts, strconv.Itoa(n)+" model alias(es)")
	}
	switch len(apiKeyEnvName) {
	case 0:
	case 1:
		parts = append(parts, "key from $"+apiKeyEnvName[0]+" (user-supplied)")
	default:
		parts = append(parts, "credential from $"+strings.Join(apiKeyEnvName, ", $")+" (user-supplied)")
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

// serviceClaimDetail describes a contributed service in one footprint line: the
// daemon halves it declares (argv visible — the thing a reader of a daemon claim is
// checking for), and the endpoint file name when it publishes one. Ordered
// jail-half-first because that is the half this build executes.
func serviceClaimDetail(c packdecl.Contribution) string {
	var parts []string
	if c.JailDaemon != nil {
		parts = append(parts, "jail daemon ["+strings.Join(c.JailDaemon.Cmd, " ")+"]")
	}
	if c.HostDaemon != nil {
		// Run by a host or macos-user launch as a launch-owned child, for a pack yolo ships or a
		// local one, and refused for a fetched one (docs/design/host-notch-services.md OQ-HS4,
		// HS-D27); the footprint reports what the pack WANTS either way, so a reader of a
		// fetched pack's footprint sees the argv its launch would refuse.
		parts = append(parts, "host daemon ["+strings.Join(c.HostDaemon.Cmd, " ")+"] (runs at the host and on macos-user for a pack yolo ships or a local one; refused for a fetched pack)")
	}
	if c.Endpoint != "" {
		parts = append(parts, "endpoint /run/yolo-services/"+c.Endpoint)
	}
	return strings.Join(parts, "; ")
}
