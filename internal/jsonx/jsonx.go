// Package jsonx is a canonical JSON encoder/decoder byte-compatible with
// Python's json module for the forms yolo-jail relies on:
//
//   - DumpsSnapshot: json.dumps(obj, indent=2, sort_keys=True,
//     ensure_ascii=True) — the config-snapshot format. A single byte of drift
//     here fires a spurious config-approval prompt on every Python<->Go switch
//     (the most user-visible regression the port can cause), so it is pinned
//     by a large cross-language corpus.
//   - DumpsCompact: json.dumps(obj) with Python's DEFAULT separators
//     (", " and ": ") — used for YOLO_EXTRA_PACKAGES / YOLO_JAIL_DAEMONS env
//     values that feed --impure nix eval and the entrypoint.
//   - Order-preserving Decode: Python dicts are insertion-ordered; Go maps are
//     not. Decoding into an OrderedMap preserves key order so re-encoding
//     round-trips byte-identically.
//
// Go's encoding/json differs from Python in ways that matter here: it HTML-
// escapes <, >, & by default; it emits "\u00XX" lowercase-hex where Python
// uses the same but there are float-repr and separator differences. This
// package implements the encoder directly to match Python exactly.
//
// Source of truth: Python's json.dumps observed behavior.
package jsonx

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// OrderedMap is an insertion-ordered string-keyed map, the Go analog of a
// Python dict. Decode produces these so re-encoding preserves key order.
type OrderedMap struct {
	keys   []string
	values map[string]any
}

// NewOrderedMap returns an empty OrderedMap.
func NewOrderedMap() *OrderedMap {
	return &OrderedMap{values: map[string]any{}}
}

// Set inserts or updates key. A new key is appended to the order; updating an
// existing key keeps its position (Python dict semantics).
func (m *OrderedMap) Set(key string, value any) {
	if _, ok := m.values[key]; !ok {
		m.keys = append(m.keys, key)
	}
	m.values[key] = value
}

// Get returns the value for key and whether it is present.
func (m *OrderedMap) Get(key string) (any, bool) {
	v, ok := m.values[key]
	return v, ok
}

// Keys returns the keys in insertion order (do not mutate).
func (m *OrderedMap) Keys() []string { return m.keys }

// Len returns the number of entries.
func (m *OrderedMap) Len() int { return len(m.keys) }

// encoder holds the mode flags for one Dumps call.
type encoder struct {
	sb       strings.Builder
	sortKeys bool
	indent   int    // >0 => pretty; 0 => compact
	itemSep  string // between items
	keySep   string // between key and value
}

// DumpsSnapshot renders v as json.dumps(v, indent=2, sort_keys=True,
// ensure_ascii=True). No trailing newline (Python's json.dumps adds none).
func DumpsSnapshot(v any) (string, error) {
	e := &encoder{sortKeys: true, indent: 2, itemSep: ",", keySep: ": "}
	if err := e.encode(v, 0); err != nil {
		return "", err
	}
	return e.sb.String(), nil
}

// DumpsCompact renders v as json.dumps(v) with Python's default separators
// (", " between items, ": " between key and value), sort_keys=False,
// ensure_ascii=True.
func DumpsCompact(v any) (string, error) {
	e := &encoder{sortKeys: false, indent: 0, itemSep: ", ", keySep: ": "}
	if err := e.encode(v, 0); err != nil {
		return "", err
	}
	return e.sb.String(), nil
}

// DumpsIndent renders v as json.dumps(v, indent=n) — pretty-printed but
// WITHOUT sort_keys (insertion order preserved), ensure_ascii=True. This is the
// form src/oauth_broker.py:_write_tokens uses (indent=2, no sort_keys), where
// key order must follow the oauth object's insertion order, not sorted order.
func DumpsIndent(v any, n int) (string, error) {
	e := &encoder{sortKeys: false, indent: n, itemSep: ",", keySep: ": "}
	if err := e.encode(v, 0); err != nil {
		return "", err
	}
	return e.sb.String(), nil
}

