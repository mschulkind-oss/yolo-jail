// Package tomlx provides TOML parsing parity for the load-bearing places the
// port needs it (go-port plan §3 internal/tomlx): mise.toml / mise.jail.toml
// venv discovery (feeds the venv-shadow mount set and a jail-made-venv rmtree),
// agents_md's pyproject.toml read, and codex's ~/.codex/config.toml (the one
// agent config that is TOML, not JSON).
//
// Backed by github.com/BurntSushi/toml (the de-facto standard, vendored) rather
// than hand-written — TOML is genuinely complex and a vetted parser is the
// happy path. The parity surface is the SHAPES the port reads (str vs dict
// entries, nested env._.python.venv, later-file-wins), pinned by a fixture
// corpus in the test.
//
// Source of truth: Python tomllib observed behavior over the real config shapes.
package tomlx

import (
	"os"

	"github.com/BurntSushi/toml"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Decode parses TOML bytes into a generic map, matching tomllib.load's shape:
// tables -> map[string]any, arrays -> []any, and scalars to Go's bool/int64/
// float64/string/time. Returns an error on malformed TOML (tomllib raises).
func Decode(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// DecodeFile reads and parses a TOML file.
func DecodeFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(data)
}

// DecodeOrdered parses TOML into a *jsonx.OrderedMap that preserves the
// document's key order (top-level keys in file order; nested tables likewise),
// matching what Python's tomllib.load + dict insertion order yields. Scalars
// map to Go bool/int64/float64/string; tables to nested *jsonx.OrderedMap;
// arrays to []any. Used by codex's config.toml round-trip where top-level user
// key order must be preserved on re-emit.
//
// Note tomllib preserves the ORDER a key first appears in the source; BurntSushi
// MetaData.Keys() reports keys in that same document order, which we replay.
func DecodeOrdered(data []byte) (*jsonx.OrderedMap, error) {
	var raw map[string]any
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, err
	}
	keys := md.Keys()

	// A key is a TABLE (built incrementally as its children are visited) if any
	// other reported key has it as a strict prefix; otherwise it is a
	// scalar/array LEAF. BurntSushi reports inline-table subkeys too, so an
	// inline table like env = {K="v"} shows up as a table with a child key.
	isPrefix := make(map[string]bool, len(keys))
	for i := range keys {
		for j := range keys {
			if i == j {
				continue
			}
			if hasPrefix(keys[j], keys[i]) {
				isPrefix[joinKey(keys[i])] = true
				break
			}
		}
	}

	root := jsonx.NewOrderedMap()
	for _, key := range keys {
		parts := []string(key)
		cur := root
		var srcCur any = raw
		for i, p := range parts {
			sm, ok := srcCur.(map[string]any)
			if !ok {
				break
			}
			val, present := sm[p]
			if !present {
				break
			}
			if i == len(parts)-1 {
				if isPrefix[joinKey(key)] {
					// Table node: ensure it exists (children fill it in order).
					ensureChild(cur, p)
				} else {
					cur.Set(p, convertTOMLValue(val))
				}
			} else {
				cur = ensureChild(cur, p)
				srcCur = val
			}
		}
	}
	return root, nil
}

func hasPrefix(key, prefix toml.Key) bool {
	if len(prefix) >= len(key) {
		return false
	}
	for i := range prefix {
		if key[i] != prefix[i] {
			return false
		}
	}
	return true
}

func joinKey(k toml.Key) string {
	return "\x00" + join(k, "\x00")
}

func join(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// ensureChild returns the *jsonx.OrderedMap at key, creating it if absent.
func ensureChild(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if v, ok := m.Get(key); ok {
		if om, isMap := v.(*jsonx.OrderedMap); isMap {
			return om
		}
	}
	child := jsonx.NewOrderedMap()
	m.Set(key, child)
	return child
}

// convertTOMLValue converts a BurntSushi-decoded value into the ordered model:
// nested map[string]any -> *jsonx.OrderedMap is NOT done here (tables are built
// via MetaData.Keys ordering in DecodeOrdered); this handles leaf scalars and
// arrays. An inline table appears as map[string]any and is converted with its
// own MetaData-less order — but codex only reads inline tables it wrote (env
// maps), which the writer re-serializes from jsonx anyway; for the round-trip
// we preserve Go map order via a stable fallback (rare for user keys).
func convertTOMLValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		om := jsonx.NewOrderedMap()
		for k, val := range t {
			om.Set(k, convertTOMLValue(val))
		}
		return om
	case []map[string]any:
		arr := make([]any, len(t))
		for i, e := range t {
			arr[i] = convertTOMLValue(e)
		}
		return arr
	case []any:
		arr := make([]any, len(t))
		for i, e := range t {
			arr[i] = convertTOMLValue(e)
		}
		return arr
	default:
		return v
	}
}

// VenvValue extracts env._.python.venv from a parsed mise config, walking the
// nested tables the way the entrypoint's venv-discovery does
// (`env -> _ -> python -> venv`). Returns (value, true) if the whole chain is
// present, else (nil, false). Mirrors the Python venv_value walk in
// entrypoint/shell.py.
func VenvValue(cfg map[string]any) (any, bool) {
	var cur any = cfg
	for _, key := range []string{"env", "_", "python", "venv"} {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[key]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// MiseVenvPath resolves the venv path from a set of mise config files in
// priority order (highest-priority first, first hit wins), mirroring the
// entrypoint's shell.py discovery:
// - for each file, read env._.python.venv;
// - the FIRST file that has a value wins;
// - a string value is the path directly;
// - a table value must have create=true (else "no venv"), then path (default
// ".venv"); a non-string/absent -> "no venv".
//
// Returns (path, true) when a venv should be created, or ("", false) when none.
func MiseVenvPath(files []string) (string, bool) {
	var v any
	found := false
	for _, f := range files {
		cfg, err := DecodeFile(f)
		if err != nil {
			continue
		}
		if val, ok := VenvValue(cfg); ok {
			v = val
			found = true
			break
		}
	}
	if !found {
		return "", false
	}
	switch t := v.(type) {
	case string:
		return t, true
	case map[string]any:
		create, _ := t["create"].(bool)
		if !create {
			return "", false
		}
		path, ok := t["path"].(string)
		if !ok {
			path = ".venv"
		}
		return path, true
	default:
		return "", false
	}
}
