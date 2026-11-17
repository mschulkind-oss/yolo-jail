package config

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// lowerReadableCallRoots moves the format spelling only after workspace layers have merged,
// so readable list resets/unions keep their existing semantics and dual spellings cannot merge.
// Writers move with the value; the gate and validators still consume brokered.<source>.roots.
func lowerReadableCallRoots(values *jsonx.OrderedMap, source *srcNode) (*jsonx.OrderedMap, *srcNode, error) {
	raw, present := values.Get("call_roots")
	if !present {
		return values, source, nil
	}
	roots, ok := asMap(raw)
	if !ok {
		return nil, nil, configErr("config.call_roots: expected an object; use [call_roots] with a list per source")
	}
	brokered, _ := values.Get(brokeredKey)
	brokers, _ := asMap(brokered)
	for _, name := range roots.Keys() {
		if brokers != nil {
			v, _ := brokers.Get(name)
			entry, _ := asMap(v)
			if entry != nil {
				if _, has := entry.Get(brokeredRootsKey); has {
					return nil, nil, configErr("config.call_roots.%s: both call_roots.%s (%s) and brokered.%s.roots (%s) were written; keep one format spelling for this source, then run yolo config check", name, name,
						strings.Join(source.key("call_roots").key(name).locations(), ", "), name,
						strings.Join(source.key(brokeredKey).key(name).key(brokeredRootsKey).locations(), ", "))
				}
			}
		}
	}
	node := source.key("call_roots")
	source = cloneSourceNode(source)
	for _, name := range roots.Keys() {
		value, _ := roots.Get(name)
		putProjectedReadablePath(values, source, []string{brokeredKey, name, brokeredRootsKey}, value, node.key(name))
	}
	values.Delete("call_roots")
	source = source.without("call_roots")
	return values, source, nil
}
