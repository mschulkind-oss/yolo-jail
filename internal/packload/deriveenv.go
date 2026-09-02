package packload

// deriveenv.go is the ENV-EMITTING DERIVE (provider-catalog-and-selection.md §3.1 and
// §9 OQ-CS8; provider-table-fidelity.md OQ-PT9): the ONE runner both notches compose an
// agent's provider environment through. The binding lives in the agent's OWN pack, as a
// yolo.env(agent, fn) registration in its derive.lua — the producer reads the composed
// providers table and returns the variables the agent's process needs — so no manifest
// vocabulary declares the delivery, and a second implementation of the composition has
// nowhere to live: the jail notch's env block and the host notch's composition both
// reduce through AgentEnv, the way both already reduce the pack env fold through
// packload.EnvVarsFor.
//
// Host-side only, on purpose. An IN-JAIL env derive has no consumer: the container's
// environment is fixed at `podman run` (the argv this runner's output feeds), and the
// macos-user notch fixes its plan env before bootstrap — so the entrypoint never runs
// this and the yolo.env registration a pack's derive.lua carries is inert there.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// DeriveScript reads a pack's derive.lua (at its tree root), or "" when absent. The
// script registers per-surface producers via yolo.derive(agent, surface, fn) and
// environment producers via yolo.env(agent, fn); a surface with no registered derive
// gets no dynamic layer, and an agent whose pack registered no yolo.env composes no
// provider environment.
func DeriveScript(p *Pack) string {
	if p.Root == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(p.Root, "derive.lua"))
	if err != nil {
		return ""
	}
	return string(data)
}

// AgentEnv composes one agent's provider environment: it runs the yolo.env producer the
// agent's own pack registered, over the composed providers table, and returns what the
// producer emitted as agentenv.Vars, sorted by key — a map has no order, and an
// environment must not reshuffle between launches.
//
// providers is the launch's composed table (ComposeProviders) — the SAME table the
// config derives read as YOLO_PROVIDERS — but the copy handed to the producer is
// HYDRATED: each entry that names an api_key_env_name the lookup can find carries
// api_key = that value. The credential crosses into the derive invocation ONLY; the
// table itself stays secret-free (provider-table-fidelity.md §5.5, D8) and is never
// mutated. A lookup that finds nothing composes no api_key at all — an empty credential
// is the pre-flight's refusal to make, not a token to hand an agent. useProfiles is the
// effective profile table in ProfileTable's shape, exposed as ctx.use_profiles.
//
// The producer is discovered by bin ownership: the one selected pack that installs the
// agent's CLI. Nothing composes when the inputs are inert — no profile at this agent's
// CLI name, no pack installing the bin, a pack whose derive.lua registers no yolo.env
// for the agent, or a provider entry the table does not hold (the producer's own "no
// selection" case) all return (nil, nil), the identity. A Lua error, or a producer that
// sets a variable to something other than a string or ctx.tombstone, is a real error:
// this composition IS the delivery, so a broken producer refuses the launch rather than
// composing half an environment.
func AgentEnv(packs []*Pack, providers *jsonx.OrderedMap, useProfiles map[string]string,
	agent, profile string, lookup func(string) (string, bool)) ([]agentenv.Var, error) {
	if agent == "" || profile == "" {
		return nil, nil
	}
	var owner *Pack
	for _, p := range packs {
		if p.installsBin(agent) {
			owner = p
			break
		}
	}
	if owner == nil {
		return nil, nil
	}
	script := DeriveScript(owner)
	if script == "" {
		return nil, nil
	}
	out, err := (luahook.GopherLuaVM{}).Derive(script, &luahook.DeriveCtx{
		Agent:            agent,
		Env:              true,
		ProfileName:      profile,
		SelectedProvider: ProviderFor(packs, agent, profile),
		Tables: map[string]map[string]any{
			manifest.SourceProviders:   hydrateProviders(providers, lookup),
			manifest.SourceUseProfiles: plainProfiles(useProfiles),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("pack %s: %s's env derive: %w", owner.Name, agent, err)
	}
	names := make([]string, 0, len(out))
	for name := range out {
		names = append(names, name)
	}
	sort.Strings(names)
	vars := make([]agentenv.Var, 0, len(names))
	for _, name := range names {
		// The empty spellings compose nothing, the rule the old placeholder vocabulary
		// enforced: an empty value is an absent input (an empty endpoint is a request
		// to the wrong host; an empty token is a credential that gets SENT), and an
		// empty key names no variable at all.
		if name == "" {
			continue
		}
		switch v := out[name].(type) {
		case nil: // the tombstone: an explicit removal, not a set
			vars = append(vars, agentenv.Var{Key: name, Unset: true})
		case string:
			if v == "" {
				continue
			}
			vars = append(vars, agentenv.Var{Key: name, Value: v})
		default:
			return nil, fmt.Errorf("pack %s: %s's env derive set %s to a %T, want a string "+
				"(or ctx.tombstone to remove the variable)", owner.Name, agent, name, out[name])
		}
	}
	return vars, nil
}

// hydrateProviders deep-copies the composed providers table into the plain value model
// the derive ctx exposes, resolving each entry's credential into the copy: an entry that
// names an api_key_env_name the lookup finds carries api_key = that value, and one that
// does not carries no api_key at all. The copy is the whole point — the credential
// crosses into the derive invocation only (the table the launch relays stays
// secret-free, D8), and the input table is never mutated.
func hydrateProviders(providers *jsonx.OrderedMap, lookup func(string) (string, bool)) map[string]any {
	root, _ := plainValue(providers).(map[string]any)
	if root == nil {
		root = map[string]any{}
	}
	if lookup == nil {
		return root
	}
	for _, v := range root {
		entry, ok := v.(map[string]any)
		if !ok {
			continue
		}
		keyName, _ := entry["api_key_env_name"].(string)
		if keyName == "" {
			continue
		}
		if val, ok := lookup(keyName); ok && val != "" {
			entry["api_key"] = val
		}
	}
	return root
}

// plainValue lowers one composed value into the plain model a derive ctx table holds:
// objects become map[string]any (recursively), a null drops, everything else passes
// through. A nil *jsonx.OrderedMap is a nil input, not a receiver to call methods on.
func plainValue(v any) any {
	m, ok := v.(*jsonx.OrderedMap)
	if !ok || m == nil {
		return v
	}
	out := make(map[string]any, m.Len())
	for _, k := range m.Keys() {
		sub, _ := m.Get(k)
		if sub == nil {
			continue
		}
		out[k] = plainValue(sub)
	}
	return out
}

// plainProfiles lowers the effective profile table into the same plain model, so the
// producer reads ctx.use_profiles.claude exactly as the in-jail derives do.
func plainProfiles(t map[string]string) map[string]any {
	out := make(map[string]any, len(t))
	for k, v := range t {
		out[k] = v
	}
	return out
}
