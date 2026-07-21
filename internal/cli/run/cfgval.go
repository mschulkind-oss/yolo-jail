package run

import "github.com/mschulkind-oss/yolo-jail/internal/jsonx"

// cfgGet returns config[key] (or nil, false).
func cfgGet(cfg *jsonx.OrderedMap, key string) (any, bool) {
	if cfg == nil {
		return nil, false
	}
	return cfg.Get(key)
}

// cfgMap returns config[key] as an *OrderedMap, or nil.
func cfgMap(cfg *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	v, _ := cfgGet(cfg, key)
	if m, ok := v.(*jsonx.OrderedMap); ok {
		return m
	}
	return nil
}

// cfgList returns config[key] as a []any, or nil.
func cfgList(cfg *jsonx.OrderedMap, key string) []any {
	v, _ := cfgGet(cfg, key)
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

// cfgStr returns config[key] as a string, or "".
func cfgStr(cfg *jsonx.OrderedMap, key string) string {
	v, _ := cfgGet(cfg, key)
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// cfgStrList returns config[key] as a []string, coercing string elements
// (non-strings are rare/never for the keys we read).
func cfgStrList(cfg *jsonx.OrderedMap, key string) []string {
	var out []string
	for _, e := range cfgList(cfg, key) {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// cfgTrue reports whether config[key] is exactly the JSON boolean true.
func cfgTrue(cfg *jsonx.OrderedMap, key string) bool {
	v, _ := cfgGet(cfg, key)
	b, ok := v.(bool)
	return ok && b
}

// mapGet returns m[key] (or nil).
func mapGet(m *jsonx.OrderedMap, key string) any {
	if m == nil {
		return nil
	}
	v, _ := m.Get(key)
	return v
}

// mapStr returns m[key] as a string, or "".
func mapStr(m *jsonx.OrderedMap, key string) string {
	if s, ok := mapGet(m, key).(string); ok {
		return s
	}
	return ""
}

// mapStrOr returns m[key] as a string, or def when absent/non-string.
func mapStrOr(m *jsonx.OrderedMap, key, def string) string {
	if v := mapGet(m, key); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

// mapTrue reports whether m[key] is exactly true.
func mapTrue(m *jsonx.OrderedMap, key string) bool {
	b, ok := mapGet(m, key).(bool)
	return ok && b
}

// mapBoolOr returns m[key] as a bool, or def.
func mapBoolOr(m *jsonx.OrderedMap, key string, def bool) bool {
	if b, ok := mapGet(m, key).(bool); ok {
		return b
	}
	return def
}
