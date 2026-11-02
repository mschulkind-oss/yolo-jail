package config

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// LowerLegacyNulls respells, in place, every legacy `null` in values (a config already in the
// readable key spelling) at a path the readable schema gives a removal spelling: an empty list for
// a list that a null replaced (`install.nix = []`, the readable spelling of a legacy
// `"packages": null`; `security.blocked_tools = []`), "off" for a table or entry it dropped
// (`mcp_servers.<name> = "off"`, `lsp_servers.<name> = "off"`, `install.mise.<tool> = "off"`), and
// "native" for an agent's selection (`active_provider.<agent> = "native"`, what the legacy
// `profile` key's null always meant). TOML has no null, and each of those spellings says in TOML
// what the null said in JSONC (ReadableRemovalPolicies, RC-D50, RC-D52), so the lowering is exact.
//
// It is the ONE null lowering, shared by `yolo config migrate` (configmigrate.lower), the
// inherited user scope a launch delivers as TOML (RenderFilteredForDelivery) and, for the
// spellings a merge reads exactly as it read the null (lowerLegacyScalarNulls), every read of a
// legacy file. It returns the dotted paths of the nulls it could not respell, where the readable
// schema has no removal spelling; values keeps those nulls.
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
				case RemovalNativeString:
					m.Set(key, NativeProvider)
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

// lowerLegacyScalarNulls is LowerLegacyNulls for the read of one legacy file, limited to the
// paths whose null a merge reads exactly as it reads the spelling that replaces it: a scalar
// ("off", "native") replaces a lower layer's value as the null did, and a higher layer's value
// replaces it as it replaced the null. An empty list is left to the delivery and migrate: a
// legacy file's list union-merges, so at read it would keep what the null dropped. The paths
// are the ones whose null no legacy reader honored after a merge (validateLSPServers,
// validateMiseTools and the active_provider validator refused it), so no config that read
// before reads differently (RC-D52).
func lowerLegacyScalarNulls(values *jsonx.OrderedMap) {
	for _, at := range [][]string{{"lsp_servers"}, {"install", "mise"}, {ActiveProviderKey}} {
		table, ok := asMap(getPath(values, at))
		if !ok {
			continue
		}
		for _, key := range table.Keys() {
			if v, _ := table.Get(key); v != nil {
				continue
			}
			switch readableMergeRemovalPolicy(append(append([]string(nil), at...), key)) {
			case RemovalOffString:
				table.Set(key, "off")
			case RemovalNativeString:
				table.Set(key, NativeProvider)
			}
		}
	}
}

func getPath(values *jsonx.OrderedMap, path []string) any {
	var current any = values
	for _, key := range path {
		m, ok := asMap(current)
		if !ok {
			return nil
		}
		current, _ = m.Get(key)
	}
	return current
}
