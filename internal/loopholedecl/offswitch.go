package loopholedecl

import "sort"

// THE OFF SWITCH (docs/design/legacy-config-compat.md LC-D3). A pack's loophole runs whenever its
// pack is selected, and for one whose only approval is a grant, "off" and "on with no grant" give a
// project the same access. Not so for the loopholes below, whose approval is a login or a setting,
// or which hand the jail a host capability with no grant at all: running one hands over the login,
// the credentials or the cgroup delegate. For these, and only these, the owner can keep the
// loophole off for every launch that reads the config: legacy `loopholes.<name>.enabled: false`,
// or, in a TOML user config, `off = true` in its pack's `[allow.<pack>]` table.
//
// The map is loophole name -> the shipped pack that ships it; TestOffSwitchLoopholesShip pins it
// against the official pack embed.
var offSwitchLoopholes = map[string]string{
	"claude-oauth-broker": "claude-auth",
	"openai-auth-broker":  "codex-auth",
	"aws-auth":            "aws-auth",
	"cgroup-delegate":     "cgroup-delegate",
}

// AllowOffKey is the key of a pack's `[allow.<pack>]` table that keeps its off-switch loophole off.
const AllowOffKey = "off"

// HasOffSwitch reports whether the named loophole is one an owner's `enabled: false` (or TOML
// `off = true`) keeps off.
func HasOffSwitch(loophole string) bool {
	_, ok := offSwitchLoopholes[loophole]
	return ok
}

// OffSwitchPack is the shipped pack whose `[allow.<pack>]` table carries the loophole's `off`, and
// whether the loophole has an off switch at all.
func OffSwitchPack(loophole string) (string, bool) {
	pack, ok := offSwitchLoopholes[loophole]
	return pack, ok
}

// OffSwitchLoopholesOf is the off-switch loopholes the named pack ships, sorted; nil for a pack
// that ships none.
func OffSwitchLoopholesOf(pack string) []string {
	var out []string
	for loophole, owner := range offSwitchLoopholes {
		if owner == pack {
			out = append(out, loophole)
		}
	}
	sort.Strings(out)
	return out
}

// OffSwitchLoopholes is every off-switch loophole, sorted.
func OffSwitchLoopholes() []string {
	out := make([]string, 0, len(offSwitchLoopholes))
	for loophole := range offSwitchLoopholes {
		out = append(out, loophole)
	}
	sort.Strings(out)
	return out
}
