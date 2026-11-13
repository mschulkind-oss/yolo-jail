package loopholes

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE PACK SWITCH (docs/reference/config/user-config.md#one-switch-packs, approved 2026-10-25): `packs` is the only
// switch. Every pack loophole runs whenever its pack is selected and grants nothing until a grant
// covers the project (docs/reference/loophole-grants.md §4.1); there is no manifest default,
// and the one config key that decides whether it runs is the off switch of the four loopholes
// whose running hands the jail a login, credentials or the cgroup delegate (LaunchBlock,
// docs/reference/config/rulings.md LC-D3). Under a TOML user config a pack's settings are its
// `[allow.<pack>]` table, read through ConfigBlock.
//
// ONE PREDICATE for the settings shape, read from the user config's format, so every discovery
// surface (the launch, `yolo check`, `yolo loopholes list`, the inert report) answers alike.
// `yolo config migrate` proves a conversion against the explicit form, PackSwitchActivation.

// SelectionIsTheSwitch reports whether the user config is TOML, whose `[allow.<pack>]` tables
// carry the pack's settings.
func SelectionIsTheSwitch() bool {
	return strings.EqualFold(filepath.Ext(paths.UserConfigDisplayPath()), ".toml")
}

// ungatedLoopholes is the shipped loopholes the access design rules ungated whose approval is a
// login or a user-scope setting (docs/reference/loophole-grants.md §4.3, LG-D4): each declares no grant
// kind, so a legacy `loopholes.<name>.enabled: true` for one carries nothing a grant could hold.
var ungatedLoopholes = map[string]bool{"claude-oauth-broker": true, "openai-auth-broker": true, "aws-auth": true}

// allowanceKeys are the keys of an `allow.<source>` table that are a broker's allowance rather
// than a pack setting (docs/reference/config/user-config.md#what-a-jail-may-do-with-your-logins).
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
			if key == loopholedecl.AllowOffKey && len(loopholedecl.OffSwitchLoopholesOf(name)) > 0 {
				// The pack's off switch (LaunchBlock), not a setting its loophole is handed.
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

// LaunchBlock is the `loopholes` block every launch-side set is built from (NewHostSet's
// argument): cfg's own block, plus `<loophole>.enabled = false` for each off-switch loophole of a
// pack whose `[allow.<pack>]` table (the user's) or `[requests.<pack>]` table (the project's,
// LC-D15) sets `off = true`, the TOML spelling of a legacy `enabled: false`
// (docs/reference/config/rulings.md LC-D3). The TOML `off` wins over a legacy
// layer's `enabled: true`, which says nothing selecting the pack does not. The result is a copy
// when it differs; cfg is unchanged.
func LaunchBlock(cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	if cfg == nil {
		return nil
	}
	var block *jsonx.OrderedMap
	if v, ok := cfg.Get("loopholes"); ok {
		block, _ = v.(*jsonx.OrderedMap)
	}
	allowV, _ := cfg.Get("allow")
	allow, _ := allowV.(*jsonx.OrderedMap)
	requestsV, _ := cfg.Get("requests")
	requests, _ := requestsV.(*jsonx.OrderedMap)
	if allow == nil && requests == nil {
		return block
	}
	var packs []string
	for _, table := range []*jsonx.OrderedMap{allow, requests} {
		if table != nil {
			packs = append(packs, table.Keys()...)
		}
	}
	out := block
	for _, pack := range packs {
		if !AllowOff(allow, pack) && !AllowOff(requests, pack) {
			continue
		}
		for _, loophole := range loopholedecl.OffSwitchLoopholesOf(pack) {
			if out == block {
				out = jsonx.NewOrderedMap()
				if block != nil {
					out = jsonx.DeepCopyMap(block)
				}
			}
			entry, _ := getMapEntry(out, loophole)
			entry.Set("enabled", false)
		}
	}
	return out
}

// AllowOff reports whether the table's `<pack>` entry, an `[allow.<pack>]` or a project's
// `[requests.<pack>]`, sets `off = true` (LaunchBlock).
func AllowOff(allow *jsonx.OrderedMap, pack string) bool {
	if allow == nil {
		return false
	}
	tv, _ := allow.Get(pack)
	table, _ := tv.(*jsonx.OrderedMap)
	if table == nil {
		return false
	}
	off, _ := table.Get(loopholedecl.AllowOffKey)
	b, _ := off.(bool)
	return b
}

// ConfiguredSetting reads a loophole's setting the way the settings readers do (ConfigBlock): under a TOML
// user config an `allow.<pack>` table's key counts, a legacy layer's `loopholes.<pack>.settings`
// key winning. The value is a trimmed string, "" when unset or not a string. Pass it wherever a
// reader asks for a `func(loophole, key string) string` (packload.LoopholeSettingIn's shape, which
// reads only the `loopholes` block and so misses a TOML user's `[allow.<pack>]` table).
func ConfiguredSetting(cfg *jsonx.OrderedMap) func(loophole, key string) string {
	block := ConfigBlock(cfg)
	return func(loophole, key string) string {
		if block == nil {
			return ""
		}
		entryV, _ := block.Get(loophole)
		entry, _ := entryV.(*jsonx.OrderedMap)
		if entry == nil {
			return ""
		}
		settingsV, _ := entry.Get("settings")
		settings, _ := settingsV.(*jsonx.OrderedMap)
		if settings == nil {
			return ""
		}
		v, _ := settings.Get(key)
		s, _ := v.(string)
		return strings.TrimSpace(s)
	}
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
		// The TOML off switches count exactly where the launch counts them (LaunchBlock, which
		// every launch applies whatever the user config's format), so a conversion that drops a
		// legacy `enabled: false` without writing `off = true` is a loophole that starts
		// running, which the proof reports.
		loopholesBlock = LaunchBlock(cfg)
	}
	out := map[string]Activation{}
	for _, lp := range Discover(DiscoverOptions{IncludeDisabled: true, LoopholesConfig: loopholesBlock,
		PackModules: modules}) {
		out[lp.Name] = Activation{Enabled: lp.Enabled, Settings: withoutGrantKinds(lp, WithProjectRequestSettings(cfg, lp, SuppliedSettings(block, lp)))}
	}
	return out
}

// RunsOnSelection reports whether the loophole name is one of the ungated shipped loopholes, for
// which a legacy `loopholes.<name>.enabled: true` means nothing beyond selecting its pack.
// `yolo config migrate` asks before it drops one; any other loophole's switch becomes a grant.
func RunsOnSelection(name string) bool { return ungatedLoopholes[name] }

// WithProjectRequestSettings is supplied, a loophole's settings as the block reads them, with each
// request-scope setting lp declares that cfg's project `[requests.<pack>]` table writes laid over
// it (docs/reference/loophole-grants.md LG-D15, docs/reference/config/rulings.md LC-D17): the
// project's value replaces the user's `[allow.<pack>]` one for this project, never merged with it.
// Every reader of a loophole's settings that a launch, `yolo check` or `yolo config migrate`'s proof
// uses goes through it, so all three hand the daemon the same values. supplied is not changed.
func WithProjectRequestSettings(cfg *jsonx.OrderedMap, lp *Loophole, supplied *jsonx.OrderedMap) *jsonx.OrderedMap {
	if cfg == nil || lp == nil {
		return supplied
	}
	requestsV, _ := cfg.Get("requests")
	requests, _ := requestsV.(*jsonx.OrderedMap)
	if requests == nil {
		return supplied
	}
	pack := lp.Pack
	if pack == "" {
		pack = lp.Name
	}
	tableV, _ := requests.Get(pack)
	table, _ := tableV.(*jsonx.OrderedMap)
	if table == nil {
		return supplied
	}
	var out *jsonx.OrderedMap
	for _, decl := range lp.Settings {
		if decl.Scope != loopholedecl.SettingScopeRequest {
			continue
		}
		v, ok := table.Get(decl.Key)
		if !ok {
			continue
		}
		if out == nil {
			out = jsonx.NewOrderedMap()
			if supplied != nil {
				out = jsonx.DeepCopyMap(supplied)
			}
		}
		out.Set(decl.Key, v)
	}
	if out == nil {
		return supplied
	}
	return out
}

// withoutGrantKinds is settings less each key that names one of lp's grant kinds: the
// user-sourced request an `[allow.<pack>]` table writes beside the pack's settings (LC-D16),
// which ConfigBlock lays among them and the daemon is never handed (ResolveSettings).
func withoutGrantKinds(lp *Loophole, settings *jsonx.OrderedMap) *jsonx.OrderedMap {
	if lp == nil || settings == nil {
		return settings
	}
	var out *jsonx.OrderedMap
	for _, g := range lp.Grants {
		if _, has := settings.Get(g.Kind); !has {
			continue
		}
		if out == nil {
			out = jsonx.DeepCopyMap(settings)
		}
		out.Delete(g.Kind)
	}
	if out == nil {
		return settings
	}
	if out.Len() == 0 {
		return nil
	}
	return out
}
