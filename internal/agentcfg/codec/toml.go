package codec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

// TOML is the codec for the two TOML surfaces yolo composes: Codex's
// ~/.codex/config.toml and the global mise config (§1.1).
//
// Decode is backed by internal/tomlx (which wraps the vendored BurntSushi/toml
// parser); it yields the generic model — tables as map[string]any, arrays as
// []any, scalars as string/bool/int64/float64 (per tomlx.Decode's documented
// shape, matching Python tomllib).
//
// # Encode and the internal/tomlx gap
//
// internal/tomlx exposes DECODE only (Decode / DecodeFile / DecodeOrdered) —
// it has NO encoder, and the brief forbids importing github.com/BurntSushi/toml
// directly or adding any dependency. So Encode is a small deterministic emitter
// implemented HERE, over the same generic model. It is intentionally a subset
// of TOML — enough to round-trip the values yolo composes:
//
//   - scalars: string, bool, int/int64, float64 (and Go's untyped forms)
//   - arrays of scalars      -> inline arrays `k = [1, 2, 3]`
//   - nested maps            -> tables `[a.b]`
//   - arrays of maps         -> arrays of tables `[[a]]`
//
// Keys are emitted in sorted order (scalar/array-of-scalar keys first, then
// sub-tables) so output is deterministic and diff-stable (§3.1). This is the
// documented gap: if a surface ever needs a TOML feature this emitter omits
// (e.g. inline tables, heterogeneous mixed arrays, datetimes), the fix is to
// extend this emitter or add an encoder to internal/tomlx — NOT to reach for a
// new dependency here.
type TOML struct{}

// Name returns "toml".
func (TOML) Name() string { return "toml" }

// Decode parses TOML bytes into the generic model via internal/tomlx, then
// normalizes to the engine's value model. tomlx (BurntSushi) decodes an array
// of tables as []map[string]any; the engine and the rest of this package speak
// []any, so normalizeTOML rewrites those (recursively) into []any.
func (TOML) Decode(data []byte) (any, error) {
	m, err := tomlx.Decode(data)
	if err != nil {
		return nil, err
	}
	return normalizeTOML(m), nil
}

// normalizeTOML converts BurntSushi's []map[string]any (arrays of tables) into
// the generic []any the engine merges over, recursing through maps and slices.
// Other values pass through unchanged.
func normalizeTOML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = normalizeTOML(val)
		}
		return t
	case []map[string]any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = normalizeTOML(e)
		}
		return out
	case []any:
		for i, e := range t {
			t[i] = normalizeTOML(e)
		}
		return t
	default:
		return v
	}
}

// Encode renders a generic value as deterministic TOML. The top-level value
// must be a map (TOML documents are tables); anything else is an error.
func (TOML) Encode(v any) ([]byte, error) {
	m, ok := toStringMap(v)
	if !ok {
		return nil, fmt.Errorf("codec: toml encode requires a top-level table, got %T", v)
	}
	var sb strings.Builder
	if err := encodeTable(&sb, nil, m); err != nil {
		return nil, err
	}
	return []byte(sb.String()), nil
}

// toStringMap accepts the two map shapes the model can carry (map[string]any
// from a plain decode; other string-keyed maps are not produced here).
func toStringMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// encodeTable writes the scalar/array-of-scalar keys of m under the given key
// path (empty for the root), then recurses into sub-tables and arrays-of-tables.
// Keys are sorted at every level for determinism.
func encodeTable(sb *strings.Builder, path []string, m map[string]any) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// First pass: leaf key/value pairs (scalars and scalar arrays).
	var subTables, tableArrays []string
	for _, k := range keys {
		switch child := m[k].(type) {
		case map[string]any:
			subTables = append(subTables, k)
		case []any:
			if isTableArray(child) {
				tableArrays = append(tableArrays, k)
			} else {
				lit, err := encodeArray(child)
				if err != nil {
					return err
				}
				fmt.Fprintf(sb, "%s = %s\n", encodeKey(k), lit)
			}
		default:
			lit, err := encodeScalar(m[k])
			if err != nil {
				return err
			}
			fmt.Fprintf(sb, "%s = %s\n", encodeKey(k), lit)
		}
	}

	// Second pass: sub-tables as [a.b] headers.
	for _, k := range subTables {
		child := m[k].(map[string]any)
		childPath := append(append([]string(nil), path...), k)
		fmt.Fprintf(sb, "\n[%s]\n", encodeKeyPath(childPath))
		if err := encodeTable(sb, childPath, child); err != nil {
			return err
		}
	}

	// Third pass: arrays of tables as [[a.b]] blocks.
	for _, k := range tableArrays {
		childPath := append(append([]string(nil), path...), k)
		for _, elem := range m[k].([]any) {
			em, _ := toStringMap(elem)
			fmt.Fprintf(sb, "\n[[%s]]\n", encodeKeyPath(childPath))
			if err := encodeTable(sb, childPath, em); err != nil {
				return err
			}
		}
	}
	return nil
}

// isTableArray reports whether every element of arr is a map (so it emits as an
// array of tables rather than an inline array). An empty array is NOT a table
// array (it emits as `k = []`).
func isTableArray(arr []any) bool {
	if len(arr) == 0 {
		return false
	}
	for _, e := range arr {
		if _, ok := e.(map[string]any); !ok {
			return false
		}
	}
	return true
}

// encodeArray renders an inline array of scalars. Nested arrays are supported;
// arrays containing tables reach here only when mixed (some non-map elements),
// which TOML cannot represent inline — that is an error.
func encodeArray(arr []any) (string, error) {
	parts := make([]string, 0, len(arr))
	for _, e := range arr {
		switch ev := e.(type) {
		case map[string]any:
			return "", fmt.Errorf("codec: toml cannot encode a mixed array containing a table")
		case []any:
			lit, err := encodeArray(ev)
			if err != nil {
				return "", err
			}
			parts = append(parts, lit)
		default:
			lit, err := encodeScalar(e)
			if err != nil {
				return "", err
			}
			parts = append(parts, lit)
		}
	}
	return "[" + strings.Join(parts, ", ") + "]", nil
}

// encodeScalar renders a single scalar value as a TOML literal.
func encodeScalar(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", fmt.Errorf("codec: toml has no null; a nil value cannot be encoded")
	case string:
		return encodeString(t), nil
	case bool:
		if t {
			return "true", nil
		}
		return "false", nil
	case int:
		return strconv.FormatInt(int64(t), 10), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case float64:
		// Shortest round-tripping form; ensure a decimal point so it decodes
		// back as a float, not an int.
		s := strconv.FormatFloat(t, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return s, nil
	default:
		return "", fmt.Errorf("codec: toml cannot encode value of type %T", v)
	}
}

// encodeString renders a TOML basic string with the standard escapes.
func encodeString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&sb, `\u%04x`, r)
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// bareKey matches a TOML bare key (letters, digits, '_', '-'); anything else
// must be quoted.
func isBareKey(k string) bool {
	if k == "" {
		return false
	}
	for _, r := range k {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// encodeKey renders a single key segment, quoting it when not a bare key.
func encodeKey(k string) string {
	if isBareKey(k) {
		return k
	}
	return encodeString(k)
}

// encodeKeyPath renders a dotted table path, quoting segments as needed.
func encodeKeyPath(path []string) string {
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = encodeKey(p)
	}
	return strings.Join(parts, ".")
}
