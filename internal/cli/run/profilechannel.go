package run

// profilechannel.go is the profile/provider channel ONE launch composes — the B-0 move
// applied to the environment half of the channel, exactly as stageRunPacks applied it to
// the pack trees and the launch-flag injection applied it to the argv (both documented at
// their call sites in run.go).
//
// The defect this removes: every piece of the channel was composed inside the container
// arm — the profile table and the pack env fold in assembleRunCmd, the composed provider
// table and the provider env vars in commonEnvBlock — and the macos-user branch returns
// before reaching any of it. That backend therefore parsed and VALIDATED `-p zai`
// (checkProfileTargets sits in stagePacks, above the dispatch) and then delivered nothing:
// no variant env, no provider env, no YOLO_PROVIDERS/YOLO_USE_PROFILES for its
// bootstrap, no credential pre-flight. `yolo -p zai -- claude` on macos-user composed
// nothing and said so to nobody — the same signature of silence B-0 found for the pack
// trees, one layer down.
//
// The composition is therefore hoisted ABOVE the backend dispatch, and each arm consumes
// the result: the container arm writes it into yolo-user-env.sh's channel section and
// each agent's own env file (deliverChannel — per-entry delivery, on a fresh launch and an
// attach alike), the macos-user arm layers it into its plan env and relays the two wire
// tables to its bootstrap. What reaches WHICH agent is the credential gate's answer
// (packload.ScopeCredentials, composed here once; docs/design/provider-credential-scope.md).
// One composition means the
// two backends cannot answer differently about what a profile delivers — which is the same
// property packload.ProfileTable's launch-flag injection already claims for the two
// spellings of one launch.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// packChannel is everything a launch composes from its selected profiles and providers,
// reduced to the form each consumer reads. Composed once, by composePackChannel, above
// the backend dispatch; never re-derived downstream.
type packChannel struct {
	// profiles is the CLI-keyed effective profile table (effectiveUseProfiles): the
	// merge the env block emits as YOLO_USE_PROFILES, the launch-flag injection reads,
	// and the profile disclosure line describes.
	profiles *jsonx.OrderedMap
	// providers is the composed provider table (composedProviders): user `providers`
	// entries over every selected pack's `kind: "provider"` service facts. Emitted as
	// YOLO_PROVIDERS and read by the env derive below.
	providers *jsonx.OrderedMap
	// scope is THE CREDENTIAL GATE's answer for this launch (packload.ScopeCredentials,
	// docs/design/provider-credential-scope.md OQ-CN2): the env_sources and pack env every
	// process may see, and per profiled agent what only that agent receives — its
	// provider's claimed credentials, the gated env its own selection satisfies, and its
	// pack's env derive's output (the shape vars, composed through the gate's lookup).
	// Every vehicle reads it and none re-derives it: the container arm writes the shared
	// half into yolo-user-env.sh and each agent's half into its own env file
	// (agentenvfiles.go), and the macos-user arm layers the shared half plus the launched
	// agent's (launchEnv).
	scope *packload.CredentialScope
	// userEnv is the hydrated env_sources BEFORE the gate — the secret channel the gate
	// scopes. Hydrated once, here, because two hydrations would read every dotenv file
	// twice and warn twice. No writer delivers it whole any more: the shared file carries
	// scope.SharedEnvSources(), and each agent's file its own claimed entries.
	userEnv *jsonx.OrderedMap
	// resolvedProfiles is every profile name this launch could activate, resolved to its
	// provider and option map (packload.ResolveProfiles). Emitted as YOLO_PROFILES and
	// handed to the env-derive runner, so both consumers of a profile's body — the jail's
	// derives and the host notch's env composition — read ONE resolution.
	resolvedProfiles map[string]packload.ResolvedProfile
	// localProviderForwards are implicit host-loopback forwards requested by user
	// provider URLs. They originate only in user config, never in a pack endpoint;
	// see localProviderForwards for why that boundary matters.
	localProviderForwards []any
	// localProviderForwardSources is the same set WITH provider attribution, for the
	// launch disclosure (OQ-PC2). Same walker, two projections — see providerlocal.go.
	localProviderForwardSources []providerForward
}

