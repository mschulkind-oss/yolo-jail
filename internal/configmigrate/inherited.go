package configmigrate

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// LowerInheritedUserScope is the lowering an inherited user scope crosses into a jail with when
// it mixes a value a TOML file wrote with a legacy setting the readers keep in its legacy
// spelling (config.RenderFilteredForDeliveryLowering, RC-D55): the very conversion `yolo config
// migrate` writes for a user file (lower), over the scope's merged read, with the provider
// table and the loopholes a launch runs proved the same before and after, as a migration's plan
// proves them.
//
// It declines, naming the setting, where the migration would leave a value "not converted", and
// where the conversion rests on more than the scope itself holds: a block for a grant-declaring
// loophole, whose conversion is the user's ceiling and the workspaces' grant records, which only
// a migration of the files moves. The launch then refuses as before, naming `yolo config
// migrate`, which converts the files themselves.
func LowerInheritedUserScope(values *jsonx.OrderedMap) (*jsonx.OrderedMap, error) {
	view := newUserView(values)
	// The jail reads the scope as a TOML user config, where a selected pack's loophole runs on
	// its selection (layerViewOf's switch); on a host whose user config is TOML too, so it does
	// there, and an `enabled: true` beside the selection says nothing more. Anything else the
	// activation proof below catches.
	readable := config.UserConfigIsTOML(paths.UserConfigPath())
	view.selectionSwitch = readable
	packs := packload.Embedded()
	if table, ok := mapAt(values, "loopholes"); ok {
		for _, d := range selectedGrantDecls(packs, view.packs) {
			if _, written := table.Get(d.Loophole); written {
				return nil, fmt.Errorf("config.loopholes.%s: its conversion is the pack's ceiling and each workspace's "+
					"grants, which only converting the file moves", d.Loophole)
			}
		}
	}
	l := lower(jsonx.DeepCopyMap(values), loweringContext{scope: ScopeUser, user: view}, "")
	if len(l.skipped) > 0 {
		nc := l.skipped[0]
		return nil, fmt.Errorf("config.%s: %s", nc.Path, nc.Reason)
	}
	before, _ := providerTable(withMovedProviderKeys(values, l.values), packs)
	after, _ := providerTable(l.values, packs)
	if d := firstDifference(before, after); d != "" {
		return nil, fmt.Errorf("%s would differ at %s", providerTableLayer, d)
	}
	if d := firstDifference(loopholeActivation(values, readable, packs), loopholeActivation(l.values, true, packs)); d != "" {
		return nil, fmt.Errorf("%s would differ at %s", loopholeActivationLayer, d)
	}
	return l.values, nil
}

// firstDifference names the first path at which a and b differ, or "" when they are the same.
func firstDifference(a, b *jsonx.OrderedMap) string {
	diffs := diffValues("", a, b)
	if len(diffs) == 0 {
		return ""
	}
	return strings.TrimPrefix(diffs[0].Path, ".")
}
