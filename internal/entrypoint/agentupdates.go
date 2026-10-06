package entrypoint

// agentupdates.go is the JAIL half of the `agent_updates` policy
// (docs/design/program-delivery.md §3.5, OQ-PD12). The HOST half — the user-scope config
// key and the three sites that put it on the wire — is internal/config/agentupdates.go.
//
// The wire value is the config value verbatim: `true`, `false`, a TIMING ("launch" or
// "next-launch", OQ-PD30), or a per-pack object of those keyed by PACK NAME with `"*"` as
// the default key. The key is a pack name and not a bin name because one pack may declare
// more than one program, and the unit a user reasons about is the pack they selected.

import (
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

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

// agentUpdatesRefreshTiming is when pack's pre-launch refresh runs in this jail (OQ-PD30):
// config.AgentUpdatesNextLaunch for one the user moved to the background, and
// config.AgentUpdatesAtLaunch otherwise. The launcher generator bakes it, beside the
// UPDATES_ENABLED agentUpdatesAllows decides.
func agentUpdatesRefreshTiming(e *Env, pack string) string {
	return refreshTimingValue(e.Getenv(AgentUpdatesEnv), pack)
}

// PackPolicyAllows is agentUpdatesAllows' reading over a wire value the caller already holds:
// `true`, `false`, a timing, or a per-pack object with "*" as the default key, open when absent
// or unparseable. It is THE reader of that shape at the host too — `agent_updates` for the host
// agent floor's evergreen refresh, and `host_floor` (config.HostFloorWire), which takes the same
// shape by design — so the two notches, and the two keys, cannot come to read one value
// differently (docs/design/host-tool-provisioning.md). A timing is a yes: it says WHEN a pack's
// pre-launch refresh runs, and the host notch runs none (OQ-PD31). `host_floor`'s validator
// refuses a timing, so one reaches this reader there only from a config `yolo check` reports.
func PackPolicyAllows(wire, pack string) bool { return agentUpdatesValue(wire, pack) }

// agentUpdatesValue is the reading, split from the Env lookup so the generator and the
// tests exercise one implementation of the precedence rule — the same split
// config.hostApplyOnLaunchValue makes, for the same reason.
func agentUpdatesValue(wire, pack string) bool {
	allowed, _ := agentUpdatesDecision(wire, pack)
	return allowed
}

// refreshTimingValue is agentUpdatesRefreshTiming's reading, split for the same reason.
func refreshTimingValue(wire, pack string) string {
	_, timing := agentUpdatesDecision(wire, pack)
	return timing
}

// agentUpdatesDecision is the ONE reading of the wire: whether pack may move, and when its
// pre-launch refresh runs. Both answers come from one setting, chosen once — the pack's own
// entry when it is a valid setting, else "*"'s, else the default (allowed, at launch) — so a
// specific `true` beats a `"*": "next-launch"` whole rather than inheriting its timing.
func agentUpdatesDecision(wire, pack string) (allowed bool, timing string) {
	return config.PackPolicyDecision(wire, pack)
}
