package configmigrate

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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
// pack switch decided by the side's user config format: a TOML user config's selection switches
// its ungated loopholes on, a legacy one leaves `default_enabled` standing.
func loopholeActivation(composed *jsonx.OrderedMap, userTop string, packs []*packload.Pack) *jsonx.OrderedMap {
	readable := strings.EqualFold(filepath.Ext(userTop), ".toml")
	selected := newUserView(composed).packs
	var modules []loopholes.PackModule
	for _, p := range packs {
		if !selected[p.Name] {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindLoophole || c.From == "" {
				continue
			}
			modules = append(modules, loopholes.PackModule{Dir: filepath.Join(p.Root, filepath.FromSlash(c.From)), Pack: p.Name, HostExecApproved: true})
		}
	}
	activation := loopholes.PackSwitchActivation(composed, readable, modules)
	names := make([]string, 0, len(activation))
	for name := range activation {
		names = append(names, name)
	}
	sort.Strings(names)
	out := jsonx.NewOrderedMap()
	for _, name := range names {
		a := activation[name]
		entry := jsonx.NewOrderedMap()
		entry.Set("enabled", a.Enabled)
		if a.Enabled && a.Settings != nil && a.Settings.Len() > 0 {
			entry.Set("settings", jsonx.DeepCopyMap(a.Settings))
		}
		out.Set(name, entry)
	}
	return out
}