// IntValue constructs a JSON integer value (re-encodes with no ".0", matching
// Python's int). Use this to inject computed integers (timestamps, expires_in)
// into an OrderedMap so they serialize like Python ints, not floats.
func IntValue(n int64) any {
	return jsonInt(strconv.FormatInt(n, 10))
}

func (e *encoder) newlineIndent(depth int) {
	if e.indent == 0 {
		return
	}
	e.sb.WriteByte('\n')
	e.sb.WriteString(strings.Repeat(" ", e.indent*depth))
}

func (e *encoder) encode(v any, depth int) error {
	switch t := v.(type) {
	case nil:
		e.sb.WriteString("null")
	case bool:
		if t {
			e.sb.WriteString("true")
		} else {
			e.sb.WriteString("false")
		}
	case string:
		e.encodeString(t)
	case int:
		e.sb.WriteString(strconv.Itoa(t))
	case int64:
		e.sb.WriteString(strconv.FormatInt(t, 10))
	case float64:
		e.sb.WriteString(formatFloat(t))
	case jsonInt:
		e.sb.WriteString(string(t))
	case []any:
		e.encodeArray(t, depth)
	case []string:
		arr := make([]any, len(t))
		for i, s := range t {
			arr[i] = s
		}
		e.encodeArray(arr, depth)
	case *OrderedMap:
		e.encodeOrderedMap(t, depth)
	case map[string]any:
		// Unordered map: Python would preserve insertion order, but a Go map
		// has none. sort_keys=True makes this deterministic; sort_keys=False
		// on a plain map is inherently unordered, so we sort as the only
		// stable choice (callers needing order use OrderedMap).
		e.encodeStringMap(t, depth, true)
	default:
		return fmt.Errorf("jsonx: unsupported type %T", v)
	}
	return nil
}

func (e *encoder) encodeArray(arr []any, depth int) {
	if len(arr) == 0 {
		e.sb.WriteString("[]")
		return
	}
	e.sb.WriteByte('[')
	for i, item := range arr {
		if i > 0 {
			e.sb.WriteString(e.itemSep)
		}
		e.newlineIndent(depth + 1)
		_ = e.encode(item, depth+1)
	}
	e.newlineIndent(depth)
	e.sb.WriteByte(']')
}

func (e *encoder) encodeOrderedMap(m *OrderedMap, depth int) {
	keys := m.keys
	if e.sortKeys {
		keys = append([]string(nil), m.keys...)
		sort.Strings(keys)
	}
	e.emitObject(keys, func(k string) any { return m.values[k] }, depth)
}

func (e *encoder) encodeStringMap(m map[string]any, depth int, forceSort bool) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	if e.sortKeys || forceSort {
		sort.Strings(keys)
	}
	e.emitObject(keys, func(k string) any { return m[k] }, depth)
}

func (e *encoder) emitObject(keys []string, get func(string) any, depth int) {
	if len(keys) == 0 {
		e.sb.WriteString("{}")
		return
	}
	e.sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			e.sb.WriteString(e.itemSep)
		}
		e.newlineIndent(depth + 1)
		e.encodeString(k)
		e.sb.WriteString(e.keySep)
		_ = e.encode(get(k), depth+1)
	}
	e.newlineIndent(depth)
	e.sb.WriteByte('}')
}

