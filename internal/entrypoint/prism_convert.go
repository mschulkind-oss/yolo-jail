package entrypoint

// prism_convert.go bridges yolo's runtime value model (jsonx — order-preserving
// *OrderedMap, integer-literal-preserving jsonInt) to the plain, stdlib-native
// generic model the agentcfg engine merges and its JSON codec re-encodes.
//
// This is the "connecting seam" of the config-composition cutover: the computed
// layer (Inputs.Computed) is yolo's per-boot DYNAMIC content — the reconciled
// MCP-server table, the LSP-derived toggles — and it originates as jsonx values
// (LoadMCPServers returns an *OrderedMap; env-sourced config decodes via jsonx).
// The engine, by contrast, operates purely over map[string]any / []any / stdlib
// scalars (engine.go's package doc), and its JSON codec (codec/json.go) uses
// encoding/json for BOTH decode and encode. Handing the engine a raw
// *OrderedMap or jsonInt would be doubly wrong:
//
//   - deepMerge / mergeValue / diffValue type-switch on map[string]any. An
//     *OrderedMap value is not that type, so it would be treated as an opaque
//     scalar — merged wholesale instead of key-by-key, defeating layering.
//   - encoding/json.Marshal of the unexported jsonInt (a named string type)
//     emits a QUOTED STRING ("5"), not a number (5). float64 is what stdlib
//     decode produces, so converting jsonInt→float64 keeps the render's
//     last_render sidecar byte-stable across the decode/encode round-trip.
//
// The engine deliberately has no jsonx dependency (it is a pure leaf), so the
// conversion lives here, in the one caller that holds both models.

import "github.com/mschulkind-oss/yolo-jail/internal/jsonx"

// prismMap deeply converts a jsonx OrderedMap into the engine's plain
// map[string]any model. A nil map yields nil (an absent computed layer).
func prismMap(m *jsonx.OrderedMap) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, m.Len())
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out[k] = prismValue(v)
	}
	return out
}

// prismValue deeply converts one jsonx value into the engine's generic model:
// *OrderedMap → map[string]any (recursively), []any recursed element-wise,
// jsonInt → float64 (to survive the stdlib JSON round-trip), and every other
// scalar (string, bool, float64, nil) passed through unchanged. A nil stays nil
// so a computed-layer tombstone (RFC-7386 null) is preserved.
func prismValue(v any) any {
	switch t := v.(type) {
	case *jsonx.OrderedMap:
		return prismMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = prismValue(e)
		}
		return out
	default:
		// jsonInt is unexported; detect it via the jsonx accessor and normalize
		// to float64 (what encoding/json produces from a JSON number), so a
		// computed integer re-encodes as a number, not a quoted string.
		if lit, ok := jsonx.AsIntLiteral(v); ok {
			return jsonIntLiteralToFloat(lit)
		}
		return v
	}
}

// jsonIntLiteralToFloat parses a decimal integer literal to float64. yolo's
// computed integers (MCP config has none in practice; this is the general,
// correct path) are all well within float64's exact-integer range, so the
// conversion is lossless for every value that actually occurs. A malformed
// literal (never produced by jsonx) yields 0.
func jsonIntLiteralToFloat(lit string) float64 {
	var f float64
	neg := false
	s := lit
	if len(s) > 0 && s[0] == '-' {
		neg = true
		s = s[1:]
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		f = f*10 + float64(c-'0')
	}
	if neg {
		return -f
	}
	return f
}
