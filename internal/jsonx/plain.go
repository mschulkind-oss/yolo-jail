package jsonx

// plain.go converts jsonx's config value model (order-preserving *OrderedMap,
// integer-literal-preserving jsonInt) into the plain, stdlib-native generic model
// — map[string]any / []any / string / bool / float64 / nil.
//
// It lives here rather than in a caller because two unrelated subsystems need the
// SAME conversion and must not drift: internal/entrypoint bridges the computed
// layer into the agentcfg engine, and internal/config lowers a user-declared
// `host_files` entry's `defaults`/`managed`/`content` into engine layers. Both
// feed the same engine, whose merge and encode steps are unforgiving about the
// two jsonx types:
//
//   - deepMerge/mergeValue type-switch on map[string]any. An *OrderedMap is not
//     that type, so it would be treated as an opaque scalar — replaced wholesale
//     instead of merged key-by-key, silently defeating layering.
//   - encoding/json.Marshal of jsonInt (a named string type) emits a QUOTED
//     STRING ("5"), not a number (5). float64 is what a stdlib decode produces, so
//     normalizing here keeps a render's last_render sidecar byte-stable across the
//     decode/encode round-trip.

import (
	"errors"
	"math"
	"strconv"
)

// PlainMap deeply converts an OrderedMap to the plain generic model. A nil map
// yields nil, so "this layer is absent" survives the conversion (the engine
// distinguishes a nil layer from an empty one).
func PlainMap(m *OrderedMap) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, m.Len())
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out[k] = Plain(v)
	}
	return out
}

// Plain deeply converts one jsonx value to the plain generic model: *OrderedMap →
// map[string]any (recursively), []any recursed element-wise, an integer literal →
// float64, and every other scalar (string, bool, float64, nil) passed through
// unchanged. A nil stays nil so an RFC-7386 null tombstone is preserved.
func Plain(v any) any {
	switch t := v.(type) {
	case *OrderedMap:
		return PlainMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = Plain(e)
		}
		return out
	default:
		if lit, ok := AsIntLiteral(v); ok {
			return intLiteralToFloat(lit)
		}
		return v
	}
}

// intLiteralToFloat parses a decimal integer literal to float64. Config integers
// are all well within float64's exact-integer range, so this is lossless for
// every value that actually occurs; an arbitrary-precision literal beyond 2^53
// loses low bits, which is the same thing a stdlib JSON decode of that number
// would do.
//
// An OVERFLOWING literal (a magnitude past float64's max — reachable because
// IntLiteral accepts arbitrary-precision decimals, e.g. a big hex conversion)
// keeps the ±Inf that ParseFloat returns alongside ErrRange. Returning 0 there
// instead would silently turn an enormous number into zero in a rendered config;
// ±Inf re-encodes as Infinity, which is at least loud and matches what
// numberToValue does for an overflowing FLOAT literal. Only a genuine syntax
// error — never produced by Decode/IntLiteral, whose looksNumeric gate rejects
// it — yields 0.
func intLiteralToFloat(lit string) float64 {
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) && math.IsInf(f, 0) {
			return f
		}
		return 0
	}
	return f
}
