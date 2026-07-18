package config

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// pyReprValue reproduces Python's repr() for the decoded-JSON value types that
// reach a "{x!r}" validation error where x may be non-string (journal, kvm,
// gpu.enabled, ephemeral_storage, an env var name that failed the type check).
//
//	None      -> "None"
//	True/False-> "True"/"False"
//	int       -> decimal (jsonx int literal, verbatim — arbitrary precision)
//	float     -> repr(float) (jsonx.FormatFloatRepr)
//	str       -> pytext.Repr (quote choice + escapes)
//	list/dict -> bracketed repr of elements (repr of each; dict as {k!r: v!r})
func pyReprValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case string:
		return pytext.Repr(t)
	case float64:
		return jsonx.FormatFloatRepr(t)
	case []any:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = pyReprValue(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *jsonx.OrderedMap:
		var parts []string
		for _, k := range t.Keys() {
			val, _ := t.Get(k)
			parts = append(parts, pytext.Repr(k)+": "+pyReprValue(val))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		if lit, ok := jsonx.AsIntLiteral(v); ok {
			return lit
		}
		return "None"
	}
}
