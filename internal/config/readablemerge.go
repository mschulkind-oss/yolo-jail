package config

import (
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// projectLegacyB2Config lowers the B2-owned, losslessly representable legacy keys on the
// format-aware reader path. Other legacy roots remain byte-for-byte semantic compatibility
// values until their E/F/G owners provide projections; in particular this does not infer pack,
// provider, or authority meaning from their old representations.
func projectLegacyB2Config(values *jsonx.OrderedMap, source *srcNode) (*jsonx.OrderedMap, *srcNode) {
	for _, projection := range []struct {
		old  string
		path []string
	}{
		{old: "packages", path: []string{"install", "nix"}},
		{old: "mise_tools", path: []string{"install", "mise"}},
		{old: "gpu", path: []string{"machine", "gpu"}},
		{old: "resources", path: []string{"machine", "resources"}},
	} {
		value, present := values.Get(projection.old)
		if !present {
			continue
		}
		values.Delete(projection.old)
		var valueNode *srcNode
		if source != nil {
			valueNode = source.key(projection.old)
		}
		var rootNode *srcNode
		if source != nil {
			rootNode = &srcNode{writers: source.writers, keys: cloneSourceKeys(source.keys)}
			delete(rootNode.keys, projection.old)
		}
		putProjectedReadablePath(values, rootNode, projection.path, value, valueNode)
		if source != nil {
			source = rootNode
		}
	}
	values, source = projectLegacyReadableNetwork(values, source)
	values, source = lowerLegacyEnvSources(values, source)
	// A null no merged read honored is respelled to the scalar that reads the same in every merge
	// (RC-D52), so a TOML layer over or under it, and the TOML delivery of it, agree.
	lowerLegacyScalarNulls(values)
	return values, source
}

func projectLegacyReadableNetwork(values *jsonx.OrderedMap, source *srcNode) (*jsonx.OrderedMap, *srcNode) {
	values, source = projectLegacyNetworkList(values, source, "ports", []string{"machine", "ports"}, LowerLegacyPublishPorts)
	return projectLegacyNetworkList(values, source, "forward_host_ports", []string{"machine", "forward"}, parseLegacyForwardPorts)
}

func projectLegacyNetworkList(values *jsonx.OrderedMap, source *srcNode, oldKey string, target []string, convert func([]any) ([]any, bool)) (*jsonx.OrderedMap, *srcNode) {
	networkValue, ok := values.Get("network")
	if !ok {
		return values, source
	}
	network, ok := asMap(networkValue)
	if !ok {
		return values, source
	}
	entriesValue, present := network.Get(oldKey)
	entries, ok := asList(entriesValue)
	if !present || !ok {
		return values, source
	}
	converted, ok := convert(entries)
	if !ok {
		return values, source
	}
	oldNetworkNode := source.key("network")
	entryNode := oldNetworkNode.key(oldKey)
	network.Delete(oldKey)
	var rootNode *srcNode
	if source != nil {
		rootNode = cloneSourceNode(source)
		rootNetworkNode := cloneSourceNode(oldNetworkNode)
		if rootNetworkNode != nil {
			delete(rootNetworkNode.keys, oldKey)
			if len(rootNetworkNode.keys) == 0 {
				delete(rootNode.keys, "network")
			} else {
				rootNode.keys["network"] = rootNetworkNode
			}
		}
	}
	if network.Len() == 0 {
		values.Delete("network")
	} else {
		values.Set("network", network)
	}
	projectedElements := make([]*srcNode, len(converted))
	for i := range converted {
		if oldElement := entryNode.elem(i); oldElement != nil {
			projectedElements[i] = &srcNode{writers: oldElement.writers, keys: map[string]*srcNode{
				"host": oldElement,
				"jail": oldElement,
			}}
			if table, ok := converted[i].(*jsonx.OrderedMap); ok {
				for _, key := range []string{"address", "protocol"} {
					if _, present := table.Get(key); present {
						projectedElements[i].keys[key] = oldElement
					}
				}
			}
		}
	}
	var projectedNode *srcNode
	if entryNode != nil {
		projectedNode = &srcNode{writers: entryNode.writers, elems: projectedElements}
	}
	putProjectedReadablePath(values, rootNode, target, converted, projectedNode)
	return values, rootNode
}

// LowerLegacyPublishPorts is the one lowering of a legacy `network.ports` list to
// `machine.ports` tables, shared by every read of a legacy file and `yolo config migrate`. Every
// form the legacy validator accepts converts: `host:jail`, `address:host:jail` (the address goes
// to `address`) and either with a trailing `/tcp` or `/udp` (to `protocol`, written only when the
// legacy entry wrote one), so readablePublishPorts renders the same `-p` text the legacy entry was
// (RC-D53). It answers false for an entry the legacy validator refuses.
func LowerLegacyPublishPorts(entries []any) ([]any, bool) {
	converted := make([]any, 0, len(entries))
	for _, entry := range entries {
		text, ok := entry.(string)
		if !ok {
			return nil, false
		}
		protocol := ""
		if i := strings.LastIndex(text, "/"); i >= 0 {
			text, protocol = text[:i], text[i+1:]
			if protocol != "tcp" && protocol != "udp" {
				return nil, false
			}
		}
		parts := strings.Split(text, ":")
		address, hasAddress := "", false
		switch len(parts) {
		case 2:
		case 3:
			address, hasAddress = parts[0], true
			parts = parts[1:]
		default:
			return nil, false
		}
		if strings.Contains(address, "/") {
			return nil, false
		}
		host, hostOK := parseLegacyPortNumber(parts[0])
		jail, jailOK := parseLegacyPortNumber(parts[1])
		if !hostOK || !jailOK {
			return nil, false
		}
		port := jsonx.NewOrderedMap()
		port.Set("host", legacyReadablePortNumber(host))
		port.Set("jail", legacyReadablePortNumber(jail))
		if hasAddress {
			port.Set("address", address)
		}
		if protocol != "" {
			port.Set("protocol", protocol)
		}
		converted = append(converted, port)
	}
	return converted, true
}

// PublishPortSpec is the `-p` text one `machine.ports` table renders, the legacy entry it was
// lowered from: `[address:]host:jail[/protocol]`. It answers false for a table without two port
// numbers.
func PublishPortSpec(port *jsonx.OrderedMap) (string, bool) {
	if port == nil {
		return "", false
	}
	hostValue, _ := port.Get("host")
	jailValue, _ := port.Get("jail")
	host, hostOK := jsonx.AsInt(hostValue)
	jail, jailOK := jsonx.AsInt(jailValue)
	if !hostOK || !jailOK {
		return "", false
	}
	spec := strconv.FormatInt(host, 10) + ":" + strconv.FormatInt(jail, 10)
	if address, ok := port.Get("address"); ok {
		if text, isText := address.(string); isText {
			spec = text + ":" + spec
		}
	}
	if protocol, ok := port.Get("protocol"); ok {
		if text, isText := protocol.(string); isText {
			spec += "/" + text
		}
	}
	return spec, true
}

func parseLegacyForwardPorts(entries []any) ([]any, bool) {
	converted := make([]any, 0, len(entries))
	for _, entry := range entries {
		var local, host int
		switch value := entry.(type) {
		case string:
			parsed, err := ParsePortForwards([]any{value}, nil)
			if err != nil || len(parsed) != 1 {
				return nil, false
			}
			local, host = parsed[0].LocalPort, parsed[0].HostPort
		default:
			if number, ok := jsonx.AsInt(entry); ok {
				local, host = int(number), int(number)
			} else if number, ok := entry.(int); ok {
				local, host = number, number
			} else {
				return nil, false
			}
		}
		port := jsonx.NewOrderedMap()
		if local < 1 || local > 65535 || host < 1 || host > 65535 {
			return nil, false
		}
		port.Set("jail", legacyReadablePortNumber(local))
		port.Set("host", legacyReadablePortNumber(host))
		converted = append(converted, port)
	}
	return converted, true
}

func legacyReadablePortNumber(port int) any {
	value, _ := jsonx.IntLiteral(strconv.Itoa(port))
	return value
}

func parseLegacyPortNumber(text string) (int, bool) {
	port, err := strconv.Atoi(text)
	return port, err == nil && port >= 1 && port <= 65535
}

func putProjectedReadablePath(values *jsonx.OrderedMap, source *srcNode, path []string, value any, valueNode *srcNode) {
	if len(path) == 1 {
		if old, present := values.Get(path[0]); present {
			oldNode := source.key(path[0])
			if oldMap, oldOK := asMap(old); oldOK {
				if newMap, newOK := asMap(value); newOK {
					value, valueNode = mergeReadableConfigMode(oldMap, newMap, oldNode, valueNode, []string{path[0]}, false)
				}
			} else if oldList, oldOK := asList(old); oldOK {
				if newList, newOK := asList(value); newOK {
					value, valueNode = mergeLists(oldList, newList, oldNode, valueNode)
				}
			}
		}
		values.Set(path[0], value)
		if source != nil {
			keys := cloneSourceKeys(source.keys)
			keys[path[0]] = valueNode
			source.keys = keys
		}
		return
	}
	parent, present := values.Get(path[0])
	parentMap, ok := asMap(parent)
	if !present || !ok {
		parentMap = jsonx.NewOrderedMap()
	}
	var parentNode *srcNode
	if source != nil {
		parentNode = cloneSourceNode(source.key(path[0]))
	}
	if parentNode == nil && source != nil {
		parentNode = &srcNode{keys: map[string]*srcNode{}}
	}
	putProjectedReadablePath(parentMap, parentNode, path[1:], value, valueNode)
	values.Set(path[0], parentMap)
	if source != nil {
		keys := cloneSourceKeys(source.keys)
		keys[path[0]] = parentNode
		source.keys = keys
	}
}

func cloneSourceNode(source *srcNode) *srcNode {
	if source == nil {
		return nil
	}
	return &srcNode{writers: source.writers, keys: cloneSourceKeys(source.keys), elems: source.elems, clearList: source.clearList, clearTable: source.clearTable}
}

func readableResetTree(value any, path []string) *srcNode {
	node := &srcNode{}
	switch current := value.(type) {
	case *jsonx.OrderedMap:
		for _, key := range current.Keys() {
			child, _ := current.Get(key)
			if childNode := readableResetTree(child, appendReadablePath(path, key)); childNode != nil {
				node.keys = ensureSourceKeys(node.keys)
				node.keys[key] = childNode
			}
		}
	case []any:
		if len(current) == 0 && readableMergeRemovalPolicy(path) == RemovalEmptyList {
			node.clearList = true
		} else {
			for i, child := range current {
				if childNode := readableResetTree(child, path); childNode != nil {
					node.elems = ensureSourceElems(node.elems, i)
					node.elems[i] = childNode
				}
			}
		}
	case string:
		node.clearTable = current == "off" && readableMergeRemovalPolicy(path) == RemovalOffString
	}
	if !node.clearList && !node.clearTable && len(node.keys) == 0 && len(node.elems) == 0 {
		return nil
	}
	return node
}

func markReadableRemovalValues(node *srcNode, value any, path []string) {
	if node == nil {
		return
	}
	switch current := value.(type) {
	case *jsonx.OrderedMap:
		for _, key := range current.Keys() {
			child, _ := current.Get(key)
			markReadableRemovalValues(node.key(key), child, appendReadablePath(path, key))
		}
	case []any:
		if len(current) == 0 && readableMergeRemovalPolicy(path) == RemovalEmptyList {
			node.clearList = true
			return
		}
		for i, child := range current {
			markReadableRemovalValues(node.elem(i), child, path)
		}
	case string:
		if current == "off" && readableMergeRemovalPolicy(path) == RemovalOffString {
			node.clearTable = true
		}
	}
}

func ensureSourceKeys(keys map[string]*srcNode) map[string]*srcNode {
	if keys == nil {
		return map[string]*srcNode{}
	}
	return keys
}

func ensureSourceElems(elems []*srcNode, index int) []*srcNode {
	for len(elems) <= index {
		elems = append(elems, nil)
	}
	return elems
}

func cloneSourceKeys(source map[string]*srcNode) map[string]*srcNode {
	if len(source) == 0 {
		return map[string]*srcNode{}
	}
	out := make(map[string]*srcNode, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func mergeReadableConfig(base, override *jsonx.OrderedMap, baseSource, overrideSource *srcNode, path []string) (*jsonx.OrderedMap, *srcNode) {
	return mergeReadableConfigMode(base, override, baseSource, overrideSource, path, true)
}

func mergeReadableConfigMode(base, override *jsonx.OrderedMap, baseSource, overrideSource *srcNode, path []string, clearEmptyLists bool) (*jsonx.OrderedMap, *srcNode) {
	if len(path) != 0 {
		return mergeReadableConfigWalk(base, override, baseSource, overrideSource, path, clearEmptyLists)
	}
	// Two configs' env lists meet in layer order under one key (alignEnvForMerge, RC-D50).
	base, override, baseSource, overrideSource = alignEnvForMerge(base, override, baseSource, overrideSource)
	merged, node := mergeReadableConfigWalk(base, override, baseSource, overrideSource, path, clearEmptyLists)
	return canonicalEnvReset(merged, node)
}

func mergeReadableConfigWalk(base, override *jsonx.OrderedMap, baseSource, overrideSource *srcNode, path []string, clearEmptyLists bool) (*jsonx.OrderedMap, *srcNode) {
	track := baseSource != nil || overrideSource != nil
	result := jsonx.NewOrderedMap()
	var node *srcNode
	if track {
		node = &srcNode{writers: concatWriters(baseSource.writerList(), overrideSource.writerList()),
			keys: make(map[string]*srcNode, base.Len()+override.Len())}
	}
	for _, key := range base.Keys() {
		value, _ := base.Get(key)
		result.Set(key, value)
		if track {
			node.keys[key] = baseSource.key(key)
		}
	}
	for _, key := range override.Keys() {
		value, _ := override.Get(key)
		existing, present := result.Get(key)
		if present {
			if baseMap, ok := asMap(existing); ok {
				if overrideMap, ok := asMap(value); ok {
					baseMapNode := node.key(key)
					overrideMapNode := overrideSource.key(key)
					if overrideMapNode != nil && overrideMapNode.clearTable {
						result.Set(key, overrideMap)
						if track {
							mergedSource := replacedBy(baseMapNode, overrideMapNode)
							if mergedSource == nil {
								mergedSource = &srcNode{}
							}
							mergedSource.clearTable = true
							mergedSource.keys = overrideMapNode.keys
							node.keys[key] = mergedSource
						}
						continue
					}
					merged, mergedSource := mergeReadableConfigMode(baseMap, overrideMap, baseMapNode, overrideMapNode, appendReadablePath(path, key), clearEmptyLists)
					if len(path) == 0 && key == "mcp_servers" {
						secureMCPServerMerge(baseMap, overrideMap, merged)
					}
					result.Set(key, merged)
					if track {
						node.keys[key] = mergedSource
					}
					continue
				}
			}
			if baseList, ok := asList(existing); ok {
				if overrideList, ok := asList(value); ok {
					baseListNode := node.key(key)
					overrideListNode := overrideSource.key(key)
					clearList := overrideListNode != nil && overrideListNode.clearList
					if clearEmptyLists && len(overrideList) == 0 && readableMergeRemovalPolicy(appendReadablePath(path, key)) == RemovalEmptyList {
						clearList = true
					}
					if clearList {
						result.Set(key, overrideList)
						if track {
							mergedSource := replacedBy(baseListNode, overrideListNode)
							if mergedSource == nil {
								mergedSource = &srcNode{}
							}
							mergedSource.clearList = true
							node.keys[key] = mergedSource
						}
						continue
					}
					merged, mergedSource := mergeLists(baseList, overrideList, baseListNode, overrideListNode)
					if mergedSource != nil && baseListNode != nil && baseListNode.clearList {
						mergedSource = cloneSourceNode(mergedSource)
						mergedSource.clearList = true
					}
					result.Set(key, merged)
					if track {
						node.keys[key] = mergedSource
					}
					continue
				}
			}
		}
		result.Set(key, value)
		if track {
			baseValueNode := node.key(key)
			overrideValueNode := overrideSource.key(key)
			var mergedSource *srcNode
			if present {
				mergedSource = replacedBy(baseValueNode, overrideValueNode)
			} else {
				mergedSource = overrideValueNode
			}
			if (baseValueNode != nil && baseValueNode.clearTable) || (overrideValueNode != nil && overrideValueNode.clearTable) {
				if mergedSource == nil {
					mergedSource = &srcNode{}
				}
				mergedSource = cloneSourceNode(mergedSource)
				mergedSource.clearTable = true
			}
			node.keys[key] = mergedSource
		}
	}
	return result, node
}

func readableMergeRemovalPolicy(path []string) RemovalPolicy {
	if rule, ok := readableSchemaRule(path); ok {
		return rule.Removal
	}
	return RemovalUnspecified
}
