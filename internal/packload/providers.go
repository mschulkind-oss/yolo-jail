package packload

// Provider composition and preflight: docs/reference/providers.md

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
	"errors"
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
// A provider NAME claimed by two packs is refused by the launch pre-flight (the kind is
// sole-owned by name; the claim target is the bare name, so packload.Collisions' generic
// exclusive loop reports it). This compose keeps the FIRST and never overwrites, so a
// caller that skipped the pre-flight degrades to a stable table rather than to whichever
// pack happened to sort last.
func ComposeProviders(user *jsonx.OrderedMap, packs []*Pack) (*jsonx.OrderedMap, error) {
	out := jsonx.NewOrderedMap()
	shipper := map[string]string{}
	for _, p := range packs {
		for _, prov := range p.Decl.Providers() {
			if _, seen := out.Get(prov.Name); seen {
				continue
			}
			out.Set(prov.Name, shippedProviderEntry(prov))
			shipper[prov.Name] = p.Name
		}
	}
	if user == nil {
		// The adapter pass runs on EVERY return, not only the one with a user layer: a
		// launch whose providers are entirely pack-shipped is the common bridged case, and
		// an early return that skipped it would leave exactly that launch unresolved.
		adaptEndpoints(out, packs)
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
		mergeUnder(cm, u)
		if err := addressConflict(name, cm, shipper[name]); err != nil {
			return nil, err
		}
	}
	// LAST, over the finished table (protocol-resolution.md §3, outcome 2). An adapter
	// contributes an address for a protocol a provider does not offer, and it is applied
	// here rather than at delivery because the composed table is what every consumer of an
	// address reads — each agent's derive, and the adapter's own daemon deciding where to
	// listen. Below the user layer so an explicit `endpoints.<protocol>.base_url` always
	// wins: an adapter fills a hole, and a user who wrote an address did not leave one.
	adaptEndpoints(out, packs)
	return orderedOrNil(out), nil
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
func adaptEndpoints(table *jsonx.OrderedMap, packs []*Pack) {
	adapters := Adaptations(packs)
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
		if len(offered) == 0 {
			continue
		}
		for _, a := range adapters {
			if !wanted[a.To] || offered[a.To] || !offered[a.From] {
				continue
			}
			addEndpoint(entry, a.To, a.Address)
			offered[a.To] = true
		}
	}
}

// addEndpoint writes endpoints.<protocol>.base_url on a composed entry, creating the
// `endpoints` map when the provider had none of its own. It writes ONLY the base_url: a
// `wire_api` here would be the adapter asserting which dialect it speaks, and what the
// adapter serves is the protocol it declared — the same shape a provider entry that names
// a protocol and leaves the dialect to the consumer's default already has.
func addEndpoint(entry *jsonx.OrderedMap, protocol, address string) {
	v, ok := entry.Get("endpoints")
	endpoints, isMap := v.(*jsonx.OrderedMap)
	if !ok || !isMap {
		endpoints = jsonx.NewOrderedMap()
		entry.Set("endpoints", endpoints)
	}
	ep := jsonx.NewOrderedMap()
	ep.Set("base_url", address)
	endpoints.Set(protocol, ep)
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
// (protocol-resolution.md §5), so a config the HOST validated can no longer carry it at
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
	shipper := map[string]string{}
	for _, p := range packs {
		for _, prov := range p.Decl.Providers() {
			if _, seen := shipper[prov.Name]; !seen {
				shipper[prov.Name] = p.Name
			}
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

// entryString reads one string field out of a composed provider entry, "" when the
// entry is absent or the field is not a string. The composed table is what the derives
// read, so it is also what the pre-flight reads: a user override of api_key_env_name
// re-points the check at the variable the launch would actually have hydrated.
func entryString(entry *jsonx.OrderedMap, key string) string {
	if entry == nil {
		return ""
	}
	v, ok := entry.Get(key)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// ProviderCredentialGaps is the SELECTED-PACK CREDENTIAL PRE-FLIGHT
// (profiles-as-pack-variants.md §6.2 as rescoped by OQ-13; the requirement itself re-ruled
// by OQ-PT4): every provider the composed table CATALOGS — present and carrying an
// endpoint, per requiredProviders — must have the variable its api_key_env_name points at
// set in the launch environment, or the launch is refused. An entry the table does not
// hold is nobody's requirement: the user's null dropped it, or nothing ever put it there,
// and an agent that cannot reach the provider owes nobody a credential.
//
// lookup answers "is this variable set in what this launch would deliver" — the whole
// assembled environment, not one channel of it. On the jail notch that is the env_sources
// hydration, the -e pairs of the assembled argv, and the environment yolo itself was
// launched from (which the env derive can draw on); on the host notch it is the
// composed process env. A cataloged provider that declares no api_key_env_name is checked
// for EXISTENCE only — an entry can be in an agent's dictionary without naming where its
// key lives — and a provider with no endpoint (Bedrock, whose credential is the ambient
// AWS chain yolo cannot inspect) is not required at all, because it reaches no catalog.
//
// consulted is what the caller asked for credentials — the env_sources entries it walked,
// the invoking environment, whatever this notch actually consults — and is quoted verbatim
// in the facts. Naming it is the §6.1 half of the ruling: env_sources fails open (a
// missing file warns and skips), so without this line the reader is told only that a key
// never arrived, not which channel was supposed to bring it.
//
// Returns the FACT lines, empty when every requirement is deliverable. No lead and no
// remedy: the lead is the refusing notch's voice, and the remedy names the channels only
// that notch knows — including the escape hatch (paths.AllowMissingProvidersEnv), which a
// refusal must name and an override notice must not re-offer.
func ProviderCredentialGaps(packs []*Pack, providers *jsonx.OrderedMap,
	lookup func(string) (string, bool), consulted []string) []string {
	var facts []string
	for _, req := range requiredProviders(packs, providers) {
		entry := providerEntry(providers, req.provider)
		keyName := entryString(entry, "api_key_env_name")
		if keyName == "" {
			continue // cataloged and needs no credential pointer
		}
		if v, ok := lookup(keyName); ok && v != "" {
			continue
		}
		who := "your config declares" // an entry only the user's config put in the table
		if req.pack != "" {
			who = "pack " + req.pack + " requires"
		}
		facts = append(facts, "  • "+who+" provider "+quoted(req.provider)+
			", whose credential variable "+keyName+" is not set in this launch's environment")
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
// (docs/reference/providers.md OQ-CS7). mergeOptionDefaults is the whole of the
// exception — the map itself (`providers.zai.options: null`) still deletes, like every
// other field, because the ruling is about a value IN the map and not about the map.
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
// replaces, and a null LOWERS THE DEFAULT while keeping the option declared (OQ-CS7).
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
// declared with none (OQ-CS7). The null has to survive INTO the table rather than
// collapsing to an absent key, because the table is what providerOptions reads back —
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
		// Declared and carried, not executed (the field's own doc says why); the
		// footprint reports what the pack WANTS either way.
		parts = append(parts, "host daemon ["+strings.Join(c.HostDaemon.Cmd, " ")+"] (declared, not yet executed)")
	}
	if c.Endpoint != "" {
		parts = append(parts, "endpoint /run/yolo-services/"+c.Endpoint)
	}
	return strings.Join(parts, "; ")
}
