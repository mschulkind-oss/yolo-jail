package config

import "github.com/mschulkind-oss/yolo-jail/internal/jsonx"

// PackPolicyDecision reads the shared boolean or refresh-timing policy for a pack:
// a valid specific entry beats "*", which beats the open, at-launch default.
// This is the common reader for host_floor and agent_updates; see
// docs/design/host-tool-provisioning.md and docs/design/program-delivery.md §3.5.
// Moving the read here lets configuration validation use the production rule
// without importing entrypoint, which already depends on config.
func PackPolicyDecision(wire, pack string) (allowed bool, timing string) {
	if wire == "" {
		return true, AgentUpdatesAtLaunch
	}
	decoded, err := jsonx.Decode([]byte(wire))
	if err != nil {
		return true, AgentUpdatesAtLaunch
	}
	if m, ok := decoded.(*jsonx.OrderedMap); ok {
		for _, key := range []string{pack, "*"} {
			if v, present := m.Get(key); present {
				if allowed, timing, ok := packPolicySetting(v); ok {
					return allowed, timing
				}
			}
		}
		return true, AgentUpdatesAtLaunch
	}
	if allowed, timing, ok := packPolicySetting(decoded); ok {
		return allowed, timing
	}
	// A shape nobody ruled on — a list, a number, an unknown string. The host validator
	// refuses it; if one reaches here the launch has already been reported on, and freezing
	// every agent over it, or moving work out of the user's sight, would be the wrong
	// direction to fail in.
	return true, AgentUpdatesAtLaunch
}

// packPolicySetting reads one value, reporting whether it is a setting at all: a bool, or one
// of the two timing strings, each of which lets the pack move. Anything else (null, a number, an
// unknown string) is not, so a map entry holding it is treated as absent and "*" still applies.
func packPolicySetting(v any) (allowed bool, timing string, ok bool) {
	switch t := v.(type) {
	case bool:
		return t, AgentUpdatesAtLaunch, true
	case string:
		if t == AgentUpdatesAtLaunch || t == AgentUpdatesNextLaunch {
			return true, t, true
		}
	}
	return false, "", false
}