// composePackChannel composes the channel from the config and the STAGED pack set.
//
// userEnv may be nil, which means "hydrate env_sources here". Run passes the map it
// hydrated itself so the container arm can write the same value to yolo-user-env.sh
// rather than resolving env_sources a second time; a hand-built assembleInput (every one
// is a test) passes whatever it has, or nil.
func (o *Options) composePackChannel(cfg *jsonx.OrderedMap, packs []*packload.Pack,
	userEnv *jsonx.OrderedMap) (*packChannel, error) {
	if userEnv == nil {
		userEnv = config.ResolveEnvSources(o.Workspace, cfg, func(msg string) {
			o.pr(o.Stdout).print(msg)
		})
	}
	profiles := o.effectiveUseProfiles(cfg, packs)
	// The user's profile DECLARATIONS, at user scope — never read off `cfg`, which is the
	// merged map and would let a workspace spelling through (config.LoadProfiles reads
	// the user file directly; OQ-CS5). Malformed entries arrive as warnings, which this
	// is the right place to surface: the fatal form of the same problem already refused
	// the launch in ValidateConfig.
	userProfiles, err := config.LoadProfiles(func(msg string) {
		o.pr(o.Stdout).print(msg)
	})
	if err != nil {
		return nil, err
	}
	// THE EIGHTH bespoke pre-flight (the numbering is packs.go's and
	// providerpreflight.go's; checkProfileTargets was the fifth), and the one OQ-CS6
	// buys: declaration is MANDATORY, so a selected name nothing declares refuses here
	// rather than silently doing nothing. Before the resolution below, because an
	// undeclared name has no resolution to argue about.
	if err := o.checkProfileDeclarations(profiles, userProfiles, packs); err != nil {
		return nil, err
	}
	// The provider table composes BEFORE the profiles resolve, because the resolution
	// reads the declared options off it (packload.providerOptions) and the census must
	// measure the surface this launch actually carries — not the one a manifest walk
	// would have described. Same object for both, so the env derive below and the
	// resolution cannot disagree about what a provider declares.
	providers, err := composedProviders(cfg, packs)
	if err != nil {
		return nil, err
	}
	resolved, err := packload.ResolveProfiles(packs, userProfiles, providers)
	if err != nil {
		return nil, err
	}
	c := &packChannel{
		profiles:                    profiles,
		providers:                   providers,
		userEnv:                     userEnv,
		resolvedProfiles:            resolved,
		localProviderForwards:       localProviderForwards(cfgMap(cfg, "providers")),
		localProviderForwardSources: localProviderForwardSources(cfgMap(cfg, "providers")),
	}
	// THE CREDENTIAL GATE, ONCE (OQ-CN2): the one decision of what reaches which agent,
	// which every vehicle reads. It also runs each profiled agent's env derive (the
	// env-derive runner, OQ-CS8), with that agent's selected provider's credential — and
	// no other provider's — hydrated into the derive's copy of the table. A credential
	// resolves through what this launch carries: the hydrated env_sources, then the
	// environment yolo was launched from, so the relay does not claim a credential the
	// launch would not have carried.
	scope, err := packload.ScopeCredentials(packload.ScopeInput{
		Packs:      packs,
		Providers:  providers,
		Profiles:   packload.ProfileTable(profiles),
		Resolved:   resolved,
		EnvSources: userEnv,
		Fallback: func(name string) (string, bool) {
			v := o.Getenv(name)
			return v, v != ""
		},
	})
	if err != nil {
		return nil, err
	}
	c.scope = scope
	return c, nil
}

