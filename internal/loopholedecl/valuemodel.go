package loopholedecl

import (
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// valuemodel.go is how the schema READS a decoded JSON5 value: which values count
// as true (`enabled`, `broker_ip`), and how a non-string scalar becomes the string
// a string-typed field wants. These are schema semantics, not generic utilities —
// `"enabled": 0` meaning disabled is a fact about the manifest format — so they are
// exported for the one other place that applies the SAME schema to the SAME value
// model: the `loopholes:` config block, whose inline entries mirror manifest keys.

// Str renders a decoded-JSON scalar as the schema's string: a string as-is, a
// bool as True/False, an integer as decimal, anything else through its repr.
func Str(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		return jsonx.FormatFloatRepr(t)
	default:
		if lit, ok := jsonx.AsIntLiteral(v); ok {
			return lit
		}
		s, _ := jsonx.DumpsCompact(v)
		return s
	}
}

// Truthy is the schema's truthiness for a decoded-JSON value: absent, null,
// false, "", 0 and an empty list/object are false; everything else is true.
func Truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return len(t) > 0
	case float64:
		return t != 0
	case []any:
		return len(t) > 0
	case *jsonx.OrderedMap:
		return t.Len() > 0
	default:
		if lit, ok := jsonx.AsIntLiteral(v); ok {
			return !isZeroIntLiteral(lit)
		}
		return true
	}
}

// AllStrings reports whether every element of a decoded list is a string — the
// shape check every argv-valued field makes.
func AllStrings(list []any) bool {
	for _, x := range list {
		if _, ok := x.(string); !ok {
			return false
		}
	}
	return true
}

// StringSlice coerces a decoded list of strings (AllStrings must hold).
func StringSlice(list []any) []string {
	out := make([]string, len(list))
	for i, x := range list {
		out[i], _ = x.(string)
	}
	return out
}

func isZeroIntLiteral(lit string) bool {
	s := strings.TrimPrefix(lit, "-")
	s = strings.TrimPrefix(s, "+")
	for _, c := range s {
		if c != '0' {
			return false
		}
	}
	return len(s) > 0
}

// atoiOr parses an integer literal, falling back to def.
func atoiOr(lit string, def int) int {
	n, err := strconv.Atoi(lit)
	if err != nil {
		return def
	}
	return n
}

func inList(s string, list []string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// sortedListRepr renders a list literal of the sorted values.
func sortedListRepr(values []string) string {
	sorted := copyOf(values)
	sort.Strings(sorted)
	parts := make([]string, len(sorted))
	for i, s := range sorted {
		parts[i] = pytext.Repr(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// --- small decode helpers for `get(key) or default` idioms ---

// orEmptyList implements `get(key) or []`: a falsy value yields an empty list
// (which passes the list type check); a truthy non-list stays as-is (so the
// caller's type check fires the error).
func orEmptyList(m *jsonx.OrderedMap, key string) any {
	v, ok := m.Get(key)
	if !ok || !Truthy(v) {
		return []any{}
	}
	return v
}

// orEmptyMap implements `get(key) or {}` for the jail_env path.
func orEmptyMap(m *jsonx.OrderedMap, key string) any {
	v, ok := m.Get(key)
	if !ok || !Truthy(v) {
		return jsonx.NewOrderedMap()
	}
	return v
}

func orEmptyMapValue(v any) any {
	if !Truthy(v) {
		return jsonx.NewOrderedMap()
	}
	return v
}

// getOrNil returns m[key] or nil when absent.
func getOrNil(m *jsonx.OrderedMap, key string) any {
	v, ok := m.Get(key)
	if !ok {
		return nil
	}
	return v
}
