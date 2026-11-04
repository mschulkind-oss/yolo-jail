package configmigrate

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// loopholeActivationLayer is the equality check's layer for which loopholes a launch runs and
// the settings it hands each: the per-scope layers compare `loopholes.<p>` lowered to
// `allow.<p>`, and this compares what the launch's discovery makes of each spelling. Without it a
// conversion that dropped `loopholes.aws-auth.enabled` proved equal while the converted launch
// left aws-auth off.
const loopholeActivationLayer = "the loopholes a launch runs"

// loopholeActivation is which shipped loopholes composed, one side's merged read, runs and with
// which supplied settings, through the launch's own rule (loopholes.PackSwitchActivation) with the
// pack switch decided by the side's user config format (readable): a TOML user config's selection
// switches its ungated loopholes on, a legacy one leaves `default_enabled` standing.
func loopholeActivation(composed *jsonx.OrderedMap, readable bool, packs []*packload.Pack) *jsonx.OrderedMap {
	selected := newUserView(composed).packs
	var modules []loopholes.PackModule
	// unselected is each loophole a pack this side does not select ships. A legacy entry naming
	// one, with no command of its own, is that pack's config and runs nothing, on either side
	// (docs/design/legacy-config-compat.md LC-D9's M7 row): discovery reads it as a commandless
	// inline record, which is not a loophole a launch runs.
	unselected := map[string]bool{}
	for _, p := range packs {
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindLoophole || c.From == "" {
				continue
			}
			if !selected[p.Name] {
				unselected[filepath.Base(filepath.FromSlash(c.From))] = true
				continue
			}
			modules = append(modules, loopholes.PackModule{Dir: filepath.Join(p.Root, filepath.FromSlash(c.From)), Pack: p.Name, HostExecApproved: true})
		}
	}
	activation := loopholes.PackSwitchActivation(composed, readable, modules)
	names := make([]string, 0, len(activation))
	for name := range activation {
		if unselected[name] && !entryHasCommand(composed, name) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	gated := map[string]config.LoopholeGrantDeclaration{}
	for _, d := range selectedGrantDecls(packs, selected) {
		gated[d.Loophole] = d
	}
	out := jsonx.NewOrderedMap()
	for _, name := range names {
		a := activation[name]
		entry := jsonx.NewOrderedMap()
		if d, isGated := gated[name]; isGated {
			// A grant-declaring loophole's access is its grants, which the standing layer
			// compares (loopholeStandingLayer): running is not access (docs/reference/loophole-grants.md
			// §4.1). Its settings are compared whether or not it runs, less the ones a grant
			// replaced (journal's `full`, a `visible` list).
			if settings := withoutLegacyGrantKeys(d, a.Settings); settings.Len() > 0 {
				entry.Set("settings", settings)
			}
			out.Set(name, entry)
			continue
		}
		entry.Set("enabled", a.Enabled)
		if a.Enabled && a.Settings != nil && a.Settings.Len() > 0 {
			entry.Set("settings", jsonx.DeepCopyMap(a.Settings))
		}
		out.Set(name, entry)
	}
	return out
}

// entryHasCommand is whether composed's `loopholes.<name>` entry writes a `command`, which makes it
// an inline loophole of its own rather than a pack's config.
func entryHasCommand(composed *jsonx.OrderedMap, name string) bool {
	if composed == nil {
		return false
	}
	block, _ := composed.Get("loopholes")
	m, _ := block.(*jsonx.OrderedMap)
	if m == nil {
		return false
	}
	entry, _ := m.Get(name)
	em, _ := entry.(*jsonx.OrderedMap)
	if em == nil {
		return false
	}
	_, ok := em.Get("command")
	return ok
}

func withoutLegacyGrantKeys(d config.LoopholeGrantDeclaration, settings *jsonx.OrderedMap) *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	if settings == nil {
		return out
	}
	for _, k := range settings.Keys() {
		if setting, values := legacyGrantKey(d, k); setting || values {
			continue
		}
		v, _ := settings.Get(k)
		out.Set(k, v)
	}
	return out
}

// activationNextSteps names, on each line of the activation layer, the step that makes it agree
// (AGENTS.md: every stop names the next step). A path is `<loophole>.enabled` or
// `<loophole>.settings`.
func activationNextSteps(diffs []Difference) []Difference {
	for i, d := range diffs {
		name, field, ok := strings.Cut(d.Path, ".")
		if !ok {
			continue
		}
		pack, offSwitch := loopholedecl.OffSwitchPack(name)
		if !offSwitch {
			pack = name // a shipped pack's other loopholes carry their pack's name
		}
		switch {
		case field == "enabled" && d.After == "true" && offSwitch:
			// An off-switch loophole runs whenever its pack is selected (LC-D3), so the old file
			// keeps it off with a switch, and no "enabled": true runs it.
			diffs[i].Next = "the converted config runs " + name + " because its pack is selected, and the old one keeps it off; write `off = true` under [allow." + pack +
				"] in the TOML user config to keep it off, or remove the `\"enabled\": false` that keeps it off in the old file to let it run, then run `yolo config migrate` again"
		case field == "enabled" && d.After == "true":
			diffs[i].Next = "the converted config runs " + name + " because its pack is selected, and the old one does not; add \"enabled\": true under loopholes." + name +
				" in the old file to keep it running, or remove \"" + name + "\" from packs to keep it off, then run `yolo config migrate` again"
		case field == "enabled":
			diffs[i].Next = "the converted config does not run " + name + ", and the old one does; a TOML config runs a loophole when its pack is selected, so add the pack " +
				"(`yolo pack add " + pack + "`) to the old file's packs, or keep the old file (this yolo keeps reading it), then run `yolo config migrate` again"
		case field == "settings":
			diffs[i].Next = "the converted [allow." + name + "] table would hand " + name + " different settings; write them under loopholes." + name +
				".settings with \"enabled\": true in the old file, then run `yolo config migrate` again"
		}
	}
	return diffs
}
