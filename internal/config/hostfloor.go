package config

import "github.com/mschulkind-oss/yolo-jail/internal/jsonx"

// hostfloor.go is the `host_floor` key: which selected packs' programs yolo keeps in the HOST
// AGENT FLOOR (docs/design/host-tool-provisioning.md) — its own host prefix, from which
// `yolo host -- <agent>` runs them whatever PATH its launcher held.
//
// # The value, and why it is agent_updates' shape
//
// ON BY DEFAULT (OQ-HP1, ruled 2026-09-29: "this floor should be on by default … Obviously, let
// it be configured. You could even configure a floor of nothing"). So the key is an opt-OUT, and
// it takes exactly `agent_updates`' two shapes, read by the same rule:
//
//	"host_floor": false                              // a floor of nothing
//	"host_floor": { "*": true, "claude": false }     // every selected pack's but claude's
//
// THE KEY IS A PACK NAME, as `agent_updates`' is: one pack may declare more than one program, and
// the pack is the thing the user selected. A specific key beats "*"; "*" beats absence; absence is
// true. A pack left out has NO FLOOR ENTRY, and what `yolo host` runs for it then is the launch's
// PATH, said on a line of its own (host-agent-environment.md OQ-HE11).
//
// # Why it is read from the USER config directly
//
// The floor installs executables the host runs with the user's full authority, and the floor is
// built from the user-scope selection for the reason `packs` is user-scope: a workspace config is
// agent-editable. A workspace spelling of this key is refused by validateHostFloor and never read.
const hostFloorKey = "host_floor"

// HostFloorWire renders the user's `host_floor` value as compact JSON, "" when absent — the wire
// shape `agent_updates` uses, so both keys are read by one reader
// (entrypoint.PackPolicyAllows).
func HostFloorWire() string {
	return hostFloorWire(UserScopeConfigOrEmpty())
}

func hostFloorWire(cfg *jsonx.OrderedMap) string {
	v, present := cfg.Get(hostFloorKey)
	if !present || v == nil {
		return ""
	}
	wire, err := jsonx.DumpsCompact(v)
	if err != nil {
		return ""
	}
	return wire
}
