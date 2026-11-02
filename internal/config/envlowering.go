package config

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// LowerEnvSources is THE lowering of a legacy `env_sources` list into its readable spelling,
// `env.sources` (RC-D50): the same list entry for entry, a path staying a path and an inline
// table `{"A": "1", "B": null}` becoming `{ set = { A = "1" }, unset = ["B"] }`, since TOML has no
// null. Within one layer env.sources and env.files are one ordered list (sources, then files), and
// the layers are walked in merge order (normalizeLayerEnv, alignEnvForMerge), so the lowering is
// exact whatever the other layers spell.
//
// Every caller shares it, so none can disagree about what a list means: `yolo config migrate`
// (configmigrate.lowerEnvSources), and every read of a legacy file (projectLegacyB2Config), which
// is how a TOML file and a legacy JSONC file merge in layer order, a launch delivers the
// inherited user scope as TOML, and a legacy --user-layer applies over a TOML user config.
//
// The error names an entry no reader accepts: one neither a path nor a table, or an inline value
// neither a string nor null. The validator refuses such a config, so a reader leaves it at its
// old spelling for the validator to name.
func LowerEnvSources(list []any) ([]any, error) {
	lowered := make([]any, 0, len(list))
	for i, entry := range list {
		switch entry := entry.(type) {
		case string:
			lowered = append(lowered, entry)
		case *jsonx.OrderedMap:
			table, err := envSourcesTable(entry, i)
			if err != nil {
				return nil, err
			}
			lowered = append(lowered, table)
		default:
			return nil, fmt.Errorf("env_sources[%d] is neither a dotenv path nor an inline table", i)
		}
	}
	return lowered, nil
}

func envSourcesTable(vars *jsonx.OrderedMap, i int) (*jsonx.OrderedMap, error) {
	set := jsonx.NewOrderedMap()
	var unset []any
	for _, name := range vars.Keys() {
		v, _ := vars.Get(name)
		switch v := v.(type) {
		case string:
			set.Set(name, v)
		case nil:
			unset = append(unset, name)
		default:
			return nil, fmt.Errorf("env_sources[%d].%s: an inline variable's value must be a string or null", i, name)
		}
	}
	table := jsonx.NewOrderedMap()
	if set.Len() > 0 {
		table.Set("set", set)
	}
	if len(unset) > 0 {
		table.Set("unset", unset)
	}
	return table, nil
}

// envSourcesInline is one env.sources table as the env resolver reads an inline entry: its set
// names assigned, its unset names removed (nil). A legacy table names each variable once, so
// applying the two parts in either order is the same. ok is false for a malformed table, which
// the validator refuses.
func envSourcesInline(table *jsonx.OrderedMap) (*jsonx.OrderedMap, bool) {
	out := jsonx.NewOrderedMap()
	for _, key := range table.Keys() {
		if key != "set" && key != "unset" {
			return nil, false
		}
	}
	if raw, present := table.Get("set"); present {
		set, ok := asMap(raw)
		if !ok {
			return nil, false
		}
		for _, name := range set.Keys() {
			v, _ := set.Get(name)
			s, ok := asStr(v)
			if !ok {
				return nil, false
			}
			out.Set(name, s)
		}
	}
	if raw, present := table.Get("unset"); present {
		unset, ok := asList(raw)
		if !ok {
			return nil, false
		}
		for _, v := range unset {
			name, ok := asStr(v)
			if !ok {
				return nil, false
			}
			out.Set(name, nil)
		}
	}
	return out, true
}

