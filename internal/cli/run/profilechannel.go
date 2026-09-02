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
// the result: the container arm emits it onto its argv, the macos-user arm layers it into
// its plan env and relays the two wire tables to its bootstrap. One composition means the
// two backends cannot answer differently about what a profile delivers — which is the same
// property packload.ProfileTable's launch-flag injection already claims for the two
// spellings of one launch.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
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
	// packEnv is the pack env fold over the profile table (packload.EnvVarsFor): each
	// selected pack's static `kind: "env"` values with its selected variant's own
	// literals folded on top (OQ-8).
	packEnv map[string]string
	// shapeVars are the provider environment variables the active profiles compose, per
	// profiled agent, in the order the container argv emits them (packload.AgentEnv over
	// profiles.Keys() — the env-derive runner). This is the half that routes a hydrated
	// credential into the agent's process env (OQ-14).
	shapeVars []agentenv.Var
	// userEnv is the hydrated env_sources this launch would deliver — the secret channel
	// both the env derive's credential and the credential pre-flight consult. Hydrated
	// once, here, because the container arm also writes it to yolo-user-env.sh and two
	// hydrations would read every dotenv file twice and warn twice.
	userEnv *jsonx.OrderedMap
	// resolvedProfiles is every profile name this launch could activate, resolved to its
	// provider and option map (packload.ResolveProfiles). Emitted as YOLO_PROFILES and
	// handed to the env-derive runner, so both consumers of a profile's body — the jail's
	// derives and the host notch's env composition — read ONE resolution.
	resolvedProfiles map[string]packload.ResolvedProfile
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
	resolved, err := packload.ResolveProfiles(packs, userProfiles)
	if err != nil {
		return nil, err
	}
	providers, err := composedProviders(cfg, packs)
	if err != nil {
		return nil, err
	}
	c := &packChannel{
		profiles:         profiles,
		providers:        providers,
		packEnv:          packload.EnvVarsFor(packs, packload.ProfileTable(profiles)),
		userEnv:          userEnv,
		resolvedProfiles: resolved,
	}
	// The provider environment, one pass over the profile table in table order — the
	// same iteration the container argv's env block makes, so the two spellings emit the
	// same vars in the same order. Each agent's OWN pack composes its variables (the
	// env-derive runner, OQ-CS8): the producer reads the composed table, with the
	// selected provider's credential hydrated into its copy only, and the launch relays
	// what it emitted. The credential resolves through what this launch carries — the
	// hydrated env_sources, then the environment yolo was launched from — so the relay
	// does not claim a credential the launch would not have carried.
	lookup := c.shapeLookup(o)
	for _, agent := range profiles.Keys() {
		profile := mapStr(profiles, agent)
		vars, err := packload.AgentEnv(packs, providers, packload.ProfileTable(profiles),
			agent, profile, lookup, packload.WithResolvedProfiles(resolved))
		if err != nil {
			return nil, err
		}
		c.shapeVars = append(c.shapeVars, vars...)
	}
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

// shapeLookup is the lookup the provider environment resolves a credential through: the
// hydrated env_sources, then the environment yolo itself was launched from. It
// deliberately does NOT see the channel's own outputs — a credential must not resolve
// through a variable another part of this composition set.
func (c *packChannel) shapeLookup(o *Options) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if s := mapStr(c.userEnv, name); s != "" {
			return s, true
		}
		if v := o.Getenv(name); v != "" {
			return v, true
		}
		return "", false
	}
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
		if s := mapStr(c.userEnv, name); s != "" {
			return s, true
		}
		if v, ok := argvPairs[name]; ok && v != "" {
			return v, true
		}
		if v := c.packEnv[name]; v != "" {
			return v, true
		}
		for _, v := range c.shapeVars {
			if v.Key == name && v.Value != "" {
				return v.Value, true
			}
		}
		if v := o.Getenv(name); v != "" {
			return v, true
		}
		return "", false
	}
}

// launchEnv flattens the channel into launch-environment form: the form the macos-user
// arm layers into its plan env. The container arm emits the same content onto its argv
// and never calls this.
//
// The order mirrors the container argv's, because layering order is semantics for a key
// two sources both set: the pack env fold first (sorted — a map has no order and the
// environment it becomes must not reshuffle between runs), then the provider env vars in
// table order, so a provider var is the more specific intent and wins (OQ-8's rule at the
// env boundary rather than the fold's), then the two wire tables.
//
// The shape vars' Unset half is skipped, exactly as the container env block skips it:
// `env -i K=V…` starts from nothing, so there is nothing to remove, and spelling a
// removal here would need a convention neither backend has.
func (c *packChannel) launchEnv() *jsonx.OrderedMap {
	env := jsonx.NewOrderedMap()
	keys := make([]string, 0, len(c.packEnv))
	for k := range c.packEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env.Set(k, c.packEnv[k])
	}
	for _, v := range c.shapeVars {
		if v.Unset || v.Key == "" {
			continue
		}
		env.Set(v.Key, v.Value)
	}
	env.Set("YOLO_PROVIDERS", jsonDumpsOrEmptyObj(c.providers))
	env.Set("YOLO_PROFILES", jsonDumpsOrEmptyObj(packload.ProfilesWireTable(c.resolvedProfiles)))
	env.Set("YOLO_USE_PROFILES", jsonDumpsOrEmptyObj(c.profiles))
	return env
}
