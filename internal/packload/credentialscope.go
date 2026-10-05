package packload

// credentialscope.go is THE CREDENTIAL GATE (docs/reference/providers.md,
// OQ-BR4 and OQ-CN1–OQ-CN6): the one function that decides which of a launch's composed
// environment values reach which agent. Its rule is the ruling's sentence — a profile's
// credentials and gated env reach ONLY the agent that selected it — applied to the three
// kinds of value a launch composes:
//
//   - an env_sources value whose name a composed provider CLAIMS (lists in its
//     `api_key_env_name`, OQ-CN1) reaches an agent only when that agent's selected
//     profile resolves to a claiming provider. An unclaimed value reaches everything, as
//     before: the gate scopes provider credentials, not the user's other variables;
//   - a gated `kind: "env"` contribution reaches the agents its gate fires FOR
//     (gateFiresFor): an agent pack's own CLI when its selection satisfies the gate, and —
//     for a pack that installs no CLI, aws-auth's case — every agent whose selection does.
//     A `platform` gate is satisfied by the selected provider's platform (OQ-BR8), so
//     aws-auth's pointer reaches every agent on a Bedrock provider, whatever its profile is
//     named. The jail-wide "wide pass" (trap D2) is gone;
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
// It decides; it writes nothing. Which source wins when several set one name for one process is
// decided here too, once (SharedEnv and EnvFor, envcompose.go), and every vehicle serializes that
// composition. Where each answer lands is the vehicle's business: a per-agent env file sourced by
// that agent's launcher on the container backends (OQ-CN6), the one launched agent's session on
// macos-user, the one exec'd process at the host notch.

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ScopeInput is everything the gate reads, all of it already composed by the caller.
type ScopeInput struct {
	Packs []*Pack
	// Providers is the launch's composed provider table (ComposeProviders): the claims are
	// read off it, so a user's override of a provider's api_key_env_name re-points the gate
	// exactly as it re-points the pre-flight.
	Providers *jsonx.OrderedMap
	// Profiles is the CLI-keyed effective selection (ProfileTable): each agent's primary.
	Profiles map[string]string
	// Sets is each agent's whole ACTIVE SET (ProfileSets; docs/design/active-provider-sets.md
	// §4.5), when the launch selects one: every entry's claimed key reaches that agent, and only
	// that agent. Its first entry must be the agent's Profiles entry. Nil, or an agent it does
	// not name, reads that agent's set as its one Profiles entry, which is every launch whose
	// sets all have one entry.
	Sets map[string][]string
	// Resolved is the launch's resolved profile table (ResolveProfiles).
	Resolved map[string]ResolvedProfile
	// EnvSources is the hydrated env_sources, in hydration order. Nil is an empty channel.
	EnvSources *jsonx.OrderedMap
	// EnvSourceRemovals are the names env_sources REMOVES, in order: each inline null no later
	// entry cancelled (config.ResolveEnvSourcesFull's second answer). Every process's composition
	// carries them at env_sources' rank (envcompose.go): a removal takes out the pack env fold's
	// value, and at the host the invoking shell's, and never a shape var. A removal carries no
	// value, so no claim scopes it. Nil removes nothing.
	EnvSourceRemovals []string
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
	// UnservedAdaptations are the conversions this notch can never serve
	// (UnservedAdaptationsAt, at a notch that serves no pack service: the host, macos-user): the ones its
	// composition left out, and the unselected shipped packs' of the same kind. Handed to every
	// derive's protocol gate (WithUnservedAdaptations), so a pairing only one of them would
	// resolve refuses as *UnservedAdapterError, saying why, and outcome 3 never offers one. Nil
	// at the jail notch, which runs its packs' services and composes their addresses.
	UnservedAdaptations []Adaptation
	// CallerTokens are this entry's pack-service caller tokens, keyed by the variable that
	// carries each (paths.ServiceCallerTokenEnv): what the launch minted, or the running
	// jail's own on an attach (docs/reference/wire-bridge.md, WB-D18). They are no provider's
	// credential, so no claim withholds them, and they answer FIRST in every agent's lookup:
	// the address a service serves names its token as its credential (serviceCredentialEnv),
	// and the derive of any agent sent there must read the value the service will demand,
	// never a same-named env_sources entry or a variable in the launching shell. Nil at the
	// host notch, which runs no pack service.
	CallerTokens map[string]string
	// Served is what this notch serves (ServedDaemons; docs/plans/notch-convergence.md §4
	// item 2). A pack `env` variable declared `served_by` a daemon not served here is
	// withheld from every process and named (UnservedEnvLines): an address nothing serves is
	// a dead pointer, and on a shared loopback a credential handed to whoever binds the port.
	// Nil composes as declared, every variable delivered: the shape of a caller that is not
	// composing for a notch. Every launch passes one.
	Served *ServedDaemons
	// RegionFiles is where this notch reads a platform's region file (regionfill.go,
	// docs/design/bedrock-plumbing.md BR-DIR1): an agent on a provider reached through a region
	// that receives none is given the one the file holds for its credential's profile. Nil reads
	// no file, the shape of a caller that is not composing a launch (`yolo check`, a test).
	RegionFiles *RegionFileSource
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
	// callerTokens is ScopeInput.CallerTokens.
	callerTokens map[string]string
	// sharedEnvSources is every env_sources entry no provider claims, in hydration order.
	sharedEnvSources *jsonx.OrderedMap
	// removals is ScopeInput.EnvSourceRemovals, which every composition carries (envcompose.go).
	removals []string
	// sharedFold is the pack env fold with no gate satisfied, in fold order, as this notch serves
	// it: every selected pack's unconditional `kind: "env"`. sharedPackEnv is its reduction.
	sharedFold    []EnvFoldEntry
	sharedPackEnv map[string]string
	// agents is each agent (CLI name) with a selected profile, and what only it receives.
	agents map[string]*AgentDelivery
	// packs, profiles and served are the inputs FoldFor re-folds an undelivered agent with.
	packs    []*Pack
	profiles map[string]string
	served   *ServedDaemons
	// sel is the gate's view of the selection (SelectionOf over the gate's own inputs).
	sel GateSelection
	// unservedEnv is every pack env variable withheld because its daemon is not served here,
	// keyed by variable, naming the daemon (ScopeInput.Served).
	unservedEnv map[string]string
	// unlistenedEnv is every pack env variable withheld because its value names
	// loopholedecl.TokenListen and the daemon it is served by has no served address — a pack
	// pointing at a loophole daemon that declares no `jail_daemon.listen`, or at another
	// pack's service, which has none (packdecl refuses a pack's own) — keyed by variable,
	// naming the daemon.
	unlistenedEnv map[string]string
	// untokenedEnv is every pack env variable withheld because its value names
	// loopholedecl.TokenCallerToken and this launch minted no caller token for the daemon it
	// is served by, keyed by variable, naming the daemon.
	untokenedEnv map[string]string
}

