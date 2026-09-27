package packload

// credentialscope.go is THE CREDENTIAL GATE (docs/design/provider-credential-scope.md,
// OQ-BR4 and OQ-CN1–OQ-CN6): the one function that decides which of a launch's composed
// environment values reach which agent. Its rule is the ruling's sentence — a profile's
// credentials and gated env reach ONLY the agent that selected it — applied to the three
// kinds of value a launch composes:
//
//   - an env_sources value whose name a composed provider CLAIMS (lists in its
//     `api_key_env_name`, OQ-CN1) reaches an agent only when that agent's selected
//     profile resolves to a claiming provider. An unclaimed value reaches everything, as
//     before: the gate scopes provider credentials, not the user's other variables;
//   - a `profile`-gated `kind: "env"` contribution reaches the agents its gate fires FOR
//     (gateFiresFor): an agent pack's own CLI when it selected the profile, and — for a
//     pack that installs no CLI, aws-auth's case — every agent that selected it. The
//     jail-wide "wide pass" (trap D2) is gone;
//   - a shape variable (an agent pack's env derive's output) is its agent's by
//     construction, and the derive's credential hydration reads through the same gated
//     lookup (LookupFor), so the rendered-config path narrows with the environment
//     (OQ-CN2's "both write paths").
//
// ONE GATE, TWO CALL SITES, and that is OQ-CN2 rather than a second implementation: the
// jail notch calls it from composePackChannel (internal/cli/run), whose result the
// container vehicle and the macos-user vehicle both read, and the host notch calls it from
// composeHostVars (internal/cli), which has never gone through composePackChannel because
// it composes from user scope only. The same arrangement EnvFold and AgentEnv already have.
//
// It decides; it writes nothing. Where each answer lands is the vehicle's business: a
// per-agent env file sourced by that agent's launcher on the container backends (OQ-CN6),
// the one launched agent's session on macos-user, the one exec'd process at the host notch.

