// Package agentenv composes the process environment an agent runs with, from the
// resolved `pack_profiles` + `providers` configuration.
//
// # Why this is its own package
//
// It is the ONE env-composition implementation both notches use. The jail serializes
// what Resolve returns into `podman run -e KEY=VAL`; `yolo host` applies the same result
// on top of os.Environ() before exec. That is what makes `yolo -- claude` and
// `yolo host -- claude` compose the same environment from the same resolved profile,
// which docs/design/host-agent-environment.md §2.2 claims as jail/host parity — a claim
// two independent copies of this logic could not keep.
//
// # Why the environment channel exists at all
//
// A provider's `api_key_env` carries the NAME of a variable, never its value: config
// files are committed and secrets are not. Every agent that reads a credential therefore
// reads it from its own PROCESS environment, so a config surface can route a credential
// but cannot deliver one (host-agent-environment.md §1 P2). Resolve does not invent
// secret values either — it composes the non-secret flags that a profile implies, and
// the secret itself arrives from `env_sources`, which the caller hydrates.
package agentenv

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Var is one environment assignment, or one REMOVAL.
//
// Unset is not a cosmetic distinction: `unset AWS_PROFILE` and `AWS_PROFILE=` behave
// differently in every AWS SDK, and the hand-written shell wrapper this whole design
// replaces exists partly to do the former (host-agent-environment.md §2.2). No config
// SURFACE can express a removal at all, which is why its presence alone requires the
// process-env channel.
type Var struct {
	Key   string
	Value string
	Unset bool
}

// Resolve returns the profile-derived environment for one agent, in a stable order.
//
// `agent` is the agent's own name as `pack_profiles` keys it ("claude"), and profiles
// is the EFFECTIVE profile map — config's `pack_profiles` after any per-launch
// overrides, which the caller has already merged.
//
// An agent with no profile, an unknown profile, or a profile naming no configured
// provider resolves to nothing. That is deliberate rather than an error: a shared config
// routinely names providers a given machine has no credentials for, and refusing there
// would break the ordinary case (see reference-mismatch-diagnostics.md for the general
// disposition on configured-but-not-in-effect state).
func Resolve(cfg *jsonx.OrderedMap, agent string, profiles *jsonx.OrderedMap) []Var {
	if cfg == nil || profiles == nil || agent == "" {
		return nil
	}
	profile, ok := getStr(profiles, agent)
	if !ok || profile == "" {
		return nil
	}
	provider := mapAt(mapAt(cfg, "providers"), profile)

	// Bedrock is the only profile that implies environment today, and only for claude:
	// CLAUDE_CODE_USE_BEDROCK is a variable Claude Code reads, not a property of the
	// provider. Extracted verbatim from the jail's assemble path so the two notches
	// cannot drift; a general provider->env mapping is pack-profiles' job, not this
	// function's, and inventing one here would be designing ahead of that ruling.
	if agent == "claude" && profile == "bedrock" {
		return bedrockVars(provider)
	}
	return nil
}

// bedrockVars is the claude+bedrock block. Order is frozen: it is the order the jail's
// podman argv has always emitted, and an argv golden test is cheaper to keep passing than
// to re-baseline.
func bedrockVars(provider *jsonx.OrderedMap) []Var {
	out := []Var{{Key: "CLAUDE_CODE_USE_BEDROCK", Value: "1"}}
	if provider == nil {
		return out
	}
	if region, ok := getStr(provider, "region"); ok && region != "" {
		out = append(out, Var{Key: "AWS_REGION", Value: region})
	}
	models := mapAt(provider, "models")
	for _, m := range []struct{ key, env string }{
		{"default", "ANTHROPIC_DEFAULT_OPUS_MODEL"},
		{"haiku", "ANTHROPIC_DEFAULT_HAIKU_MODEL"},
		{"sonnet", "ANTHROPIC_DEFAULT_SONNET_MODEL"},
	} {
		if v, ok := getStr(models, m.key); ok && v != "" {
			out = append(out, Var{Key: m.env, Value: v})
		}
	}
	return out
}

// Apply overlays vars onto an environ slice in "KEY=VALUE" form, returning a new slice.
// A Var with Unset removes the key entirely rather than setting it empty.
//
// Key order of the INPUT is preserved for keys it already carries (an overlay updates in
// place), so the result stays readable and diffable; new keys append in vars order.
func Apply(environ []string, vars []Var) []string {
	idx := make(map[string]int, len(environ))
	out := make([]string, 0, len(environ)+len(vars))
	for _, kv := range environ {
		k := envKey(kv)
		if prev, dup := idx[k]; dup {
			// A duplicate key in the inherited environ: last wins, matching what
			// execve hands a process. Overwrite in place so the overlay below still
			// targets one slot.
			out[prev] = kv
			continue
		}
		idx[k] = len(out)
		out = append(out, kv)
	}
	removed := false
	for _, v := range vars {
		if v.Unset {
			if i, ok := idx[v.Key]; ok {
				out[i] = ""
				delete(idx, v.Key)
				removed = true
			}
			continue
		}
		kv := v.Key + "=" + v.Value
		if i, ok := idx[v.Key]; ok {
			out[i] = kv
			continue
		}
		idx[v.Key] = len(out)
		out = append(out, kv)
	}
	if !removed {
		return out
	}
	compacted := make([]string, 0, len(out))
	for _, kv := range out {
		if kv != "" {
			compacted = append(compacted, kv)
		}
	}
	return compacted
}

// envKey returns the key half of a "KEY=VALUE" entry ("" has no key half, and an entry
// with no "=" is treated as a bare key, which is what the kernel would hand us).
func envKey(kv string) string {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i]
		}
	}
	return kv
}

func mapAt(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if m == nil {
		return nil
	}
	v, ok := m.Get(key)
	if !ok || v == nil {
		return nil
	}
	sub, _ := v.(*jsonx.OrderedMap)
	return sub
}

func getStr(m *jsonx.OrderedMap, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m.Get(key)
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
