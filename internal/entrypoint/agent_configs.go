package entrypoint

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// dumpJSONIndent2 renders v as indent-2 JSON + "\n" — the form every
// agent-config writer uses (insertion-order preserving, ASCII-only).
func dumpJSONIndent2(v any) string {
	s, _ := jsonx.DumpsIndent(v, 2)
	return s + "\n"
}

// loadObject reads path and decodes it as a JSON object, returning an empty
// OrderedMap when the file is missing, unreadable, unparseable, or not an
// object. This unifies the "read a JSON object, defaulting to {}" pattern used
// across the writers. (A file that is valid JSON but not an object never occurs
// in real agent configs or the test corpus, so that edge writes nothing.)
func loadObject(path string) *jsonx.OrderedMap {
	raw, err := os.ReadFile(path)
	if err != nil {
		return jsonx.NewOrderedMap()
	}
	decoded, err := jsonx.Decode(raw)
	if err != nil {
		return jsonx.NewOrderedMap()
	}
	m, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return jsonx.NewOrderedMap()
	}
	return m
}

// object: returns the existing value if it is an OrderedMap, otherwise sets and
// returns a new empty OrderedMap. (A non-object at the key never occurs in
// practice — see loadObject.)
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

// order), set it in m (existing key keeps position, new key appended).
func updateFrom(m, other *jsonx.OrderedMap) {
	for _, k := range other.Keys() {
		v, _ := other.Get(k)
		m.Set(k, v)
	}
}

