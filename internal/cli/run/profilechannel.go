package run

// profilechannel.go is the profile/provider channel ONE launch composes — the B-0 move
// applied to the environment half of the channel, exactly as stageRunPacks applied it to
// the pack trees and the launch-flag injection applied it to the argv (both documented at
// their call sites in run.go).
//
// The defect this removes: every piece of the channel was composed inside the container
// arm — the profile table and the pack env fold in assembleRunCmd, the composed provider
// table and the env_shape vars in commonEnvBlock — and the macos-user branch returns
// before reaching any of it. That backend therefore parsed and VALIDATED `-p zai`
// (checkProfileTargets sits in stagePacks, above the dispatch) and then delivered nothing:
// no variant env, no env_shape relay, no YOLO_PROVIDERS/YOLO_USE_PROFILES for its
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
	"sort"

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
	// YOLO_PROVIDERS and read by the env_shape composition below.
	providers *jsonx.OrderedMap
	// packEnv is the pack env fold over the profile table (packload.EnvVarsFor): each
	// selected pack's static `kind: "env"` values with its selected variant's own
	// literals folded on top (OQ-8).
	packEnv map[string]string
	// shapeVars are the provider env_shape variables the active profiles compose, per
	// profiled agent, in the order the container argv emits them (agentenv.Resolve over
	// profiles.Keys()). This is the half that routes a hydrated credential into the
	// agent's process env (OQ-14).
	shapeVars []agentenv.Var
	// userEnv is the hydrated env_sources this launch would deliver — the secret channel
	// both a {key} placeholder and the credential pre-flight consult. Hydrated once, here,
	// because the container arm also writes it to yolo-user-env.sh and two hydrations
	// would read every dotenv file twice and warn twice.
	userEnv *jsonx.OrderedMap
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
	providers, err := composedProviders(cfg, packs)
	if err != nil {
		return nil, err
	}
	c := &packChannel{
		profiles:  profiles,
		providers: providers,
		packEnv:   packload.EnvVarsFor(packs, packload.ProfileTable(profiles)),
		userEnv:   userEnv,
	}
	// The env_shape composition, one pass over the table in table order — the same
	// iteration the container argv's env block makes, so the two spellings emit the same
	// vars in the same order. A {key} placeholder resolves through what this launch
	// carries: the hydrated env_sources, then the environment yolo was launched from.
	// The relay is the delivery: whatever it composes is put onto the launch env
	// verbatim, so drawing on the invoking shell does not claim a credential the launch
	// would not have carried.
	lookup := c.shapeLookup(o)
	for _, agent := range profiles.Keys() {
		profile := mapStr(profiles, agent)
		c.shapeVars = append(c.shapeVars, agentenv.Resolve(providers, agent, profile,
			packload.ProviderFor(packs, agent, profile), lookup)...)
	}
	return c, nil
}

// shapeLookup is the lookup the env_shape composition resolves a {key} placeholder
// through: the hydrated env_sources, then the environment yolo itself was launched from.
// It deliberately does NOT see the channel's own outputs — a placeholder must not resolve
// through a var another placeholder composed.
func (c *packChannel) shapeLookup(o *Options) agentenv.Lookup {
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
//     pack env, the shape aliases and every pack-shipped loophole's jail_env. Nil on the
//     macos-user arm, which assembles no argv;
//  3. the composed channel's own pack env and shape vars — the same pairs the container
//     argv carries, spelled out so the check means the same thing on a backend that has
//     no argv to read;
//  4. the environment yolo itself was launched from, which the relay can draw on.
//
// An EMPTY value is unset at every step: agentenv drops an empty placeholder result
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
// environment it becomes must not reshuffle between runs), then the shape vars in table
// order, so a shape alias is the more specific intent and wins (OQ-8's rule at the env
// boundary rather than the fold's), then the two wire tables.
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
	env.Set("YOLO_USE_PROFILES", jsonDumpsOrEmptyObj(c.profiles))
	return env
}