// AgentDelivery is what one agent receives beyond the shared set.
type AgentDelivery struct {
	Agent string
	// Profile and Provider are the agent's PRIMARY: its set's first entry, and the provider
	// that entry resolves to.
	Profile  string
	Provider string
	// Set is the agent's whole active set, the primary first, and Providers, index for index,
	// the provider each entry resolves to (docs/design/active-provider-sets.md §4.5). A set of
	// one is Profile and Provider alone; a grant-only process has neither.
	Set       []string
	Providers []string
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
	// Shape is its pack's env derive's output, composed through the gated lookup, then the
	// region the fill read from the platform's region file when nothing else gives the agent one
	// (RegionFile, regionfill.go): one list, so every vehicle that delivers the derive's output
	// delivers the region too.
	Shape []agentenv.Var
	// RegionFile is what the region fill read for this agent, nil when it needed nothing from
	// the file: the region it delivered, or why it delivered none, which the region pre-flight
	// names (RegionAsk.File).
	RegionFile *RegionFileLookup
}

// ScopeCredentials composes the gate's answer. A broken env derive is the one error, and
// it refuses the launch for AgentEnv's reason: this composition IS the delivery.
func ScopeCredentials(in ScopeInput) (*CredentialScope, error) {
	s := &CredentialScope{
		claims:           credentialClaims(in.Providers),
		envSources:       in.EnvSources,
		fallback:         in.Fallback,
		callerTokens:     in.CallerTokens,
		sharedEnvSources: jsonx.NewOrderedMap(),
		removals:         in.EnvSourceRemovals,
		agents:           map[string]*AgentDelivery{},
		packs:            in.Packs,
		profiles:         in.Profiles,
		served:           in.Served,
		// THE GATE'S VIEW OF THE SELECTION (OQ-BR8): each agent's profile and the platform of
		// the provider it resolves to, over this launch's own table, so every fold below and
		// every reader of Selection() answers a `platform` gate from one resolution. Over the
		// whole active set (AP-P1), so a gate any entry satisfies fires for that agent.
		sel: SelectionOfSets(in.setTable(), in.Resolved, in.Providers),
	}
	s.sharedFold = s.servedFold(EnvFold(in.Packs, s.sel, ""))
	for _, e := range s.sharedFold {
		if s.sharedPackEnv == nil {
			s.sharedPackEnv = map[string]string{}
		}
		s.sharedPackEnv[e.Key] = e.Value
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
			Fold:       s.servedFold(EnvFold(in.Packs, s.sel, agent)),
		}
		if profile != "" {
			d.Provider = ProviderFor(in.Resolved, profile)
			d.Set = in.setFor(agent)
			d.Providers = make([]string, len(d.Set))
			for i, name := range d.Set {
				d.Providers[i] = ProviderFor(in.Resolved, name)
			}
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
			s.LookupFor(agent), WithResolvedProfiles(in.Resolved),
			WithUnservedAdaptations(in.UnservedAdaptations), WithActiveSet(d.Set))
		if err != nil {
			return nil, err
		}
		d.Shape = shape
	}
	// THE REGION FILL, once every delivery is composed, since what already reaches an agent is
	// asked of its whole delivery (BR-DIR1).
	s.fillRegions(in)
	return s, nil
}

