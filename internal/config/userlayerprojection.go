package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// projectLegacyLayerOverReadable respells a legacy JSONC --user-layer in the TOML spelling of the
// user config it applies over, for the keys where the two spellings would otherwise sit side by
// side in the merged config and answer one question twice:
//
//   - `env_sources` (every entry a file path) becomes `env.files`, ahead of any `env.files` the
//     layer writes itself (the order envEntries walks them). Merged, the base's files come
//     first and the layer's after, as a layer's entries always do. Left as `env_sources`, the
//     layer's files loaded BEFORE the base's (envEntries walks `env_sources` first), so the
//     base won every variable both files set. A list holding an inline table is left alone:
//     inline entries have no lossless `env.set` spelling once they sit between files.
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
	layer, layerNode = projectLayerEnvSources(layer, layerNode)
	layer, layerNode = projectLayerLoopholeSettings(layer, layerNode, base)
	return layer, layerNode
}

func projectLayerEnvSources(layer *jsonx.OrderedMap, node *srcNode) (*jsonx.OrderedMap, *srcNode) {
	value, present := layer.Get("env_sources")
	entries, ok := asList(value)
	if !present || !ok {
		return layer, node
	}
	for _, entry := range entries {
		if _, isPath := entry.(string); !isPath {
			return layer, node
		}
	}
	env, hasEnv := asMap(getOr(layer, "env", nil))
	if getOr(layer, "env", nil) != nil && !hasEnv {
		return layer, node // a malformed `env` is the validator's to name
	}
	if env == nil {
		env = jsonx.NewOrderedMap()
	}
	own, _ := asList(getOr(env, "files", nil))
	files := append(append([]any(nil), entries...), own...)
	sourcesNode := node.key("env_sources")
	envNode := cloneSourceNode(node.key("env"))
	filesNode := envNode.key("files")
	var projected *srcNode
	if sourcesNode != nil || filesNode != nil {
		projected = &srcNode{writers: concatWriters(sourcesNode.writerList(), filesNode.writerList())}
		for i := range entries {
			projected.elems = append(projected.elems, sourcesNode.elem(i))
		}
		for i := range own {
			projected.elems = append(projected.elems, filesNode.elem(i))
		}
	}
	env.Set("files", files)
	layer.Delete("env_sources")
	layer.Set("env", env)
	if node != nil {
		node.keys = cloneSourceKeys(node.keys)
		delete(node.keys, "env_sources")
		if envNode == nil {
			envNode = &srcNode{writers: sourcesNode.writerList()}
		}
		envNode.keys = cloneSourceKeys(envNode.keys)
		envNode.keys["files"] = projected
		node.keys["env"] = envNode
	}
	return layer, node
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