// checkProfileDeclarations refuses a SELECTED profile name nothing declares — the flag
// and config spellings alike: a `use_profiles` value and a `-p <name>` are both in the
// table this reads (effectiveUseProfiles merged them). It sits beside
// checkProfileTargets, its CLI-name twin on the same table, and shares that check's
// reason for being FATAL: a silently inert selector is indistinguishable from a working
// one (OQ-CS6 — the reversal of the old free-form-names ruling).
//
// The declared set is packload.DeclaredProfileNames — every kind:profile the staged
// packs ship plus every user `profiles` entry — because that is exactly the union the
// design makes a profile name answer to; a second list here would be a second idea of
// "declared".
func (o *Options) checkProfileDeclarations(profiles *jsonx.OrderedMap,
	userProfiles map[string]packload.UserProfile, packs []*packload.Pack) error {
	if profiles.Len() == 0 {
		return nil
	}
	declared := packload.DeclaredProfileNames(packs, userProfiles)
	isDeclared := map[string]bool{}
	for _, name := range declared {
		isDeclared[name] = true
	}
	var problems []string
	for _, agent := range profiles.Keys() {
		name := mapStr(profiles, agent)
		if name == "" || isDeclared[name] {
			continue
		}
		problems = append(problems, fmt.Sprintf("profile %q selected for %s: %s",
			name, agent, packload.UndeclaredProfileMessage(name, declared)))
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("packs: %s", strings.Join(problems, "\npacks: "))
}

// deliveryLookup is what "set in this launch's environment" means to the credential
// pre-flight, in the order the launch would have used the value:
//
//  1. the hydrated env_sources (the secret channel);
//  2. argvPairs — the `-e K=V` pairs of the assembled container argv, which carry the
//     pack env, the provider env vars and every pack-shipped loophole's jail_env. Nil on
//     the macos-user arm, which assembles no argv;
//  3. the composed channel's own pack env and shape vars — the same pairs the container
//     argv carries, spelled out so the check means the same thing on a backend that has
//     no argv to read;
//  4. the environment yolo itself was launched from, which the relay can draw on.
//
// An EMPTY value is unset at every step: the env-derive runner drops an empty value
// rather than composing an empty token, and an empty credential is the failure the
// pre-flight exists to name, not an escape from it.
func (c *packChannel) deliveryLookup(o *Options, argvPairs map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, _, ok := c.deliverySource(o, argvPairs, name)
		return v, ok
	}
}

// deliverySource is deliveryLookup's body, plus the PHRASE naming which of the five
// sources answered. Everything above reads through it, so the two questions — would
// this launch deliver the variable, and from where — cannot be answered by two walks
// that disagree about the order.
//
// The phrase exists because a refusal has to name where each side was DECLARED
// (docs/reference/protocol-resolution.md#the-four-outcomes): "something overrides that
// pack's variable" sends a reader hunting through four files, and "env_sources, and a
// selected pack's env contribution" does not. It never carries the VALUE — these are
// typically credentials.
//
// It cannot name WHICH pack set a var. packEnv is already the fold
// (packload.EnvVarsFor), and the launch delivers the fold rather than any one
// contribution, so "a selected pack's" is the honest precision available here; the
// `yolo pack footprint` verb is where a reader learns which one.
//
// THE PHRASES THEMSELVES ARE packload's (packload.From*), not this file's, because the
// `yolo check` prediction reports the same refusal from two of these five channels
// (internal/cli/check/envoverrides.go). Spelled at both callers they would drift, and a
// prediction that worded one problem differently from the launch is the defect that file
// exists to avoid rather than to introduce. WHICH channels exist and IN WHAT ORDER they
// are consulted stays here, where the launch composes them.
//
// ⚠ THE FIFTH SOURCE IS NOT A DELIVERY INTO THE JAIL. The launch environment is here
// because the credential pre-flight asks what the relay can draw on; nothing forwards it
// into the jail under its own name. So the env-override pre-flight reads this through
// jailOriginLookup (envoverrides.go), which drops that answer.
//
// "DELIVERED" MEANS TO SOME PROCESS OF THE LAUNCH, since the credential gate (OQ-CN2):
// the shared set or any one agent's. A hydrated credential the gate withholds from every
// agent is NOT delivered, so it answers nothing here — an override between two variables
// nobody receives overrides nothing — and the credential pre-flight never asks about one,
// having been narrowed to the selected providers (OQ-CN3).
func (c *packChannel) deliverySource(o *Options, argvPairs map[string]string,
	name string) (value, origin string, ok bool) {
	if s := mapStr(c.userEnv, name); s != "" && c.scope.DeliversEnvSource(name) {
		return s, packload.FromEnvSources, true
	}
	if v, found := argvPairs[name]; found && v != "" {
		return v, packload.FromContainerArgv, true
	}
	if v, found := c.scope.DeliveredPackEnv(name); found && v != "" {
		return v, packload.FromPackEnv, true
	}
	if v, found := c.scope.DeliveredShape(name); found && v != "" {
		return v, packload.FromProfileEnv, true
	}
	if v := o.Getenv(name); v != "" {
		return v, packload.FromLaunchEnv, true
	}
	return "", "", false
}