// setFor is agent's active set as the gate reads it: its Sets entry when that is a set whose
// first entry is the agent's primary, else the one Profiles entry. A Sets entry disagreeing with
// Profiles about the primary is a caller composing two tables from two merges, and the primary
// the rest of the gate reads wins.
func (in ScopeInput) setFor(agent string) []string {
	profile := in.Profiles[agent]
	if profile == "" {
		return nil
	}
	if set := in.Sets[agent]; len(set) > 0 && set[0] == profile {
		return set
	}
	return []string{profile}
}

// setTable is every agent's set (setFor), keyed like Profiles.
func (in ScopeInput) setTable() map[string][]string {
	out := make(map[string][]string, len(in.Profiles))
	for agent, profile := range in.Profiles {
		if profile != "" {
			out[agent] = in.setFor(agent)
		}
	}
	return out
}

// servedFold is fold with every entry whose `served_by` daemon is not served at this notch
// left out, each one recorded for UnservedEnvLines. With no served set (ScopeInput.Served nil)
// it is fold unchanged.
func (s *CredentialScope) servedFold(fold []EnvFoldEntry) []EnvFoldEntry {
	if s.served == nil {
		return fold
	}
	out := fold[:0:0]
	for _, e := range fold {
		if e.ServedBy != "" && !s.served.Serves(e.ServedBy) {
			if s.unservedEnv == nil {
				s.unservedEnv = map[string]string{}
			}
			s.unservedEnv[e.Key] = e.ServedBy
			continue
		}
		// The pointer's address is COMPOSED from the daemon that serves it
		// (loopholedecl.TokenListen, NC-D41): the served address of this launch, never a
		// second literal in the pack that the daemon's port could drift away from.
		if e.ServedBy != "" {
			v, ok := s.served.resolveListen(e.ServedBy, e.Value)
			if !ok {
				if s.unlistenedEnv == nil {
					s.unlistenedEnv = map[string]string{}
				}
				s.unlistenedEnv[e.Key] = e.ServedBy
				continue
			}
			e.Value = v
			// The daemon's CALLER TOKEN, for a pointer that names it
			// (loopholedecl.TokenCallerToken, OQ-CN7 (c)): this launch's, scoped to the agents
			// this entry reaches. A daemon this launch minted none for gets no pointer, since a
			// client sending no token (or a literal "{caller_token}") is refused anyway.
			if strings.Contains(e.Value, loopholedecl.TokenCallerToken) {
				tok := s.callerTokens[paths.ServiceCallerTokenEnv(e.ServedBy)]
				if tok == "" {
					if s.untokenedEnv == nil {
						s.untokenedEnv = map[string]string{}
					}
					s.untokenedEnv[e.Key] = e.ServedBy
					continue
				}
				e.Value = strings.ReplaceAll(e.Value, loopholedecl.TokenCallerToken, tok)
			}
		}
		out = append(out, e)
	}
	return out
}

