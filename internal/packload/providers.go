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
// `endpoints` (provider-table-fidelity.md §4.1, OQ-PT2). Each half is legal alone — the
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
	return orderedOrNil(out), nil
}

// addressConflict refuses a COMPOSED entry that carries both the base_url shorthand and
// an endpoints map. ComposeProviders calls it after every per-field merge, because the
// merge is what can produce the pair out of two inputs that each pass validation — the
// config validator's own refusal covers only an entry a user wrote whole (see
// packdecl.ProviderAddressConflictMessage for why the words are shared).
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

// ProviderFor returns the provider the profile active at CLI name `bin` selects: the
// `provider` of a profile declared under that name by a selected pack, or — when no pack
// declares one — the profile's own name, which is the convention the composed table has
// always keyed on (use_profiles.claude = "bedrock" reaching providers.bedrock).
//
// The declaration need NOT live on the pack that installs the bin. A provider pack
// usually installs no CLI at all (packs/zai is the shipped case), so keying this on the
// bin's owner would make its declaration unreachable and, with it, the whole
// deliver-the-provider-to-the-agent shape the kind exists for. The bin owner's own
// declaration wins when both declare (the agent's own pack is the more specific intent),
// then the first pack in delivery order, so the answer is stable rather than map-order.
//
// Empty when no profile is active, and the bare name when no declaration narrows it:
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
		if prof := p.Decl.ProfileFor(profile); prof != nil && prof.Provider != "" {
			return prof.Provider
		}
	}
	for _, p := range packs {
		if prof := p.Decl.ProfileFor(profile); prof != nil && prof.Provider != "" {
			return prof.Provider
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

// requiredProviders is what the COMPOSED CATALOG demands of this launch's environment
// (provider-catalog-and-selection.md §4, OQ-PT4): every entry of the composed providers
// table that is cataloged — present AND carrying at least one endpoint — in catalog order,
// attributed to the pack that shipped it, or to the user's config when no selected pack
// did.
//
// The rule is one sentence — in a dictionary means you need the key; not in one means you
// do not — and it replaces the pack-declaration walk this used to be, which required every
// provider a selected pack SHIPS and so refused a launch whose user had dropped that
// provider with `providers.<name>: null`: the pack still declared it, so the null bought a
// refusal naming the provider it had just removed (provider-table-fidelity.md §5.1, D4).
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
// (provider-catalog-and-selection.md §4, note). Merge-patch's own rule (RFC 7386 §2) is
// the same one, and for the same reason: a null member name defines the member's
// removal.
func mergeUnder(dst, src *jsonx.OrderedMap) {
	for _, k := range src.Keys() {
		v, _ := src.Get(k)
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
