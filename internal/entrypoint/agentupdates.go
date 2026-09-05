package entrypoint

// agentupdates.go is the JAIL half of the `agent_updates` policy
// (docs/design/program-delivery.md §3.5, OQ-PD12). The HOST half — the user-scope config
// key and the three sites that put it on the wire — is internal/config/agentupdates.go.
//
// The wire value is the config value verbatim: `true`, `false`, or a per-pack object
// keyed by PACK NAME with `"*"` as the default key. The key is a pack name and not a bin
// name because one pack may declare more than one program, and the unit a user reasons
// about is the pack they selected.

import "github.com/mschulkind-oss/yolo-jail/internal/jsonx"

// AgentUpdatesEnv carries the policy from the host into the jail. It is a host↔jail
// contract in the class of YOLO_MCP_PRESETS: emitted by the run pipeline's `-e` list, by
// `yolo check`'s preflight env, and by macos-user's bootstrap env.
const AgentUpdatesEnv = "YOLO_AGENT_UPDATES"

// agentUpdatesAllows reports whether pack's programs may update themselves in this jail.
//
// THE DEFAULT IS OPEN, and it INVERTS the nearest precedent in the tree deliberately.
// config.HostApplyOnLaunchEnabled fails CLOSED — an unreadable config has not granted an
// opt-in to write into the user's real home. Here the key is an opt-OUT of a policy that
// is on by ruling, so an absent, empty or unparseable value must mean `true`: a faithful
// copy of that file would silently freeze every agent in every jail, which is the exact
// state §3.5 exists to end and the exact state nobody would notice (the plan calls this
// out as trap 9).
//
// A SPECIFIC KEY BEATS "*", and "*" beats absence. A pack may not opt itself out: the
// pack declares HOW to update (OQ-PD14), never WHETHER to.
func agentUpdatesAllows(e *Env, pack string) bool {
	return agentUpdatesValue(e.Getenv(AgentUpdatesEnv), pack)
}

// agentUpdatesValue is the reading, split from the Env lookup so the generator and the
// tests exercise one implementation of the precedence rule — the same split
// config.hostApplyOnLaunchValue makes, for the same reason.
func agentUpdatesValue(wire, pack string) bool {
	if wire == "" {
		return true
	}
	decoded, err := jsonx.Decode([]byte(wire))
	if err != nil {
		return true
	}
	switch v := decoded.(type) {
	case bool:
		return v
	case *jsonx.OrderedMap:
		if b, ok := agentUpdatesBool(v, pack); ok {
			return b
		}
		if b, ok := agentUpdatesBool(v, "*"); ok {
			return b
		}
		return true
	default:
		// A shape nobody ruled on — a list, a number, a string. The host validator
		// refuses it; if one reaches here the launch has already been reported on, and
		// freezing every agent over it would be the wrong direction to fail in.
		return true
	}
}

// agentUpdatesBool reads one key, reporting whether it was present AND a bool. A key
// present with a non-bool value is treated as absent so the "*" fallback still applies.
func agentUpdatesBool(m *jsonx.OrderedMap, key string) (bool, bool) {
	v, present := m.Get(key)
	if !present || v == nil {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}
