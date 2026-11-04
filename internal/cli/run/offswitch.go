package run

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// LoopholeOff is one off-switch loophole a launch's config keeps off
// (docs/design/legacy-config-compat.md LC-D3), with each switch that holds it off and the
// command that removes that switch.
type LoopholeOff struct {
	Loophole string
	// Switches are the switches that keep it off, each with where it is written; every one must go
	// for the loophole to run.
	Switches []OffSwitchAt
}

// OffSwitchAt is one switch: what it says, where, and the one command that removes it.
type OffSwitchAt struct {
	Said, Where, Command string
}

// LoopholesOff is every selected pack's off-switch loophole cfg keeps off for a launch in
// workspace, with the switches that hold it off. The TOML user config's `[allow.<pack>] off = true`
// is removed with `yolo config unset`; a legacy `loopholes.<name>.enabled: false` with
// `yolo config unset` at the scope that wrote it; a per-workspace file's with
// `yolo loopholes disable <name>`, the command that removes an older yolo's switch from it. The
// per-workspace file is merged last, so when it holds the switch it alone decides.
func LoopholesOff(cfg *jsonx.OrderedMap, workspace string) []LoopholeOff {
	allow, requests := cfgMap(cfg, "allow"), cfgMap(cfg, "requests")
	var out []LoopholeOff
	for _, lp := range loopholes.NewHostSet(loopholes.LaunchBlock(cfg)).All() {
		if lp.Source != loopholes.SourcePack || lp.Enabled || !loopholedecl.HasOffSwitch(lp.Name) {
			continue
		}
		out = append(out, LoopholeOff{Loophole: lp.Name, Switches: offSwitchesOf(lp.Name, allow, requests, workspace)})
	}
	return out
}

func offSwitchesOf(name string, allow, requests *jsonx.OrderedMap, workspace string) []OffSwitchAt {
	enabledKey := "loopholes." + name + ".enabled"
	legacySaid := "`" + enabledKey + ": false`"
	var out []OffSwitchAt
	pack, _ := loopholedecl.OffSwitchPack(name)
	if loopholes.AllowOff(allow, pack) {
		key := "allow." + pack + "." + loopholedecl.AllowOffKey
		out = append(out, OffSwitchAt{Said: "`" + key + " = true`", Where: "your user config", Command: "yolo config unset " + key})
	}
	if loopholes.AllowOff(requests, pack) {
		// The project's off (LC-D15), which `yolo config migrate` writes for a workspace
		// `enabled: false`.
		key := "requests." + pack + "." + loopholedecl.AllowOffKey
		out = append(out, OffSwitchAt{Said: "`" + key + " = true`", Where: "the workspace config",
			Command: "yolo config unset " + key + " --project"})
	}
	if workspace != "" {
		if wf := config.ReadWorkspaceFile(workspace); wf != nil {
			if on, set := wf.LoopholeSwitch(name); set {
				if on {
					return out
				}
				return append(out, OffSwitchAt{Said: legacySaid, Where: "this workspace's per-workspace file (" + wf.Path + ")",
					Command: "yolo loopholes disable " + name})
			}
		}
		if sw, set := config.WorkspaceLoopholeSwitches(workspace)[name]; set && !sw.Enabled {
			out = append(out, OffSwitchAt{Said: legacySaid, Where: "the workspace config " + sw.File,
				Command: "yolo config unset " + enabledKey + " --project"})
		}
	}
	if block := cfgMap(config.UserScopeConfigOrEmpty(), "loopholes"); block != nil {
		if entry := cfgMap(block, name); entry != nil {
			if v, _ := entry.Get("enabled"); v == false {
				out = append(out, OffSwitchAt{Said: legacySaid, Where: "your user config", Command: "yolo config unset " + enabledKey + " --user"})
			}
		}
	}
	if len(out) == 0 {
		// A config composed from no file this launch can name (a test's, or a frozen copy's).
		out = append(out, OffSwitchAt{Said: legacySaid, Where: "your config", Command: "yolo config unset " + enabledKey})
	}
	return out
}

// noteLoopholesOff discloses, on the launch stream, each loophole LoopholesOff finds: off, it
// starts no daemon and serves the jail nothing, so a user whose agent finds no login or no
// credentials reads why here, with the commands that turn it back on.
func (o *Options) noteLoopholesOff(cfg *jsonx.OrderedMap) {
	for _, off := range LoopholesOff(cfg, o.Workspace) {
		var where, steps []string
		for _, sw := range off.Switches {
			where = append(where, sw.Where+" sets "+sw.Said)
			steps = append(steps, "`"+sw.Command+"`")
		}
		o.pr(o.Stderr).print("[yellow]loophole " + richtext.Escape(off.Loophole) + " is off for this launch: " +
			richtext.Escape(strings.Join(where, ", and ")) + ", so it starts nothing and nothing it serves reaches this jail. " +
			"To run it, " + richtext.Escape(strings.Join(steps, ", then ")) + ".[/yellow]")
	}
}
