// Package agentenv is the process-environment currency an agent's launch is composed
// in: Var (one assignment, or one REMOVAL) and Apply (the overlay that spells both
// halves onto an environ slice).
//
// # Why this is its own package
//
// Var + Apply are the ONE spelling both notches consume: `yolo host` overlays the vars
// on top of os.Environ() before exec, and the jail notch's argv block emits the same
// vars as `-e KEY=VAL` pairs. What COMPOSES the vars is the env-derive runner
// (internal/packload AgentEnv, docs/reference/providers.md — Derives and Why it's this way, OQ-CS8):
// the agent's own pack states the provider→environment binding in its derive.lua, and
// both notches run that one producer — one runner, one Lua producer — which is what
// docs/design/host-agent-environment.md §2.2 claims as jail/host parity: `yolo --
// claude` and `yolo host -- claude` compose the same environment from the same
// resolved profile, a claim two independent compositions could not keep.
//
// # Why the environment channel exists at all
//
// A provider's `api_key_env_name` carries the NAME of a variable, never its value:
// config files are committed and secrets are not. Every agent that reads a credential
// therefore reads it from its own PROCESS environment, so a config surface can route a
// credential but cannot deliver one (host-agent-environment.md §1 P2). The env derive
// does not invent secret values either — it relays one the launch would have carried
// anyway, hydrated from `env_sources` or the invoking environment into the derive
// invocation alone.
package agentenv

// Var is one environment assignment, or one REMOVAL.
//
// Unset is not a cosmetic distinction: `unset AWS_PROFILE` and `AWS_PROFILE=` behave
// differently in every AWS SDK, and the hand-written shell wrapper this whole design
// replaces exists partly to do the former (host-agent-environment.md §2.2). No config
// SURFACE can express a removal at all, which is why its presence alone requires the
// process-env channel. An env derive spells one with ctx.tombstone
// (packload.AgentEnv).
type Var struct {
	Key   string
	Value string
	Unset bool
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
