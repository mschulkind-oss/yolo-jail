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

// PackTimingDecision is WHEN a built tree, or anything else several packs govern, updates
// (docs/design/pi-extension-store-builds.md §7.6, XB-D18): AgentUpdatesAtLaunch or
// AgentUpdatesNextLaunch. packs are the governing packs in precedence order — for a built tree its
// owning agent pack, then its contributing pack — and the first one with a valid entry of its own
// decides, then "*", then a top-level value, then the default, at the launch. A false counts as at
// the launch: whether anything moves at all is the hold's reader's (PackPolicyDecision,
// run.PatchedForkHold), and a held tree checks nothing in either mode.
//
// It extends OQ-PD31's "a pack's own entry beats `*` whole" (docs/design/program-delivery.md) to a
// second governing pack: a tree's contributing pack's own entry beats `*` too, and its owner's beats
// both.
func PackTimingDecision(wire string, packs ...string) string {
	if wire == "" {
		return AgentUpdatesAtLaunch
	}
	decoded, err := jsonx.Decode([]byte(wire))
	if err != nil {
		return AgentUpdatesAtLaunch
	}
	m, isMap := decoded.(*jsonx.OrderedMap)
	if !isMap {
		if _, timing, ok := packPolicySetting(decoded); ok {
			return timing
		}
		return AgentUpdatesAtLaunch
	}
	for _, key := range append(append([]string(nil), packs...), "*") {
		if key == "" {
			continue
		}
		if v, present := m.Get(key); present {
			if _, timing, ok := packPolicySetting(v); ok {
				return timing
			}
		}
	}
	return AgentUpdatesAtLaunch
}