// encodeString writes s as a Python json.dumps string literal with
// ensure_ascii=True: ASCII control and non-ASCII code points become \uXXXX
// (lowercase hex), with the short escapes Python uses for the common ones.
// Notably Python does NOT escape <, >, & (unlike Go's default encoder).
func (e *encoder) encodeString(s string) {
	e.sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			e.sb.WriteString(`\"`)
		case '\\':
			e.sb.WriteString(`\\`)
		case '\n':
			e.sb.WriteString(`\n`)
		case '\r':
			e.sb.WriteString(`\r`)
		case '\t':
			e.sb.WriteString(`\t`)
		case '\b':
			e.sb.WriteString(`\b`)
		case '\f':
			e.sb.WriteString(`\f`)
		default:
			switch {
			case r < 0x20:
				fmt.Fprintf(&e.sb, `\u%04x`, r)
			case r < 0x7f:
				e.sb.WriteRune(r)
			case r <= 0xffff:
				fmt.Fprintf(&e.sb, `\u%04x`, r)
			default:
				// Python emits a UTF-16 surrogate pair for astral code points.
				r1, r2 := utf16Surrogates(r)
				fmt.Fprintf(&e.sb, `\u%04x\u%04x`, r1, r2)
			}
		}
	}
	e.sb.WriteByte('"')
}

// jsonInt is a raw integer literal preserved through decode so a value that
// was an int in the source re-encodes as an int (no ".0"). Python distinguishes
// int from float; Go's default JSON decode collapses both to float64.
type jsonInt string

// formatFloat matches Python's repr(float) as embedded in json.dumps —
// json.encoder uses float.__repr__. Python's repr yields the shortest
// round-tripping decimal and chooses fixed vs exponential notation by the
// decimal point position: exponential iff decpt <= -4 || decpt > 16
// (CPython pystrtod.c format_float_short, 'r' mode). Go's strconv 'g' format
// uses a different threshold (exp at decpt > 21) and drops the trailing ".0",
// so we can't use it directly — this is the int-vs-float repr hazard the port
// plan calls out.
func formatFloat(f float64) string {
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	if math.IsInf(f, -1) {
		return "-Infinity"
	}
	if math.IsNaN(f) {
		return "NaN"
	}

	// Shortest round-tripping decimal in canonical exponential form:
	// "[-]D[.DDDD]e±DD". Parse out the significant digits and decimal exponent.
	neg := math.Signbit(f)
	s := strconv.FormatFloat(math.Abs(f), 'e', -1, 64)
	ei := strings.IndexByte(s, 'e')
	mant := s[:ei]
	expVal, _ := strconv.Atoi(s[ei+1:])
	digits := strings.Replace(mant, ".", "", 1) // all significant digits
	// decpt = number of digits to the left of the decimal point in fixed form.
	// value = digits[0].digits[1:] × 10^expVal, so decpt = expVal + 1.
	decpt := expVal + 1

	var out string
	if decpt <= -4 || decpt > 16 {
		out = formatExp(digits, decpt)
	} else {
		out = formatFixed(digits, decpt)
	}
	if neg {
		out = "-" + out
	}
	return out
}

// formatExp renders digits with decimal exponent decpt in Python repr exp form:
// mantissa (D or D.DDD) + "e" + sign + ≥2-digit exponent, where the printed
// exponent is decpt-1.
func formatExp(digits string, decpt int) string {
	var mant string
	if len(digits) == 1 {
		mant = digits
	} else {
		mant = digits[:1] + "." + digits[1:]
	}
	exp := decpt - 1
	sign := "+"
	if exp < 0 {
		sign = "-"
		exp = -exp
	}
	es := strconv.Itoa(exp)
	if len(es) < 2 {
		es = "0" + es
	}
	return mant + "e" + sign + es
}

// formatFixed renders digits with decimal exponent decpt in Python repr fixed
// form, always including a decimal point (integral floats show ".0").
func formatFixed(digits string, decpt int) string {
	switch {
	case decpt <= 0:
		return "0." + strings.Repeat("0", -decpt) + digits
	case decpt >= len(digits):
		return digits + strings.Repeat("0", decpt-len(digits)) + ".0"
	default:
		return digits[:decpt] + "." + digits[decpt:]
	}
}

func utf16Surrogates(r rune) (rune, rune) {
	r -= 0x10000
	hi := 0xd800 + (r >> 10)
	lo := 0xdc00 + (r & 0x3ff)
	return hi, lo
}
