package loopholes

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE PACK SWITCH (docs/design/config-user.md §7, approved 2026-10-25): in a TOML user config
// `packs` is the only switch, and `loopholes.<name>.enabled` has no spelling. Its settings are
// the pack's `[allow.<pack>]` table. A legacy JSONC user config keeps the old rule: the
// manifest's `default_enabled`, overridden by `loopholes.<name>.enabled`.
//
// ONLY THE UNGATED LOOPHOLES run on selection yet (ungatedOnSelection). The governing design,
// docs/design/loophole-access-approval.md §4.1, runs EVERY selected loophole and grants nothing
// until a grant covers the project; the grant layer is not built, and running a gated loophole
// (journal, host-processes, audio, ...) without it would grant everything at once (§1). Those
// keep `default_enabled` until it lands.
//
// An explicit `loopholes.<name>.enabled` in the merged config still decides in both modes: a
// TOML file cannot write one, so under a TOML user config it can only come from a legacy layer
// (a JSONC `--user-layer`, a workspace file), and that layer's answer outranks the selection the
// way it outranks `default_enabled` in a legacy config.
//
// ONE PREDICATE for the mode, read from the user config's format, so every discovery surface
// (the launch, `yolo check`, `yolo loopholes list`, the inert report) answers alike.
// `yolo config migrate` proves a conversion against the explicit form, PackSwitchActivation.

// SelectionIsTheSwitch reports whether the user config is TOML, where selecting a pack switches
// its loopholes on.
func SelectionIsTheSwitch() bool {
	return strings.EqualFold(filepath.Ext(paths.UserConfigDisplayPath()), ".toml")
}

// selectionSwitches resolves DiscoverOptions.SelectionSwitch: the caller's answer when it gave
// one, else the user config's format.
func selectionSwitches(opts DiscoverOptions) bool {
	if opts.SelectionSwitch != nil {
		return *opts.SelectionSwitch
	}
	return SelectionIsTheSwitch()
}

// ungatedOnSelection is the shipped loopholes the access design rules ungated whose approval is a
// login or a user-scope setting (loophole-access-approval.md §4.3, LG-D4): each runs on selection
// and serves nothing until a human logs in or sets a profile. `cgroup-delegate`, the fourth
// ungated one, is selected by default under LG-D13, which is not built; it is left out here with
// the gated ones.
var ungatedLoopholes = map[string]bool{"claude-oauth-broker": true, "openai-auth-broker": true, "aws-auth": true}

// ungatedOnSelection is the subset the switch has to turn on: the two login services already
// ship `default_enabled = true`, so selection runs them in either mode, and a test fixture that
// declares one off keeps meaning off.
var ungatedOnSelection = map[string]bool{"aws-auth": true}

// switchOnSelected turns on every selected pack's ungated loophole (ungatedOnSelection). The
// config block's explicit `enabled` is applied afterwards and wins.
func switchOnSelected(byName map[string]*Loophole) {
	for _, lp := range byName {
		if onBySelection(lp) {
			lp.Enabled = true
		}
	}
}

// onBySelection is whether selecting lp's pack runs it under the pack switch.
func onBySelection(lp *Loophole) bool {
	return lp != nil && lp.Source == SourcePack && SwitchedByConfig(lp) && ungatedOnSelection[lp.Name]
}

// allowanceKeys are the keys of an `allow.<source>` table that are a broker's allowance rather
// than a pack setting (docs/design/config-user.md §6).
var allowanceKeys = map[string]bool{"always": true, "on_request": true}

// ConfigBlock is the merged config's `loopholes` block as the settings readers use it: under a
// TOML user config, each `allow.<pack>` table's setting keys land in
// `loopholes.<pack>.settings`, a key the block already sets winning (a legacy layer's). A shipped
// pack's settings-bearing loophole has its pack's name, which is the mapping `yolo config migrate`
// writes (`loopholes.<p>.settings` becomes `allow.<p>`). The result is a copy; cfg is unchanged.
func ConfigBlock(cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	return configBlock(cfg, SelectionIsTheSwitch())
}

func configBlock(cfg *jsonx.OrderedMap, readable bool) *jsonx.OrderedMap {
	if cfg == nil {
		return nil
	}
	var block *jsonx.OrderedMap
	if v, ok := cfg.Get("loopholes"); ok {
		block, _ = v.(*jsonx.OrderedMap)
	}
	if !readable {
		return block
	}
	allowV, _ := cfg.Get("allow")
	allow, ok := allowV.(*jsonx.OrderedMap)
	if !ok || allow.Len() == 0 {
		return block
	}
	out := jsonx.NewOrderedMap()
	if block != nil {
		out = jsonx.DeepCopyMap(block)
	}
	for _, name := range allow.Keys() {
		if name == "mounts" {
			// Core's own ceiling for writable mounts, not a pack's settings.
			continue
		}
		tv, _ := allow.Get(name)
		table, ok := tv.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		for _, key := range table.Keys() {
			if allowanceKeys[key] {
				continue
			}
			value, _ := table.Get(key)
			entry, _ := getMapEntry(out, name)
			settings, _ := getMapEntry(entry, "settings")
			if _, set := settings.Get(key); !set {
				settings.Set(key, value)
			}
		}
	}
	return out
}

// getMapEntry returns m[key] as a map, creating it when absent (or replacing a non-map).
func getMapEntry(m *jsonx.OrderedMap, key string) (*jsonx.OrderedMap, bool) {
	if v, ok := m.Get(key); ok {
		if sub, isMap := v.(*jsonx.OrderedMap); isMap {
			return sub, true
		}
	}
	sub := jsonx.NewOrderedMap()
	m.Set(key, sub)
	return sub, false
}

// Activation is one loophole a launch would plan: whether it runs, and the settings it is handed.
type Activation struct {
	Enabled  bool
	Settings *jsonx.OrderedMap
}

// PackSwitchActivation is which of modules' loopholes cfg runs and with which supplied settings,
// with the pack switch decided by readable rather than by the user config on disk: the launch's
// own rule (Discover and ConfigBlock), for `yolo config migrate` to prove a conversion keeps it.
func PackSwitchActivation(cfg *jsonx.OrderedMap, readable bool, modules []PackModule) map[string]Activation {
	block := configBlock(cfg, readable)
	var loopholesBlock *jsonx.OrderedMap
	if cfg != nil {
		if v, ok := cfg.Get("loopholes"); ok {
			loopholesBlock, _ = v.(*jsonx.OrderedMap)
		}
	}
	out := map[string]Activation{}
	for _, lp := range Discover(DiscoverOptions{IncludeDisabled: true, LoopholesConfig: loopholesBlock,
		PackModules: modules, SelectionSwitch: &readable}) {
		out[lp.Name] = Activation{Enabled: lp.Enabled, Settings: SettingsFromConfig(block, lp.Name)}
	}
	return out
}

// RunsOnSelection reports whether selecting the pack that ships the loophole name runs it under
// a TOML user config. `yolo config migrate` asks before it drops a legacy
// `loopholes.<name>.enabled: true`: for any other loophole the TOML spelling has no switch yet.
func RunsOnSelection(name string) bool { return ungatedLoopholes[name] }
