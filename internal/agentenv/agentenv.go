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
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
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

// Lookup reports the current value of one variable in the launch environment being
// composed. It is the seam that keeps a {key} placeholder honest: agentenv never invents
// a credential, it can only RELAY one the launch would have carried anyway. Both notches
// build it from the channels they actually deliver — the hydrated env_sources first, then
// whatever the invoking environment already carries.
type Lookup func(name string) (value string, ok bool)

// agentProtocols is THE agent → wire-protocol table (zai-plumbing.md §5): which wire
// protocol each agent speaks, keyed by the CLI name the profile table keys. The manifest
// schema has no field a pack could declare it in, so this is a NAMED table — one place,
// cited, rather than a per-agent branch in the composition — because the same mapping
// lives in the derives in their own dialect (pi, codex and opencode each read the openai
// endpoint in Lua), and a third protocol or a schema field means changing every copy in
// one commit instead of discovering them one launch at a time.
//
// An agent absent here speaks nothing yolo knows, which composes nothing rather than
// guessing. codex is openai on purpose and chat-only by measurement (zai OQ-Z1:
// /v4/responses 404s on both z.ai routes); which wire_api that endpoint speaks is the
// derive's business, so the env shape says nothing about it.
var agentProtocols = map[string]string{
	"claude":   "anthropic",
	"pi":       "openai",
	"codex":    "openai",
	"opencode": "openai",
}

// ProtocolFor returns the wire protocol agent speaks, or "" when none is known.
func ProtocolFor(agent string) string { return agentProtocols[agent] }

// Resolve returns the profile-derived environment for one agent, in a stable order.
//
// providers is the launch's COMPOSED provider table — packload.ComposeProviders' output,
// pack service facts under the user's own entries — not the raw config key, so a
// pack-shipped endpoint reaches this exactly as it reaches the derives. `agent` is the
// CLI name the profile table keys ("claude"), `profile` the variant active for it, and
// `provider` who that variant names (packload.ProviderFor). lookup is where a {key}
// placeholder may resolve.
//
// Nothing here errors. An agent with no profile, an unknown provider, a provider with no
// env shape for the protocol this agent speaks, or a placeholder whose input is missing
// composes nothing. That is deliberate rather than an error: a shared config routinely
// names providers a given machine has no credentials for, and refusing there would break
// the ordinary case — the launch-time preflight (profiles-as-pack-variants.md §6.2) is
// what turns a MISSING one into a refusal, not this function.
func Resolve(providers *jsonx.OrderedMap, agent, profile, provider string, lookup Lookup) []Var {
	if agent == "" || profile == "" {
		return nil
	}
	var out []Var
	// The provider's own delivery shape first (OQ-14: no agent is special-cased — the
	// provider says how it is delivered, per protocol, and the composition follows the
	// protocol the agent speaks). It reads the composed table — the same table the derives
	// read — so the env an agent is launched with and the config its derive wrote cannot
	// disagree about where a protocol points.
	if prov := mapAt(providers, provider); prov != nil {
		out = append(out, providerVars(prov, ProtocolFor(agent), lookup)...)
	}

	// Bedrock is the only profile that implies environment today, and only for claude:
	// CLAUDE_CODE_USE_BEDROCK is a variable Claude Code reads, not a property of the
	// provider. Extracted verbatim from the jail's assemble path so the two notches
	// cannot drift; a general provider->env mapping is what the env shape above now is,
	// and this block survives only until the Bedrock profile moves into packs/claude.
	if agent == "claude" && profile == "bedrock" {
		out = append(out, bedrockVars(mapAt(providers, profile))...)
	}
	return out
}

// providerVars composes the env shape one provider declares for `protocol` — the
// placeholders resolved against the provider's own entry in the composed table.
//
// A placeholder whose input is missing drops its VAR rather than composing an empty one:
// an empty base URL is a request to the wrong host, and an empty token is a credential
// that gets SENT. The two placeholders are independent, so the half that CAN resolve
// still does. Vars are sorted so the same table composes the same order on every launch
// regardless of which spelling — pack-shipped or user-written — produced it, and a value
// that is neither placeholder renders nothing: packdecl refuses it at authoring time, and
// staying silent here keeps a smuggled template from rendering.
func providerVars(prov *jsonx.OrderedMap, protocol string, lookup Lookup) []Var {
	if protocol == "" {
		return nil
	}
	shape := mapAt(mapAt(prov, "env_shape"), protocol)
	if shape == nil {
		return nil
	}
	endpoint, _ := getStr(mapAt(mapAt(prov, "endpoints"), protocol), "base_url")
	keyName, _ := getStr(prov, "api_key_env")
	names := append([]string(nil), shape.Keys()...)
	sort.Strings(names)
	var out []Var
	for _, name := range names {
		if name == "" {
			continue
		}
		tmpl, ok := getStr(shape, name)
		if !ok || tmpl == "" {
			continue
		}
		switch tmpl {
		case packdecl.EnvShapeEndpoint:
			if endpoint == "" {
				continue
			}
			out = append(out, Var{Key: name, Value: endpoint})
		case packdecl.EnvShapeKey:
			if keyName == "" || lookup == nil {
				continue
			}
			if v, ok := lookup(keyName); ok && v != "" {
				out = append(out, Var{Key: name, Value: v})
			}
		}
	}
	return out
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