import (
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// ScopeInput is everything the gate reads, all of it already composed by the caller.
type ScopeInput struct {
	Packs []*Pack
	// Providers is the launch's composed provider table (ComposeProviders): the claims are
	// read off it, so a user's override of a provider's api_key_env_name re-points the gate
	// exactly as it re-points the pre-flight.
	Providers *jsonx.OrderedMap
	// Profiles is the CLI-keyed effective selection (ProfileTable).
	Profiles map[string]string
	// Resolved is the launch's resolved profile table (ResolveProfiles).
	Resolved map[string]ResolvedProfile
	// EnvSources is the hydrated env_sources, in hydration order. Nil is an empty channel.
	EnvSources *jsonx.OrderedMap
	// Fallback answers a credential env_sources did not hydrate — the environment yolo was
	// launched from, which the env derive may relay. Consulted through the gate like
	// env_sources is, so a claimed name is withheld from another provider's agent whichever
	// channel holds it. Nil consults nothing.
	Fallback func(string) (string, bool)
	// NoDerives composes no shape vars: every delivery's Shape stays empty and no pack's
	// derive.lua runs. `yolo check` alone sets it — a read-only verb predicting a refusal
	// does not execute pack code (internal/cli/check/envoverrides.go) — so every launch
	// path, which leaves it false, gets the derives by default.
	NoDerives bool
	// Grants is the per-invocation GRANT (docs/design/credential-sources-separation.md
	// OQ-ES5, ruled for the host notch 2026-09-27): process name → the providers whose
	// claimed env_sources values that process ALSO receives. Keys only: a grant selects no
	// profile and no provider, so it runs no derive, fires no gated env, re-points nothing,
	// and the pre-flight asks nothing of it (SelectedProviders never lists a granted
	// provider). A process with a profile keeps it and receives the granted values beside
	// its own. Only `yolo host --with-credentials` passes one; nil is every jail launch, whose
	// answer is therefore unchanged.
	Grants map[string][]string
}

// CredentialScope is the gate's answer for one launch. Its accessors answer on a nil
// receiver too — the "no gate composed" world only a hand-built test channel has: nothing
// shared beyond what the caller passes itself, no agent, and every env_sources value
// delivered, which is what a launch without the gate did.
type CredentialScope struct {
	// claims maps a credential variable to the composed providers that list it, sorted.
	claims     map[string][]string
	envSources *jsonx.OrderedMap
	fallback   func(string) (string, bool)
	// sharedEnvSources is every env_sources entry no provider claims, in hydration order.
	sharedEnvSources *jsonx.OrderedMap
	// sharedPackEnv is the pack env fold with no gate satisfied: every selected pack's
	// unconditional `kind: "env"`.
	sharedPackEnv map[string]string
	// agents is each agent (CLI name) with a selected profile, and what only it receives.
	agents map[string]*AgentDelivery
}

// AgentDelivery is what one agent receives beyond the shared set.
type AgentDelivery struct {
	Agent    string
	Profile  string
	Provider string
	// Granted is the providers this process's grant names (ScopeInput.Grants), sorted and
	// deduplicated; empty without one. A grant-only process has no Profile and no Provider.
	Granted []string
	// EnvSources is the env_sources entries only this agent receives: the claimed
	// credentials of the provider its profile selects, and of every provider its grant
	// names, in hydration order.
	EnvSources *jsonx.OrderedMap
	// PackEnv is this agent's pack env fold where it differs from the shared fold — the
	// values of the gated contributions its selection satisfies.
	PackEnv map[string]string
	// Fold is this agent's whole fold sequence (EnvFold for the agent), for a vehicle that
	// composes one process from scratch (the host notch).
	Fold []EnvFoldEntry
	// Shape is its pack's env derive's output, composed through the gated lookup.
	Shape []agentenv.Var
}

// ScopeCredentials composes the gate's answer. A broken env derive is the one error, and
// it refuses the launch for AgentEnv's reason: this composition IS the delivery.
func ScopeCredentials(in ScopeInput) (*CredentialScope, error) {
	s := &CredentialScope{
		claims:           credentialClaims(in.Providers),
		envSources:       in.EnvSources,
		fallback:         in.Fallback,
		sharedEnvSources: jsonx.NewOrderedMap(),
		sharedPackEnv:    EnvVarsFor(in.Packs, in.Profiles, ""),
		agents:           map[string]*AgentDelivery{},
	}
	if s.envSources == nil {
		s.envSources = jsonx.NewOrderedMap()
	}
	for _, k := range s.envSources.Keys() {
		if _, claimed := s.claims[k]; claimed {
			continue
		}
		v, _ := s.envSources.Get(k)
		s.sharedEnvSources.Set(k, v)
	}
	for _, agent := range deliveryAgents(in.Profiles, in.Grants) {
		profile := in.Profiles[agent]
		d := &AgentDelivery{
			Agent:      agent,
			Profile:    profile,
			Granted:    sortedUnique(in.Grants[agent]),
			EnvSources: jsonx.NewOrderedMap(),
			PackEnv:    map[string]string{},
			Fold:       EnvFold(in.Packs, in.Profiles, agent),
		}
		if profile != "" {
			d.Provider = ProviderFor(in.Resolved, profile)
		}
		for _, k := range s.envSources.Keys() {
			if _, claimed := s.claims[k]; claimed && s.delivers(d, k) {
				v, _ := s.envSources.Get(k)
				d.EnvSources.Set(k, v)
			}
		}
		for _, e := range d.Fold {
			d.PackEnv[e.Key] = e.Value
		}
		for k, v := range d.PackEnv {
			if shared, ok := s.sharedPackEnv[k]; ok && shared == v {
				delete(d.PackEnv, k)
			}
		}
		s.agents[agent] = d
		// A grant-only process has no profile, so there is no derive to run for it: the
		// grant is keys only, never a shape.
		if in.NoDerives || profile == "" {
			continue
		}
		shape, err := AgentEnv(in.Packs, in.Providers, in.Profiles, agent, profile,
			s.LookupFor(agent), WithResolvedProfiles(in.Resolved))
		if err != nil {
			return nil, err
		}
		d.Shape = shape
	}
	return s, nil
}

// deliveryAgents is every process the gate composes a delivery for, sorted: each with a
// selected profile, and each a grant names.
func deliveryAgents(profiles map[string]string, grants map[string][]string) []string {
	set := map[string]string{}
	for agent, profile := range profiles {
		if profile != "" {
			set[agent] = ""
		}
	}
	for agent, providers := range grants {
		if len(providers) > 0 {
			set[agent] = ""
		}
	}
	return sortedMapKeys(set)
}

// sortedUnique is names sorted with duplicates dropped, nil for none.
func sortedUnique(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := append([]string(nil), names...)
	sort.Strings(out)
	return slices.Compact(out)
}

// ClaimingProviders is every composed provider that claims a name envSources holds, sorted:
// what a grant of `all` names (OQ-ES5 — "every provider in the composed table that claims a
// value"). The claims are the gate's own (credentialClaims), so a user's override of a
// provider's api_key_env_name moves this answer exactly as it moves the gate's.
func ClaimingProviders(providers, envSources *jsonx.OrderedMap) []string {
	claims := credentialClaims(providers)
	seen := map[string]string{}
	if envSources != nil {
		for _, k := range envSources.Keys() {
			for _, p := range claims[k] {
				seen[p] = ""
			}
		}
	}
	return sortedMapKeys(seen)
}

// GrantedProvider is what one granted provider delivered to a process: the names it claims
// that env_sources held, in hydration order, beside every name it claims, sorted. Delivered
// empty is the "no value" case a caller reports rather than skips.
type GrantedProvider struct {
	Provider  string
	Delivered []string
	Claims    []string
}

// GrantedTo is agent's grant as delivered, one entry per granted provider, sorted: nil when
// the process has no grant. Names only — never a value.
func (s *CredentialScope) GrantedTo(agent string) []GrantedProvider {
	if s == nil {
		return nil
	}
	d := s.agents[agent]
	if d == nil || len(d.Granted) == 0 {
		return nil
	}
	out := make([]GrantedProvider, 0, len(d.Granted))
	for _, p := range d.Granted {
		g := GrantedProvider{Provider: p}
		for name, claimants := range s.claims {
			if slices.Contains(claimants, p) {
				g.Claims = append(g.Claims, name)
			}
		}
		sort.Strings(g.Claims)
		for _, k := range s.envSources.Keys() {
			if slices.Contains(s.claims[k], p) {
				g.Delivered = append(g.Delivered, k)
			}
		}
		out = append(out, g)
	}
	return out
}

// credentialClaims maps each credential variable to the composed providers listing it.
func credentialClaims(providers *jsonx.OrderedMap) map[string][]string {
	out := map[string][]string{}
	if providers == nil {
		return out
	}
	for _, name := range providers.Keys() {
		for _, v := range CredentialEnvNames(providerEntry(providers, name)) {
			out[v] = append(out[v], name)
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// receives reports whether an agent whose profile selects provider may see variable name:
// always when no provider claims it, and otherwise only when provider is a claimant.
func (s *CredentialScope) receives(provider, name string) bool {
	claimants, claimed := s.claims[name]
	if !claimed {
		return true
	}
	if provider == "" {
		return false
	}
	for _, c := range claimants {
		if c == provider {
			return true
		}
	}
	return false
}

// delivers reports whether delivery d's process may see variable name: what its profile's
// provider receives, plus every name a provider its grant names claims. A nil delivery is a
// process with neither, which receives the unclaimed names only.
func (s *CredentialScope) delivers(d *AgentDelivery, name string) bool {
	if d == nil {
		return s.receives("", name)
	}
	if s.receives(d.Provider, name) {
		return true
	}
	for _, g := range d.Granted {
		if slices.Contains(s.claims[name], g) {
			return true
		}
	}
	return false
}

// LookupFor is the credential lookup agent's env derive composes through: the hydrated
// env_sources, then the fallback, and neither for a name another provider claims. It is
// what makes hydrateProviders write only the agent's own provider's api_key into the
// derive's copy of the table (OQ-CN2's rendered-config half). A grant does not widen it: a
// granted key reaches the process's environment and never its derive, so no derive can
// point the agent at a granted provider (the grant re-points nothing).
func (s *CredentialScope) LookupFor(agent string) func(string) (string, bool) {
	provider := ""
	if d := s.agents[agent]; d != nil {
		provider = d.Provider
	}
	return func(name string) (string, bool) {
		if !s.receives(provider, name) {
			return "", false
		}
		if v, ok := s.envSources.Get(name); ok {
			if str, isStr := v.(string); isStr && str != "" {
				return str, true
			}
		}
		if s.fallback != nil {
			if v, ok := s.fallback(name); ok && v != "" {
				return v, true
			}
		}
		return "", false
	}
}

// SharedEnvSources is the env_sources every process may see: the unclaimed entries.
func (s *CredentialScope) SharedEnvSources() *jsonx.OrderedMap {
	if s == nil {
		return jsonx.NewOrderedMap()
	}
	return s.sharedEnvSources
}

// SharedPackEnv is the pack env every process may see: the unconditional contributions.
func (s *CredentialScope) SharedPackEnv() map[string]string {
	if s == nil {
		return nil
	}
	return s.sharedPackEnv
}

// Agents lists the agents with a delivery of their own, sorted.
func (s *CredentialScope) Agents() []string {
	if s == nil {
		return nil
	}
	return sortedMapKeys(agentKeys(s.agents))
}

// Agent returns one agent's delivery, nil when it selected no profile.
func (s *CredentialScope) Agent(agent string) *AgentDelivery {
	if s == nil {
		return nil
	}
	return s.agents[agent]
}

// SelectedProviders is every provider some agent's profile selects, sorted: the providers
// whose credentials this launch delivers to anybody, and so the only ones the credential
// pre-flight may demand a key for (OQ-CN3).
func (s *CredentialScope) SelectedProviders() []string {
	if s == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, d := range s.agents {
		if d.Provider != "" && !seen[d.Provider] {
			seen[d.Provider] = true
			out = append(out, d.Provider)
		}
	}
	sort.Strings(out)
	return out
}

// EnvSourcesFor is the env_sources one agent's process receives, in hydration order: the
// shared entries, its own provider's claimed ones and its grant's. An agent with no profile
// and no grant — or "" — receives the shared entries only.
func (s *CredentialScope) EnvSourcesFor(agent string) *jsonx.OrderedMap {
	if s == nil {
		return jsonx.NewOrderedMap()
	}
	d := s.agents[agent]
	out := jsonx.NewOrderedMap()
	for _, k := range s.envSources.Keys() {
		if s.delivers(d, k) {
			v, _ := s.envSources.Get(k)
			out.Set(k, v)
		}
	}
	return out
}

// DeliversEnvSource reports whether the hydrated env_sources entry name reaches SOME
// process of the launch: unclaimed, or claimed by a provider some agent selected.
func (s *CredentialScope) DeliversEnvSource(name string) bool {
	if s == nil {
		return true
	}
	if _, claimed := s.claims[name]; !claimed {
		return true
	}
	for _, d := range s.agents {
		if s.delivers(d, name) {
			return true
		}
	}
	return false
}

// DeliveredPackEnv is the value a pack env key reaches some process with: the shared
// fold's, or else the first agent's (sorted) that receives it.
func (s *CredentialScope) DeliveredPackEnv(name string) (string, bool) {
	if s == nil {
		return "", false
	}
	if v, ok := s.sharedPackEnv[name]; ok {
		return v, true
	}
	for _, agent := range s.Agents() {
		if v, ok := s.agents[agent].PackEnv[name]; ok {
			return v, true
		}
	}
	return "", false
}

// DeliveredShape is the value some agent's env derive composed for name, the first agent
// (sorted) that set it.
func (s *CredentialScope) DeliveredShape(name string) (string, bool) {
	if s == nil {
		return "", false
	}
	for _, agent := range s.Agents() {
		for _, v := range s.agents[agent].Shape {
			if v.Key == name && !v.Unset {
				return v.Value, true
			}
		}
	}
	return "", false
}

// Empty reports whether a delivery carries nothing beyond the shared set.
func (d *AgentDelivery) Empty() bool {
	if d == nil {
		return true
	}
	return d.EnvSources.Len() == 0 && len(d.PackEnv) == 0 && len(d.Shape) == 0
}

// Disclosure words what the gate did with the credentials the user configured, names
// only — never a value. §4's "no silent narrowing": a launch that withholds a credential
// says so, and one that scopes it says to whom. Nil when env_sources hydrated no claimed
// name, which is every launch that configured no provider credential.
//
// It is the jail notch's wording: DisclosureWith and no notes.
func (s *CredentialScope) Disclosure() []string {
	return s.DisclosureWith(DisclosureNotes{})
}

// DisclosureNotes is what one notch adds to the gate's disclosure, for facts the gate cannot
// know: how a withheld credential can be received there, and what the launched process already
// holds. The zero value adds nothing, and is the jail's wording, which
// docs/design/credential-sources-separation.md ES-D2 leaves unchanged until OQ-ES5 decides
// whether a jail shell has a remedy at all.
type DisclosureNotes struct {
	// Remedy words how a withheld group's names can be received, given the providers that
	// claim them (sorted), as a sentence appended to that group's line. "" appends nothing.
	// The host notch names its typed `-p` (ES-D2).
	Remedy func(claimants []string) string
	// Inherited reports whether the process this launch composes holds name, non-empty, from
	// outside yolo — the host notch's invoking shell, which passes through untouched (CN-D13).
	// A withheld name it holds is not withheld from that process, so its line says yolo did
	// not add it, never "withheld", and names no remedy (ES-D4). Nil holds nothing: the
	// jail's case, where a host shell's value never crosses raw.
	Inherited func(name string) bool
	// Composed reports whether the process this launch composes holds name, non-empty, from a
	// value yolo composed from another source than env_sources — a pack's `env` of the same
	// name, say. A withheld name it holds is not absent from that process either, so its line
	// says the env_sources value was not delivered and the process holds another source's,
	// never "withheld from every process". It keeps the remedy, because a delivered
	// env_sources value beats that source's. Inherited is asked first. Nil holds nothing: the
	// jail's case, whose wording ES-D6 keeps.
	Composed func(name string) bool
}

// DisclosureWith is Disclosure with a notch's notes applied. Names are grouped by claimant,
// recipients and, under Inherited and Composed, whether and whence the process already holds
// them, so each line tells one story.
func (s *CredentialScope) DisclosureWith(notes DisclosureNotes) []string {
	if s == nil {
		return nil
	}
	type group struct {
		claimants  []string
		providers  string
		recipients string
		held       string // "", "inherited" or "composed"
		names      []string
	}
	var order []string
	groups := map[string]*group{}
	for _, k := range s.envSources.Keys() {
		claimants, claimed := s.claims[k]
		if !claimed {
			continue
		}
		var recipients []string
		for _, agent := range s.Agents() {
			if s.delivers(s.agents[agent], k) {
				recipients = append(recipients, agent)
			}
		}
		g := &group{claimants: claimants, providers: strings.Join(claimants, ", "),
			recipients: strings.Join(recipients, ", ")}
		// Only a withheld name can be misreported by the process's own copy: a delivered one
		// is the env_sources value, which beats it.
		if g.recipients == "" {
			switch {
			case notes.Inherited != nil && notes.Inherited(k):
				g.held = "inherited"
			case notes.Composed != nil && notes.Composed(k):
				g.held = "composed"
			}
		}
		key := g.providers + "\x00" + g.recipients + "\x00" + g.held
		if existing, ok := groups[key]; ok {
			existing.names = append(existing.names, k)
			continue
		}
		g.names = []string{k}
		groups[key] = g
		order = append(order, key)
	}
	if len(order) == 0 {
		return nil
	}
	lines := []string{"Credential scope: a provider's credential reaches only the agents " +
		"whose profile selects it."}
	for _, key := range order {
		g := groups[key]
		names := strings.Join(g.names, ", ")
		switch {
		case g.held == "inherited":
			lines = append(lines, "  "+names+" (provider "+g.providers+"): not added by yolo — "+
				"no agent in this launch selected it, so the invoking shell's own value passes through")
		case g.recipients == "":
			line := "  " + names + " (provider " + g.providers + "): withheld from " +
				"every process — no agent in this launch selected it"
			if g.held == "composed" {
				line = "  " + names + " (provider " + g.providers + "): not delivered from " +
					"env_sources — no agent in this launch selected it — so the value the process " +
					"holds is one yolo composed from another source, such as a pack's env"
			}
			if notes.Remedy != nil {
				if remedy := notes.Remedy(g.claimants); remedy != "" {
					line += ". " + remedy
				}
			}
			lines = append(lines, line)
		default:
			lines = append(lines, "  "+names+" (provider "+g.providers+"): "+g.recipients+" only")
		}
	}
	return lines
}

// CredentialEnvNames returns every credential variable a composed provider entry names
// (OQ-CN1): its `api_key_env_name`, a string or a list. Nil for an entry naming none, or
// naming something that is not a variable name list.
func CredentialEnvNames(entry *jsonx.OrderedMap) []string {
	if entry == nil {
		return nil
	}
	v, ok := entry.Get("api_key_env_name")
	if !ok || v == nil {
		return nil
	}
	names, ok := packdecl.EnvNamesFromValue(v)
	if !ok {
		return nil
	}
	return names
}

// KeyEnvName is the ONE variable a composed provider entry points an agent at: its
// api_key_env_name when that names exactly one, "" otherwise (packdecl.EnvNames.KeyPointer
// says why several point at none). Every consumer that writes a single key reference — the
// credential pre-flight, the wire bridge, the derives' view of the table — reads this.
func KeyEnvName(entry *jsonx.OrderedMap) string {
	names := CredentialEnvNames(entry)
	if len(names) == 1 {
		return names[0]
	}
	return ""
}

// ProvidersForDerive is the composed table as a derive reads it: each entry's
// api_key_env_name projected to KeyEnvName — the one variable, or absent — so every
// derive keeps reading a string it can splice into `${…}`, and the list stays the gate's.
// It copies what it changes; the table the launch relays is never mutated.
func ProvidersForDerive(providers *jsonx.OrderedMap) *jsonx.OrderedMap {
	if providers == nil {
		return nil
	}
	out := jsonx.NewOrderedMap()
	for _, name := range providers.Keys() {
		v, _ := providers.Get(name)
		entry, ok := v.(*jsonx.OrderedMap)
		if !ok || entry == nil {
			out.Set(name, v)
			continue
		}
		if _, has := entry.Get("api_key_env_name"); !has {
			out.Set(name, entry)
			continue
		}
		cp := jsonx.NewOrderedMap()
		pointer := KeyEnvName(entry)
		for _, k := range entry.Keys() {
			if k == "api_key_env_name" {
				if pointer != "" {
					cp.Set(k, pointer)
				}
				continue
			}
			ev, _ := entry.Get(k)
			cp.Set(k, ev)
		}
		out.Set(name, cp)
	}
	return out
}

// agentKeys lowers the delivery map to its key set for sortedMapKeys.
func agentKeys(m map[string]*AgentDelivery) map[string]string {
	out := make(map[string]string, len(m))
	for k := range m {
		out[k] = ""
	}
	return out
}