// lowerLegacyEnvSources respells one legacy file's `env_sources` through LowerEnvSources and
// moves its record with it; normalizeLayerEnv then joins it with any `env` list the file writes.
// A malformed list or `env` is left alone, the validator's to name. values is edited in place;
// node is copied where it changes.
func lowerLegacyEnvSources(values *jsonx.OrderedMap, node *srcNode) (*jsonx.OrderedMap, *srcNode) {
	value, present := values.Get("env_sources")
	if !present {
		return values, node
	}
	if value == nil {
		// A legacy null replaces the list the layers beneath wrote, inline tables included. It
		// stays a null, on both readable lists, so either merge replaces what lies beneath with
		// it and envEntries walks nothing for it; env.set and env.unset, which env_sources never
		// held, are untouched. TOML spells the same reset `env.sources = []` (yolo config migrate).
		rawEnv := getOr(values, "env", nil)
		env, isMap := asMap(rawEnv)
		if rawEnv != nil && !isMap {
			return values, node
		}
		if env == nil {
			env = jsonx.NewOrderedMap()
		} else {
			env = jsonx.DeepCopyMap(env)
		}
		env.Set("sources", nil)
		env.Set("files", nil)
		values.Delete("env_sources")
		values.Set("env", env)
		if node != nil {
			sourcesNode := node.key("env_sources")
			node = cloneSourceNode(node)
			delete(node.keys, "env_sources")
			envNode := cloneSourceNode(node.key("env"))
			if envNode == nil {
				envNode = &srcNode{writers: sourcesNode.writerList()}
			}
			envNode.keys = cloneSourceKeys(envNode.keys)
			envNode.keys["sources"] = sourcesNode
			envNode.keys["files"] = sourcesNode
			if node.keys == nil {
				node.keys = map[string]*srcNode{}
			}
			node.keys["env"] = envNode
		}
		return values, node
	}
	entries, ok := asList(value)
	if !ok {
		return values, node
	}
	lowered, err := LowerEnvSources(entries)
	if err != nil {
		return values, node
	}
	rawEnv := getOr(values, "env", nil)
	if _, isMap := asMap(rawEnv); rawEnv != nil && !isMap {
		return values, node
	}
	sourcesNode := node.key("env_sources")
	var loweredNode *srcNode
	if sourcesNode != nil {
		loweredNode = &srcNode{writers: sourcesNode.writers}
		for i := range lowered {
			loweredNode.elems = append(loweredNode.elems, envSourcesElemNode(sourcesNode.elem(i)))
		}
	}
	values.Delete("env_sources")
	if node != nil {
		node = cloneSourceNode(node)
		delete(node.keys, "env_sources")
	}
	// The legacy list is slot (1) of its layer: ahead of anything the file writes under env.
	return combineEnvLists(values, node, "", lowered, loweredNode)
}

// normalizeLayerEnv makes one layer's env lists one ordered list under one key: its env.sources
// then its env.files, under env.files when every entry is a path and env.sources otherwise.
// Every file a read loads goes through it, so a merged config never holds both keys from
// different layers and envEntries walks each layer's entries where that layer sits.
func normalizeLayerEnv(values *jsonx.OrderedMap, node *srcNode) (*jsonx.OrderedMap, *srcNode) {
	return combineEnvLists(values, node, "", nil, nil)
}

// alignEnvForMerge prepares two configs for a merge: when either holds env.sources, both hold
// their lists there (each side's env.sources, then its env.files), so the merge concatenates them
// in layer order. Neither input is changed.
func alignEnvForMerge(base, override *jsonx.OrderedMap, baseNode, overrideNode *srcNode) (*jsonx.OrderedMap, *jsonx.OrderedMap, *srcNode, *srcNode) {
	if !envHas(base, "sources") && !envHas(override, "sources") {
		return base, override, baseNode, overrideNode
	}
	if envHas(base, "files") {
		base, baseNode = combineEnvLists(shallowCopyMap(base), baseNode, "sources", nil, nil)
	}
	if envHas(override, "files") {
		override, overrideNode = combineEnvLists(shallowCopyMap(override), overrideNode, "sources", nil, nil)
	}
	return base, override, baseNode, overrideNode
}

// canonicalEnvReset spells a merged env whose one list is an empty env.sources as an empty
// env.files. Both are the TOML reset of every list beneath (RemovalEmptyList); which key the
// merge left it under depends only on what the layers beneath spelled (alignEnvForMerge), so the
// merged config names it one way whatever they spelled. Neither input is changed.
func canonicalEnvReset(values *jsonx.OrderedMap, node *srcNode) (*jsonx.OrderedMap, *srcNode) {
	env, ok := asMap(getOr(values, "env", nil))
	if !ok || envHas(values, "files") {
		return values, node
	}
	raw, present := env.Get("sources")
	if list, isList := asList(raw); !present || !isList || len(list) != 0 {
		return values, node
	}
	values = shallowCopyMap(values)
	renamed := jsonx.NewOrderedMap()
	for _, k := range env.Keys() {
		v, _ := env.Get(k)
		if k == "sources" {
			k = "files"
		}
		renamed.Set(k, v)
	}
	values.Set("env", renamed)
	if envNode := node.key("env"); envNode != nil {
		envNode = cloneSourceNode(envNode)
		envNode.keys = cloneSourceKeys(envNode.keys)
		if listNode, has := envNode.keys["sources"]; has {
			delete(envNode.keys, "sources")
			envNode.keys["files"] = listNode
		}
		node = cloneSourceNode(node)
		node.keys = cloneSourceKeys(node.keys)
		node.keys["env"] = envNode
	}
	return values, node
}

