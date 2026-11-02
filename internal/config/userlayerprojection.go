package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// projectLegacyLayerOverReadable respells a legacy JSONC --user-layer in the TOML spelling of the
// user config it applies over, for the keys where the two spellings would otherwise sit side by
// side in the merged config and answer one question twice:
//
//   - `env_sources` needs nothing here: every read of a legacy file lowers it to env.sources or
//     env.files (lowerLegacyEnvSources), and the merge walks the base's entries before the
//     layer's, whatever each spelled (RC-D50).
//   - `loopholes.<name>.settings.<key>` becomes `allow.<name>.<key>` where the base already
//     writes an `allow.<name>` table, the mapping `yolo config migrate` writes and
//     loopholes.ConfigBlock reads back. Left as it was, the launch handed the daemon the
//     layer's value (ConfigBlock lets the legacy key win) while every reader of the merged
//     config — `yolo config why allow.<name>.<key>`, `yolo config dump` — reported the base's.
//     Where the base writes no such table the legacy key is the only answer and stays put: the
//     name may be a loophole whose pack has another name, which `allow.<name>` would refuse.
//
// An empty string is a value like any other here: a layer's `"profile": ""` replaces the base's
// profile, which is how a layer says "this login loophole serves nothing".
//
// It returns a copy; layer is unchanged. layerNode may be nil (an unrecorded read).
func projectLegacyLayerOverReadable(layer *jsonx.OrderedMap, layerNode *srcNode, base *jsonx.OrderedMap) (*jsonx.OrderedMap, *srcNode) {
	if layer == nil {
		return layer, layerNode
	}
	layer = jsonx.DeepCopyMap(layer)
	if layerNode != nil {
		layerNode = cloneSourceNode(layerNode)
	}
	layer, layerNode = projectLayerLoopholeSettings(layer, layerNode, base)
	return layer, layerNode
}

func projectLayerLoopholeSettings(layer *jsonx.OrderedMap, node *srcNode, base *jsonx.OrderedMap) (*jsonx.OrderedMap, *srcNode) {
	block, ok := asMap(getOr(layer, "loopholes", nil))
	if !ok {
		return layer, node
	}
	if base == nil {
		return layer, node
	}
	baseAllow, _ := asMap(getOr(base, "allow", nil))
	if baseAllow == nil {
		return layer, node
	}
	blockNode := cloneSourceNode(node.key("loopholes"))
	for _, name := range append([]string(nil), block.Keys()...) {
		if _, isTable := asMap(getOr(baseAllow, name, nil)); !isTable {
			continue
		}
		entry, ok := asMap(getOr(block, name, nil))
		if !ok {
			continue
		}
		settings, ok := asMap(getOr(entry, "settings", nil))
		if !ok {
			continue
		}
		entryNode := cloneSourceNode(blockNode.key(name))
		settingsNode := cloneSourceNode(entryNode.key("settings"))
		for _, key := range append([]string(nil), settings.Keys()...) {
			if key == "always" || key == "on_request" {
				continue // an allowance field's name, never a setting's spelling under allow
			}
			value, _ := settings.Get(key)
			putProjectedReadablePath(layer, node, []string{"allow", name, key}, value, settingsNode.key(key))
			settings.Delete(key)
			if settingsNode != nil {
				settingsNode.keys = cloneSourceKeys(settingsNode.keys)
				delete(settingsNode.keys, key)
			}
		}
		if settings.Len() == 0 {
			entry.Delete("settings")
			if entryNode != nil {
				entryNode.keys = cloneSourceKeys(entryNode.keys)
				delete(entryNode.keys, "settings")
			}
		} else if entryNode != nil {
			entryNode.keys = cloneSourceKeys(entryNode.keys)
			entryNode.keys["settings"] = settingsNode
		}
		if entry.Len() == 0 {
			block.Delete(name)
			if blockNode != nil {
				blockNode.keys = cloneSourceKeys(blockNode.keys)
				delete(blockNode.keys, name)
			}
		} else if blockNode != nil {
			blockNode.keys = cloneSourceKeys(blockNode.keys)
			blockNode.keys[name] = entryNode
		}
	}
	if node != nil {
		node.keys = cloneSourceKeys(node.keys)
		if block.Len() == 0 {
			delete(node.keys, "loopholes")
		} else {
			node.keys["loopholes"] = blockNode
		}
	}
	if block.Len() == 0 {
		layer.Delete("loopholes")
	}
	return layer, node
}
