package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// formerspellings.go reads a grant kind and its readable capability under the names they had
// before a rename, as the names that replaced them, at the one place every config file is
// loaded, so no reader below meets the old spelling. The one such rename is the `runs` grant
// (docs/design/github-write-scopes.md GW-D78): the kind `dispatch-workflow` became `runs`, and
// its capability `dispatch_workflows` became `runs`, in `allow.github.always` and `on_request`,
// as the key `requests.github.dispatch_workflows`, and in a legacy file's `brokered.<source>.
// grant_kinds` and grant entries. A file is never rewritten for it: the old spelling keeps
// working, and a verb that writes (`yolo request`, `yolo allow`) writes the new one.

// formerCapabilities is each broker source's former capability spellings, by the spelling that
// replaced them.
var formerCapabilities = map[string]map[string]string{
	"github": {"dispatch_workflows": "runs"},
}

// CanonicalCapability is capability of source as this yolo names it: a former spelling reads
// as the one that replaced it, and every other name is returned unchanged.
func CanonicalCapability(source, capability string) string {
	if now, ok := formerCapabilities[source][capability]; ok {
		return now
	}
	return capability
}

// respellFormerReadable reads a TOML file's former capability spellings as their current
// ones, in values and in node, its provenance tree (nil when not recorded): each entry of
// `allow.<source>.always` and `on_request` in place (one that then repeats an entry before it
// is dropped), and each key `requests.<source>.<former>` as `requests.<source>.<current>`,
// merged into that key's list when the file writes both. The node of a respelled key keeps its
// document location, so a refusal still points at the line the file wrote.
func respellFormerReadable(values *jsonx.OrderedMap, node *srcNode) {
	if allow, ok := asMap(getOr(values, "allow", nil)); ok {
		for source := range formerCapabilities {
			table, ok := asMap(getOr(allow, source, nil))
			if !ok {
				continue
			}
			for _, field := range []string{"always", "on_request"} {
				list, ok := getOr(table, field, nil).([]any)
				if !ok {
					continue
				}
				listNode := node.key("allow").key(source).key(field)
				out := make([]any, 0, len(list))
				var outNodes []*srcNode
				seen := map[string]bool{}
				for i, e := range list {
					if s, ok := e.(string); ok {
						s = CanonicalCapability(source, s)
						if seen[s] {
							continue
						}
						seen[s] = true
						e = s
					}
					out = append(out, e)
					if listNode != nil && i < len(listNode.elems) {
						outNodes = append(outNodes, listNode.elems[i])
					}
				}
				table.Set(field, out)
				if listNode != nil && len(outNodes) == len(out) {
					listNode.elems = outNodes
				}
			}
		}
	}
	requests, ok := asMap(getOr(values, "requests", nil))
	if !ok {
		return
	}
	for source, former := range formerCapabilities {
		table, ok := asMap(getOr(requests, source, nil))
		if !ok {
			continue
		}
		tableNode := node.key("requests").key(source)
		for old, now := range former {
			v, has := table.Get(old)
			if !has {
				continue
			}
			oldNode := tableNode.key(old)
			if existing, both := table.Get(now); both {
				merged, okNow := existing.([]any)
				add, okOld := v.([]any)
				if !okNow || !okOld {
					continue // two shapes the validator refuses on its own; leave both for it
				}
				for _, e := range add {
					if s, isString := e.(string); !isString || !containsAny(merged, s) {
						merged = append(merged, e)
					}
				}
				table.Set(now, merged)
			} else {
				renameKey(table, old, now, v)
				if tableNode != nil && oldNode != nil {
					tableNode.keys = cloneSourceKeys(tableNode.keys)
					tableNode.keys[now] = oldNode
				}
			}
			table.Delete(old)
			if tableNode != nil {
				tableNode.keys = cloneSourceKeys(tableNode.keys)
				delete(tableNode.keys, old)
			}
		}
	}
}

// respellFormerLegacy reads a legacy JSONC file's former grant kinds as their current ones, in
// place: each entry of `brokered.<source>.grant_kinds`, and each grant entry's `kind`.
func respellFormerLegacy(values *jsonx.OrderedMap) {
	sources, ok := asMap(getOr(values, brokeredKey, nil))
	if !ok {
		return
	}
	for _, source := range sources.Keys() {
		src, ok := asMap(getOr(sources, source, nil))
		if !ok {
			continue
		}
		if kinds, ok := getOr(src, brokeredGrantKindsKey, nil).([]any); ok {
			for i, e := range kinds {
				if s, ok := e.(string); ok {
					kinds[i] = brokerscope.CanonicalGrantKind(s)
				}
			}
		}
		if grants, ok := getOr(src, brokeredGrantsKey, nil).([]any); ok {
			for _, e := range grants {
				if m, ok := asMap(e); ok {
					if s, ok := getOr(m, brokeredGrantKindKey, nil).(string); ok {
						m.Set(brokeredGrantKindKey, brokerscope.CanonicalGrantKind(s))
					}
				}
			}
		}
	}
}

// renameKey writes value under now at old's place in m's key order; old is then deleted by the
// caller.
func renameKey(m *jsonx.OrderedMap, old, now string, value any) {
	keys := append([]string(nil), m.Keys()...)
	vals := make([]any, len(keys))
	for i, k := range keys {
		vals[i], _ = m.Get(k)
	}
	for _, k := range keys {
		m.Delete(k)
	}
	for i, k := range keys {
		if k == old {
			m.Set(now, value)
		}
		m.Set(k, vals[i])
	}
}
