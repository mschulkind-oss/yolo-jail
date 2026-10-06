package packload

// builtinproviders.go is core's reading of `built_in_providers` (packdecl.BuiltInProviders;
// docs/design/pi-codex-provider-shadowing.md OQ-3, ruled 2026-10-05): which providers an agent
// implements itself, so yolo writes no model entry over one and the agent uses its own list. A
// BUILT-IN PROVIDER is that design's term: a provider an agent ships its own client and model
// list for, under its own key.
//
// Core knows no agent. The agent's own pack declares the names, and core answers three readers
// from that one declaration: every derive's ctx.built_in_providers (BuiltInProvidersFor), the
// launch's profile line (profileReach), and the key a plan's own provider reads (AgentEnv).

import (
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
)

// BuiltInProvidersFor is agent's own providers, keyed by yolo provider name, as the pack that
// installs agent declares them (bin ownership, the rule NativeCapabilities follows): every
// declared name maps to itself, and every plan maps its yolo provider to the agent's own
// provider for that plan, or to one with no ID where the agent has none. Nil when no selected
// pack installs agent or that pack declares nothing, which every derive reads as "no provider is
// built in": the world before the ruling.
func BuiltInProvidersFor(packs []*Pack, agent string) map[string]luahook.BuiltInProvider {
	owner := binOwner(packs, agent)
	if owner == nil || owner.Decl == nil {
		return nil
	}
	decl := owner.Decl.BuiltInProvidersFor(agent)
	if decl == nil {
		return nil
	}
	out := make(map[string]luahook.BuiltInProvider, len(decl.Names)+len(decl.Plans))
	for _, name := range decl.Names {
		if name != "" {
			out[name] = luahook.BuiltInProvider{ID: name, YoloList: decl.RendersYoloList(name)}
		}
	}
	for name, plan := range decl.Plans {
		if name == "" {
			continue
		}
		if plan == nil {
			out[name] = luahook.BuiltInProvider{}
			continue
		}
		out[name] = luahook.BuiltInProvider{ID: plan.Provider, APIKeyEnvName: plan.APIKeyEnvName,
			YoloList: decl.RendersYoloList(name)}
	}
	return out
}

// RunsOwnList reports whether agent runs the yolo provider named provider on a model list of its
// own: it has the name built in (BuiltInProviderFor) and its pack does not render yolo's list for
// it (packdecl.BuiltInProviders.YoloLists). The two readers that describe the LIST, the role
// environment (AgentEnv) and the launch's profile line (profileReach), ask this rather than
// BuiltInProviderFor, so pi and opencode on openai-codex, which run yolo's one list on their own
// client (docs/design/model-lists-and-pickers.md ML-D1), keep its tiers and its endpoint line.
func RunsOwnList(packs []*Pack, agent, provider string) bool {
	own, builtIn := BuiltInProviderFor(packs, agent, provider)
	return builtIn && !own.YoloList
}

// BuiltInProviderFor is agent's own provider for the yolo provider named provider, and whether
// agent has that name built in at all: (own, true) when agent reaches it through its own client
// own.ID, (zero, true) when agent has the name built in for another plan and no provider of its
// own for this one, and (zero, false) when the name is not built in, so the agent's derive writes
// its row as before the ruling.
func BuiltInProviderFor(packs []*Pack, agent, provider string) (luahook.BuiltInProvider, bool) {
	if provider == "" {
		return luahook.BuiltInProvider{}, false
	}
	own, ok := BuiltInProvidersFor(packs, agent)[provider]
	return own, ok
}

// BuiltInKeyVars is the key each of providers delivers to agent under the name agent's own
// provider for its plan reads, wherever that differs from the variable the provider's entry
// points at (builtInKeyVar names it). Two cases compose one:
//
//   - a plan declares the name (packdecl.ProviderPlan's APIKeyEnvName): opencode's
//     zai-coding-plan reads ZHIPU_API_KEY, so on yolo's zai the zai key is composed as
//     ZHIPU_API_KEY too;
//   - the user re-pointed a shipped provider's api_key_env_name at a variable of their own
//     (`providers.openrouter.api_key_env_name = "OR_KEY"`): the agent's own client reads the
//     variable the shipped provider declares (OPENROUTER_API_KEY), and with no catalog row written
//     over a built-in provider no `${OR_KEY}` reference is left to carry the user's name, so the
//     key is composed under the shipped name.
//
// table is the hydrated providers view (hydrateProviders), whose entries carry `api_key` only
// for a credential the lookup found, so a provider with no key composes nothing: an empty
// credential is the pre-flight's refusal to make, never a value to send. One variable per name,
// the first provider in the list naming it winning, so the primary of an active set outranks a
// later entry.
func BuiltInKeyVars(packs []*Pack, agent string, table map[string]any, providers []string) []agentenv.Var {
	builtIn := BuiltInProvidersFor(packs, agent)
	if len(builtIn) == 0 {
		return nil
	}
	var out []agentenv.Var
	seen := map[string]bool{}
	for _, name := range providers {
		own, ok := builtIn[name]
		if !ok || own.ID == "" {
			continue
		}
		entry, _ := table[name].(map[string]any)
		ownVar := builtInKeyVar(packs, own, name)
		pointed, _ := entry["api_key_env_name"].(string)
		if ownVar == "" || ownVar == pointed || seen[ownVar] {
			continue
		}
		key, _ := entry["api_key"].(string)
		if key == "" {
			continue
		}
		seen[ownVar] = true
		out = append(out, agentenv.Var{Key: ownVar, Value: key})
	}
	return out
}

// builtInKeyVar is the variable the agent's own provider own reads the key of yolo provider name
// from: its plan's declared name, else the ONE variable the shipped provider declares (the later
// shipping pack winning, ComposeProviders' rule), else "" when nothing says, for a provider only
// the user declares or one of several variables.
func builtInKeyVar(packs []*Pack, own luahook.BuiltInProvider, name string) string {
	if own.APIKeyEnvName != "" {
		return own.APIKeyEnvName
	}
	return shippedKeyEnvName(packs, name)
}

// shippedKeyEnvName is the one variable the last selected pack shipping provider name declares
// for its key, "" when no pack ships it or its declaration names several.
func shippedKeyEnvName(packs []*Pack, name string) string {
	found := ""
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, prov := range p.Decl.Providers() {
			if prov.Name == name {
				found = prov.APIKeyEnvName.KeyPointer()
			}
		}
	}
	return found
}

// activeSetProviders is each entry's provider, in set order.
func activeSetProviders(set []luahook.SetEntry) []string {
	out := make([]string, 0, len(set))
	for _, e := range set {
		out = append(out, e.Provider)
	}
	return out
}
