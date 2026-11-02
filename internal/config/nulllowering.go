package config

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// LowerLegacyNulls respells, in place, every legacy `null` in values (a config already in the
// readable key spelling) at a path the readable schema gives a removal spelling: an empty list for
// a list that a null replaced (`install.nix = []`, the readable spelling of a legacy
// `"packages": null`), and "off" for a table it disabled (`mcp_servers.<name> = "off"`). TOML has
// no null, and each of those spellings says in TOML what the null said in JSONC
// (ReadableRemovalPolicies), so the lowering is exact (RC-D50).
//
// It is the ONE null lowering, shared by `yolo config migrate` (configmigrate.lower) and the
// inherited user scope a launch delivers as TOML (RenderFilteredForDelivery). It returns the
// dotted paths of the nulls it could not respell, where the readable schema has no removal
// spelling; values keeps those nulls.
func LowerLegacyNulls(values *jsonx.OrderedMap) []string {
	var left []string
	var walk func(m *jsonx.OrderedMap, path []string)
	walk = func(m *jsonx.OrderedMap, path []string) {
		for _, key := range m.Keys() {
			child, _ := m.Get(key)
			at := appendReadablePath(path, key)
			switch current := child.(type) {
			case nil:
				switch readableMergeRemovalPolicy(at) {
				case RemovalEmptyList:
					m.Set(key, []any{})
				case RemovalOffString:
					m.Set(key, "off")
				default:
					left = append(left, strings.Join(at, "."))
				}
			case *jsonx.OrderedMap:
				walk(current, at)
			}
		}
	}
	if values != nil {
		walk(values, nil)
	}
	return left
}
