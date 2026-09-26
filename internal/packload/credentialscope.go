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
	// EnvSources is the env_sources entries only this agent receives: the claimed
	// credentials of the provider its profile selects, in hydration order.
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
	for _, agent := range sortedMapKeys(in.Profiles) {
		profile := in.Profiles[agent]
		if profile == "" {
			continue
		}
		d := &AgentDelivery{
			Agent:      agent,
			Profile:    profile,
			Provider:   ProviderFor(in.Resolved, profile),
			EnvSources: jsonx.NewOrderedMap(),
			PackEnv:    map[string]string{},
			Fold:       EnvFold(in.Packs, in.Profiles, agent),
		}
		for _, k := range s.envSources.Keys() {
			if _, claimed := s.claims[k]; claimed && s.receives(d.Provider, k) {
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
		if in.NoDerives {
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

// LookupFor is the credential lookup agent's env derive composes through: the hydrated
// env_sources, then the fallback, and neither for a name another provider claims. It is
// what makes hydrateProviders write only the agent's own provider's api_key into the
// derive's copy of the table (OQ-CN2's rendered-config half).
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
// shared entries and its own provider's claimed ones. An agent with no profile — or "" —
// receives the shared entries only.
func (s *CredentialScope) EnvSourcesFor(agent string) *jsonx.OrderedMap {
	if s == nil {
		return jsonx.NewOrderedMap()
	}
	provider := ""
	if d := s.agents[agent]; d != nil {
		provider = d.Provider
	}
	out := jsonx.NewOrderedMap()
	for _, k := range s.envSources.Keys() {
		if s.receives(provider, k) {
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
		if s.receives(d.Provider, name) {
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
func (s *CredentialScope) Disclosure() []string {
	if s == nil {
		return nil
	}
	type group struct {
		providers, recipients string
		names                 []string
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
			if s.receives(s.agents[agent].Provider, k) {
				recipients = append(recipients, agent)
			}
		}
		g := &group{providers: strings.Join(claimants, ", "), recipients: strings.Join(recipients, ", ")}
		key := g.providers + "\x00" + g.recipients
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
		if g.recipients == "" {
			lines = append(lines, "  "+names+" (provider "+g.providers+"): withheld from "+
				"every process — no agent in this launch selected it")
			continue
		}
		lines = append(lines, "  "+names+" (provider "+g.providers+"): "+g.recipients+" only")
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