// FoldFor is agent's pack env fold as this notch delivers it: its delivery's when the gate
// composed one, and otherwise the same fold (EnvFold over the gate's packs and profile table)
// with the same served-at-this-notch filter. The host notch composes one process from scratch
// and asks this whether or not the agent has a profile, so an unserved address is withheld
// there by the one rule the jail's vehicles apply.
func (s *CredentialScope) FoldFor(agent string) []EnvFoldEntry {
	if s == nil {
		return nil
	}
	if d := s.agents[agent]; d != nil {
		return d.Fold
	}
	return s.servedFold(EnvFold(s.packs, s.sel, agent))
}

// UnservedEnvLines names every pack env variable this notch withheld because the jail daemon
// it points at is not served here, or, for a pointer `served_by` a BOUND LOOPHOLE
// (ServedDaemons.notBoundWhy), because this notch did not bind what that loophole binds into a
// jail, one line per daemon, sorted (P4: what a notch cannot do, it says). nil when nothing was
// withheld. Names only: the value is an address, but the line is about what is absent, and a
// reader acts on the variable.
//
// byLaunch is the launch's own word on each variable (LaunchServes): one it sets itself is left
// out, and one whose own server did not start gets the reason byLaunch gives. nil says nothing.
// The lines group a daemon's variables by the reason given, so a variable with a reason of its
// own never shares a line with one that has the notch's.
func (s *CredentialScope) UnservedEnvLines(byLaunch LaunchServes) []string {
	if s == nil || (len(s.unservedEnv) == 0 && len(s.unlistenedEnv) == 0 && len(s.untokenedEnv) == 0) {
		return nil
	}
	type reason struct {
		daemon, why string
		bound       bool
	}
	byReason := map[reason][]string{}
	var reasons []reason
	// A pointer at what a BOUND LOOPHOLE binds into a jail (ServedDaemons.notBoundWhy) points
	// at no daemon, so its line says what it does point at and why this notch has none.
	var bound map[string]bool
	if len(s.unservedEnv) > 0 {
		bound = boundLoopholes(s.packs)
	}
	for k, daemon := range s.unservedEnv {
		why := ""
		if byLaunch != nil {
			served, launchWhy := byLaunch(k)
			if served {
				continue
			}
			why = launchWhy
		}
		switch {
		case why != "":
		case bound[daemon]:
			why = s.served.notBoundWhy(daemon)
		default:
			// The served set's: the launch's reason for the daemon when it gave one
			// (WithNotServedWhy), else the notch's (ServedDaemons.notServedWhy).
			why = s.served.notServedWhy(daemon)
		}
		r := reason{daemon, why, bound[daemon]}
		if _, seen := byReason[r]; !seen {
			reasons = append(reasons, r)
		}
		byReason[r] = append(byReason[r], k)
	}
	sort.Slice(reasons, func(i, j int) bool {
		if reasons[i].daemon != reasons[j].daemon {
			return reasons[i].daemon < reasons[j].daemon
		}
		return reasons[i].why < reasons[j].why
	})
	var lines []string
	for _, r := range reasons {
		vars := byReason[r]
		sort.Strings(vars)
		if r.bound {
			lines = append(lines, strings.Join(vars, ", ")+" — points at what the "+
				strconv.Quote(r.daemon)+" loophole binds into a jail, "+r.why)
			continue
		}
		lines = append(lines, strings.Join(vars, ", ")+" — points at the "+
			strconv.Quote(r.daemon)+" jail daemon, "+r.why+", so nothing would answer it")
	}
	unlistened := map[string][]string{}
	var bare []string
	for k, daemon := range s.unlistenedEnv {
		if _, seen := unlistened[daemon]; !seen {
			bare = append(bare, daemon)
		}
		unlistened[daemon] = append(unlistened[daemon], k)
	}
	sort.Strings(bare)
	for _, daemon := range bare {
		vars := unlistened[daemon]
		sort.Strings(vars)
		lines = append(lines, strings.Join(vars, ", ")+" — names "+loopholedecl.TokenListen+
			", the listen address of the "+strconv.Quote(daemon)+" jail daemon, which serves "+
			"at no declared address (a loophole declares one as jail_daemon.listen, and a pack "+
			"service has none), so there is no address to compose")
	}
	untokened := map[string][]string{}
	var tokenless []string
	for k, daemon := range s.untokenedEnv {
		if _, seen := untokened[daemon]; !seen {
			tokenless = append(tokenless, daemon)
		}
		untokened[daemon] = append(untokened[daemon], k)
	}
	sort.Strings(tokenless)
	for _, daemon := range tokenless {
		vars := untokened[daemon]
		sort.Strings(vars)
		lines = append(lines, strings.Join(vars, ", ")+" — names "+loopholedecl.TokenCallerToken+
			", the caller token of the "+strconv.Quote(daemon)+" jail daemon, and this launch minted "+
			"none for it (its jail_daemon declares no caller_token), so there is no token to compose")
	}
	return lines
}