// launchEnv flattens the channel into the launch environment of ONE PROGRAM: the form the
// macos-user arm layers into its plan env, for the program its invocation starts. agent is
// that program's name (the basename of its argv[0]); a shell, or any name no profile
// selects, receives the shared half only.
//
// PER LAUNCH, NOT PER AGENT, and the macos-user arm discloses it
// (noteMacosUserCredentialScope): this backend runs one command per invocation under one
// session env file, so there is no second launcher to carry a second agent's values. The
// launched agent's own values reach every process of its session — as any agent's reach
// its children on every backend — and another agent started inside the session receives
// none of its own (OQ-CN6's "a vehicle that cannot express per-agent delivery stays per
// launch and says so").
//
// The order is the backend's own precedence, unchanged by the gate: the pack env fold
// first (sorted — a map has no order and the environment it becomes must not reshuffle
// between runs), then the agent's provider env vars, so a provider var is the more
// specific intent and wins (providers.md#pv-oq-8's rule at the env boundary rather than
// the fold's), then the three wire tables, then env_sources LAST, in hydration order —
// which is where macosuser.buildPlan layered its own hydration before the gate took that
// call away, so a user's own dotenv entry still beats every channel value on this backend.
//
// The shape vars' Unset half is skipped: `env -i K=V…` starts from nothing, so there is
// nothing to remove, and spelling a removal here would need a convention neither backend
// has.
func (c *packChannel) launchEnv(agent string) *jsonx.OrderedMap {
	env := jsonx.NewOrderedMap()
	packEnv := map[string]string{}
	for k, v := range c.scope.SharedPackEnv() {
		packEnv[k] = v
	}
	d := c.scope.Agent(agent)
	if d != nil {
		for k, v := range d.PackEnv {
			packEnv[k] = v
		}
	}
	keys := make([]string, 0, len(packEnv))
	for k := range packEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env.Set(k, packEnv[k])
	}
	if d != nil {
		for _, v := range d.Shape {
			if v.Unset || v.Key == "" {
				continue
			}
			env.Set(v.Key, v.Value)
		}
	}
	env.Set("YOLO_PROVIDERS", jsonDumpsOrEmptyObj(c.providers))
	env.Set("YOLO_PROFILES", jsonDumpsOrEmptyObj(packload.ProfilesWireTable(c.resolvedProfiles)))
	env.Set("YOLO_USE_PROFILES", jsonDumpsOrEmptyObj(c.profiles))
	sources := c.scope.EnvSourcesFor(agent)
	for _, k := range sources.Keys() {
		v, _ := sources.Get(k)
		env.Set(k, v)
	}
	return env
}
