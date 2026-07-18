package loopholes

import "github.com/mschulkind-oss/yolo-jail/internal/jsonx"

// orderedFromPairs builds a *jsonx.OrderedMap from an even-length list of
// key(string), value(any) pairs, deep-converting nested map[string]any into
// *jsonx.OrderedMap and []any recursively so the value model matches what
// json5.Decode produces (which the loopholes code type-asserts on).
func orderedFromPairs(kv ...any) *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	for i := 0; i+1 < len(kv); i += 2 {
		key := kv[i].(string)
		m.Set(key, toValueModel(kv[i+1]))
	}
	return m
}

// toValueModel deep-converts a Go literal (map[string]any, []any, string, bool,
// int) into the decoded-JSON value model used across the package: maps become
// *jsonx.OrderedMap, ints become jsonx integer values.
func toValueModel(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := jsonx.NewOrderedMap()
		// map iteration order is nondeterministic; callers that need a
		// stable order for nested objects should use orderedMapValue.
		for k, sub := range t {
			m.Set(k, toValueModel(sub))
		}
		return m
	case *jsonx.OrderedMap:
		return t
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = toValueModel(e)
		}
		return out
	case int:
		return jsonx.IntValue(int64(t))
	default:
		return v
	}
}