// boundLoopholes is the name of every BOUND LOOPHOLE (ServedDaemons.notBoundWhy) a selected pack
// ships: one whose manifest declares no `jail_daemon` and at least one host bind or device, the
// declaration half of the rule loopholes' JailBoundNames applies to a launch's records. nil when
// no selected pack ships one. A manifest that cannot be read is no bound loophole: its pointer
// keeps the jail-daemon wording, and the launch already warned that the loophole is absent.
func boundLoopholes(packs []*Pack) map[string]bool {
	var out map[string]bool
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		mods, _, _ := p.LoopholeModules()
		for _, m := range mods {
			if m.Decl == nil || m.Decl.JailDaemon != nil ||
				(len(m.Decl.HostBindMounts) == 0 && len(m.Decl.HostDevices) == 0) {
				continue
			}
			if out == nil {
				out = map[string]bool{}
			}
			out[m.Name] = true
		}
	}
	return out
}

// WithheldBy is the jail daemon whose pointer this notch withheld under the variable name, for
// any of UnservedEnvLines' three reasons (not served here, no served address, no caller token),
// "" when it withheld nothing by that name. The profile disclosure names it as the fix for a
// credential that reaches no agent (profileReach).
func (s *CredentialScope) WithheldBy(name string) string {
	if s == nil {
		return ""
	}
	for _, withheld := range []map[string]string{s.unservedEnv, s.unlistenedEnv, s.untokenedEnv} {
		if daemon := withheld[name]; daemon != "" {
			return daemon
		}
	}
	return ""
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

// credentialClaims maps each credential variable to the composed providers claiming it: every
// name a provider's own `api_key_env_name` lists, and, for a provider that lists none but
// declares a `platform`, every name a provider of the same platform lists.
//
// THE CLAIMS FOLLOW THE PLATFORM (PP-D9, docs/design/providers-and-profiles-redesign.md). Which
// variables carry a platform's credential is the platform's fact, like the region variables the
// region pre-flight reads by platform (BR-D1): an AWS SDK reads AWS_PROFILE whichever provider
// entry its agent was pointed at. Without this, a user's own `{"platform": "aws-bedrock"}`
// provider claimed nothing, the shipped `bedrock` beside it in the table still claimed the six AWS
// names, and the gate withheld every one of them from the agent on the user's provider, and from
// every other process: claude started in Bedrock mode with no AWS credential and nothing said so.
// Measured with the embedded packs; the maintainer's "you need to be able to define your own and
// get the same behavior" (OQ-BR8) is the rule this keeps.
//
// A provider that lists names of its own keeps exactly those: a declaration is never widened.
// The siblings are read off the COMPOSED table, as every claim is, so a user's override of the
// shipped list moves what a same-platform provider co-claims, and a platform no provider lists
// names for adds nothing. Co-claims are not transitive: only a provider's own list is inherited.
func credentialClaims(providers *jsonx.OrderedMap) map[string][]string {
	out := map[string][]string{}
	if providers == nil {
		return out
	}
	byPlatform := map[string][]string{}
	var inheritors []string
	for _, name := range providers.Keys() {
		entry := providerEntry(providers, name)
		platform := entryString(entry, "platform")
		names := CredentialEnvNames(entry)
		if len(names) == 0 {
			if platform != "" {
				inheritors = append(inheritors, name)
			}
			continue
		}
		for _, v := range names {
			out[v] = appendUnique(out[v], name)
			if platform != "" {
				byPlatform[platform] = appendUnique(byPlatform[platform], v)
			}
		}
	}
	for _, name := range inheritors {
		for _, v := range byPlatform[entryString(providerEntry(providers, name), "platform")] {
			out[v] = appendUnique(out[v], name)
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

// receivesAny is receives over an active set: an agent may see a claimed name when ANY
// provider of its set claims it (docs/design/active-provider-sets.md §4.5, "each entry's claimed
// key reaches that agent, and only that agent"). An empty set receives the unclaimed names only.
func (s *CredentialScope) receivesAny(providers []string, name string) bool {
	if len(providers) == 0 {
		return s.receives("", name)
	}
	for _, p := range providers {
		if s.receives(p, name) {
			return true
		}
	}
	return false
}

// SetProvidersOf is the providers delivery d's active set resolves to, the primary first, for a
// notch's pre-flight that asks one question per provider an agent runs on (the region
// pre-flight). Nil for a nil delivery or a grant-only process.
func SetProvidersOf(d *AgentDelivery) []string { return d.setProviders() }

// setProviders is the providers d's active set resolves to, the primary first: Providers when
// the gate composed them, else the one Provider, else none.
func (d *AgentDelivery) setProviders() []string {
	if d == nil {
		return nil
	}
	if len(d.Providers) > 0 {
		return d.Providers
	}
	if d.Provider != "" {
		return []string{d.Provider}
	}
	return nil
}

// delivers reports whether delivery d's process may see variable name: what its profile's
// provider receives, plus every name a provider its grant names claims. A nil delivery is a
// process with neither, which receives the unclaimed names only.
func (s *CredentialScope) delivers(d *AgentDelivery, name string) bool {
	if d == nil {
		return s.receives("", name)
	}
	if s.receivesAny(d.setProviders(), name) {
		return true
	}
	for _, g := range d.Granted {
		if slices.Contains(s.claims[name], g) {
			return true
		}
	}
	return false
}

// LookupFor is the credential lookup agent's env derive composes through: a pack service's
// caller token first (ScopeInput.CallerTokens), then the hydrated env_sources, then the
// fallback, and neither of the last two for a name another provider claims. It is
// what makes hydrateProviders write only the agent's own provider's api_key into the
// derive's copy of the table (OQ-CN2's rendered-config half). A grant does not widen it: a
// granted key reaches the process's environment and never its derive, so no derive can
// point the agent at a granted provider (the grant re-points nothing).
func (s *CredentialScope) LookupFor(agent string) func(string) (string, bool) {
	// The agent's whole active set (§4.5): "the api_key of each provider in that agent's set and
	// no other", so a set-capable derive's copy of the table carries every entry's key.
	providers := s.agents[agent].setProviders()
	return func(name string) (string, bool) {
		if v, ok := s.callerTokens[name]; ok && v != "" {
			return v, true
		}
		if !s.receivesAny(providers, name) {
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

// Selection is the gate's view of this launch's selection — each agent's profile and its
// provider's platform (SelectionOf over the gate's own inputs) — for the readers that must ask a
// contribution's gate the question the gate itself asked: the env-override pre-flight at both
// notches and `yolo check`. The zero selection on a nil receiver.
func (s *CredentialScope) Selection() GateSelection {
	if s == nil {
		return GateSelection{}
	}
	return s.sel
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
		// Every entry of every set (§4.5): the pre-flight demands each entry's key.
		for _, p := range d.setProviders() {
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Sets is each agent's active set as the gate composed it, the primary first: the agents with
// a set of more than one entry and nothing else, for a refusal that names an entry's position
// (SetPhrase). Nil when every set has one entry.
func (s *CredentialScope) Sets() map[string][]string {
	if s == nil {
		return nil
	}
	var out map[string][]string
	for agent, d := range s.agents {
		if len(d.Set) > 1 {
			if out == nil {
				out = map[string][]string{}
			}
			out[agent] = d.Set
		}
	}
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

// DeliveredTo answers what ONE agent's process receives under name, non-empty: the winner of
// its own composition (EnvFor, envcompose.go), so every reader of it ranks the sources as every
// vehicle delivers them — its shape vars over the env_sources it receives (the shared ones, its
// own provider's claimed ones and its grant's) over the pack env fold it receives, an
// env_sources null removing the fold's value and a shape tombstone everything below it. It is
// the per-agent question the launch-wide DeliveredPackEnv cannot answer: a value only another
// agent receives is not this agent's (the region pre-flight asks it, OQ-BR6).
func (s *CredentialScope) DeliveredTo(agent, name string) (string, bool) {
	if s == nil {
		return "", false
	}
	return s.EnvFor(agent).Value(name)
}

// Relays reports whether agent's env derive composed value into the agent's own environment
// under some name: the RELAY, by which a credential only the environment yolo was launched from
// holds (ScopeInput.Fallback) still reaches the agent, as claude's derive copies a provider's key
// into ANTHROPIC_AUTH_TOKEN. It is how the jail's credential pre-flight tells an agent that
// received such a key from one that reads the variable itself, which no jail backend forwards
// (ProviderCredentialGapsTo). A value is compared, never returned; an empty one relays nothing.
func (s *CredentialScope) Relays(agent, value string) bool {
	if s == nil || value == "" {
		return false
	}
	d := s.agents[agent]
	if d == nil {
		return false
	}
	for _, v := range d.Shape {
		if !v.Unset && v.Value == value {
			return true
		}
	}
	return false
}

// Empty reports whether a delivery carries nothing beyond the shared set.
func (d *AgentDelivery) Empty() bool {
	if d == nil {
		return true
	}
	return d.EnvSources.Len() == 0 && len(d.PackEnv) == 0 && len(d.Shape) == 0
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

// DisclosureWith words what the gate did with the credentials the user configured, names
// only — never a value, with a notch's notes applied. §4's "no silent narrowing": a launch
// that withholds a credential says so, and one that scopes it says to whom. Nil when
// env_sources hydrated no claimed name, which is every launch that configured no provider
// credential.
//
// THE ONE DISCLOSURE RENDERER, at every notch (docs/plans/notch-convergence.md item 14, row
// C7): the jail passes the zero DisclosureNotes, the host its own. A second, notes-less entry
// point (`Disclosure`) was the jail's until it was deleted, so a notch's difference is always
// a named note, never a second function. Names are grouped by claimant, recipients and, under
// Inherited and Composed, whether and whence the process already holds them, so each line
// tells one story.
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
	// IN SET ORDER (docs/design/active-provider-sets.md §4.5): when an agent holds a set of more
	// than one, each entry's key is named in the order the set lists its provider, so pi's
	// `zai, openrouter` discloses ZAI_API_KEY before OPENROUTER_API_KEY whatever order
	// env_sources hydrated them in. Stable, so a launch with no such set keeps hydration order.
	if sets := s.Sets(); len(sets) > 0 {
		rank := func(claimants []string) int {
			best := len(s.claims) + 1<<20
			for _, d := range s.agents {
				for i, p := range d.setProviders() {
					if slices.Contains(claimants, p) && i < best {
						best = i
					}
				}
			}
			return best
		}
		sort.SliceStable(order, func(i, j int) bool {
			return rank(groups[order[i]].claimants) < rank(groups[order[j]].claimants)
		})
	}
	lines := []string{s.disclosureRule()}
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

// disclosureRule is the disclosure's head line: the rule every line under it follows. Under a
// grant (ScopeInput.Grants) a process no profile selects is a recipient too, so the rule names
// the grant beside the profile (credential-sources-separation.md ES-D22). Otherwise, as at every
// jail launch, the recipients are the agents whose profile selects the provider.
func (s *CredentialScope) disclosureRule() string {
	for _, d := range s.agents {
		if len(d.Granted) > 0 {
			return "Credential scope: a provider's credential reaches only the processes whose " +
				"profile selects it or whose --with-credentials grant names it."
		}
	}
	return "Credential scope: a provider's credential reaches only the agents whose profile " +
		"selects it."
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
