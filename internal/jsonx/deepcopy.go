package jsonx

// deepcopy.go clones a value in jsonx's own model — the one Decode and json5.Decode
// produce (*OrderedMap / []any / string / bool / jsonInt / float64 / nil) — so that a
// producer which lays one caller's decoded config UNDER or OVER another's can hand its
// output out without the two sharing a single mutable object.
//
// IT LIVES HERE AND NOT AT THE CALL SITE for one reason: a copier is only correct if it
// enumerates every MUTABLE type the model can hold, and which types those are is this
// package's fact, not a caller's. A copier written elsewhere gets its exhaustiveness from
// reading this file today; the day jsonx grows a mutable value type, that copier's default
// branch silently starts aliasing again — which is the exact class of defect the copy
// exists to end. (internal/agentcfg has the twin for the PLAIN model, and it sits beside
// the merge that owns that model for the same reason.)
//
// It is not Plain's sibling in behaviour: Plain LOWERS the model (an integer literal
// becomes a float64, an *OrderedMap loses its order), so a value round-tripped through it
// no longer re-encodes byte-identically. DeepCopy changes nothing but identity.

// DeepCopy returns a value equal to v that shares no mutable object with it: every
// *OrderedMap, []any, []string and map[string]any reachable from the result is a fresh
// allocation, recursively. Scalars — string, bool, the numeric types, an integer literal
// preserved by Decode, and nil — are immutable and returned as themselves.
//
// A type this package's encoder does not know is returned as-is. That is the honest
// answer rather than a silent failure: such a value cannot have come from a decode, so
// the caller injected it and is the only party that knows whether it is shared.
func DeepCopy(v any) any {
	switch t := v.(type) {
	case *OrderedMap:
		return DeepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = DeepCopy(e)
		}
		return out
	case []string:
		return append([]string(nil), t...)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = DeepCopy(e)
		}
		return out
	default:
		return v
	}
}

// DeepCopyMap returns a deep copy of m, key order preserved. A nil map copies to nil, so
// "absent" survives the copy — the distinction Decode draws between a missing object and
// an empty one.
func DeepCopyMap(m *OrderedMap) *OrderedMap {
	if m == nil {
		return nil
	}
	out := NewOrderedMap()
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out.Set(k, DeepCopy(v))
	}
	return out
}