func envHas(values *jsonx.OrderedMap, key string) bool {
	if values == nil {
		return false
	}
	env, ok := asMap(getOr(values, "env", nil))
	if !ok {
		return false
	}
	_, present := env.Get(key)
	return present
}

// combineEnvLists joins lead (a lowered legacy list, nil for none), env.sources and env.files, in
// that order, into one list under into ("sources" or "files"; "" picks files when every entry is a
// path). An empty list a file wrote (a TOML reset) stays an empty list. A malformed env or list is
// left alone. values is edited in place; node is copied.
func combineEnvLists(values *jsonx.OrderedMap, node *srcNode, into string, lead []any, leadNode *srcNode) (*jsonx.OrderedMap, *srcNode) {
	rawEnv := getOr(values, "env", nil)
	env, ok := asMap(rawEnv)
	if rawEnv != nil && !ok {
		return values, node
	}
	if env == nil {
		if lead == nil {
			return values, node
		}
		env = jsonx.NewOrderedMap()
	} else {
		env = jsonx.DeepCopyMap(env)
	}
	envNode := cloneSourceNode(node.key("env"))
	list := append([]any(nil), lead...)
	var listNode *srcNode
	if leadNode != nil || envNode != nil {
		listNode = &srcNode{writers: leadNode.writerList()}
		for i := range lead {
			listNode.elems = append(listNode.elems, leadNode.elem(i))
		}
	}
	present := lead != nil
	for _, key := range []string{"sources", "files"} {
		raw, has := env.Get(key)
		if !has {
			continue
		}
		entries, ok := asList(raw)
		if !ok {
			return values, node // the validator's to name
		}
		present = true
		list = append(list, entries...)
		if listNode != nil {
			keyNode := envNode.key(key)
			listNode.writers = concatWriters(listNode.writers, keyNode.writerList())
			for i := range entries {
				listNode.elems = append(listNode.elems, keyNode.elem(i))
			}
			if keyNode != nil && keyNode.clearList {
				listNode.clearList = true
			}
		}
	}
	if !present {
		return values, node
	}
	if into == "" {
		into = "files"
		for _, entry := range list {
			if _, isPath := entry.(string); !isPath {
				into = "sources"
				break
			}
		}
	}
	env.Delete("sources")
	env.Delete("files")
	env.Set(into, list)
	values.Set("env", env)
	if node == nil && envNode == nil && listNode == nil {
		return values, node
	}
	node = cloneSourceNode(node)
	if node == nil {
		node = &srcNode{}
	}
	if envNode == nil {
		envNode = &srcNode{writers: listNode.writerList()}
	}
	envNode.keys = cloneSourceKeys(envNode.keys)
	if envNode.keys == nil {
		envNode.keys = map[string]*srcNode{}
	}
	delete(envNode.keys, "sources")
	delete(envNode.keys, "files")
	envNode.keys[into] = listNode
	if node.keys == nil {
		node.keys = map[string]*srcNode{}
	} else {
		node.keys = cloneSourceKeys(node.keys)
	}
	node.keys["env"] = envNode
	return values, node
}

// envSourcesElemNode is the record of one lowered entry: a path's own, or an inline table's
// variables under `set`, so a message about `env.sources[i].set.NAME` names where NAME was written.
func envSourcesElemNode(elem *srcNode) *srcNode {
	if elem == nil || len(elem.keys) == 0 {
		return elem
	}
	return &srcNode{writers: elem.writers, keys: map[string]*srcNode{
		"set":   {writers: elem.writers, keys: elem.keys},
		"unset": {writers: elem.writers},
	}}
}

// respellLegacyRecord is an approval record's config part written before env_sources was read in
// its readable spelling, respelled as the read now spells it (lowerLegacyEnvSources), so a yolo
// that reads it differently does not ask again about a config nobody changed. Any other record,
// or one that does not decode, is returned as it is.
func respellLegacyRecord(text string) string {
	if !strings.Contains(text, `"env_sources"`) {
		return text
	}
	decoded, err := jsonx.Decode([]byte(text))
	if err != nil {
		return text
	}
	record, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return text
	}
	if _, present := record.Get("env_sources"); !present {
		return text
	}
	record, _ = lowerLegacyEnvSources(record, nil)
	respelled, err := SnapshotJSON(record)
	if err != nil {
		return text
	}
	return respelled
}
