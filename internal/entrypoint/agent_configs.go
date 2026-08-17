package entrypoint

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// dumpJSONIndent2 renders v as indent-2 JSON + "\n" — the form every
// agent-config writer uses (insertion-order preserving, ASCII-only).
func dumpJSONIndent2(v any) string {
	s, _ := jsonx.DumpsIndent(v, 2)
	return s + "\n"
}

// loadObject — the "read a JSON object, defaulting to {}" reader — lived here until the
// credential harvest was deleted (pack-code-separation.md §5) took its last caller. The
// comments elsewhere in this package that say "it used to read via loadObject" are describing
// a JSON-unconditional read that surfacecodec.go replaced with codec dispatch, which is why
// nothing wants it back: silently yielding {} for anything unparseable is the behavior those
// comments exist to warn about.

// object: returns the existing value if it is an OrderedMap, otherwise sets and
// returns a new empty OrderedMap. (A non-object at the key never occurs in
// practice.)
func setDefaultMap(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if v, ok := m.Get(key); ok {
		if om, isMap := v.(*jsonx.OrderedMap); isMap {
			return om
		}
	}
	sub := jsonx.NewOrderedMap()
	m.Set(key, sub)
	return sub
}

// sets (and returns) default when key is absent.
func setDefault(m *jsonx.OrderedMap, key string, def any) any {
	if v, ok := m.Get(key); ok {
		return v
	}
	m.Set(key, def)
	return def
}
