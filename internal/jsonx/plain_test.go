package jsonx

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

// TestPlainMapNested proves the conversion is DEEP: a nested OrderedMap inside a
// list inside a map all become the plain model. A shallow conversion would leave
// an *OrderedMap where the agentcfg engine type-switches on map[string]any, and
// the engine would treat that whole subtree as an opaque scalar — replacing it
// wholesale instead of merging key-by-key.
func TestPlainMapNested(t *testing.T) {
	inner := NewOrderedMap()
	inner.Set("deep", "value")
	listed := NewOrderedMap()
	listed.Set("in", "list")

	m := NewOrderedMap()
	m.Set("obj", inner)
	m.Set("arr", []any{listed, "scalar", nil})

	got := PlainMap(m)
	want := map[string]any{
		"obj": map[string]any{"deep": "value"},
		"arr": []any{map[string]any{"in": "list"}, "scalar", nil},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PlainMap = %#v, want %#v", got, want)
	}
}

// TestPlainMapNilIsNil: a nil map converts to nil, not to an empty map. The
// engine distinguishes an ABSENT layer (nil — says nothing) from an EMPTY one (a
// real assertion), so flattening the difference here would turn "no computed
// layer" into "an empty patch".
func TestPlainMapNilIsNil(t *testing.T) {
	if got := PlainMap(nil); got != nil {
		t.Errorf("PlainMap(nil) = %#v, want nil", got)
	}
}

// TestPlainIntBecomesJSONNumber is the reason integers are normalized: jsonInt is
// a named STRING type, so encoding/json would marshal a preserved integer literal
// as "5" (quoted) rather than 5. That would corrupt every rendered config
// carrying an integer and break last_render byte-stability across the round-trip.
func TestPlainIntBecomesJSONNumber(t *testing.T) {
	m := NewOrderedMap()
	m.Set("n", IntValue(5))
	m.Set("neg", IntValue(-42))

	plain := PlainMap(m)
	if plain["n"] != float64(5) {
		t.Errorf("n = %#v (%T), want float64(5)", plain["n"], plain["n"])
	}
	if plain["neg"] != float64(-42) {
		t.Errorf("neg = %#v (%T), want float64(-42)", plain["neg"], plain["neg"])
	}

	// The point of the conversion: stdlib marshal now emits numbers, not strings.
	encoded, err := json.Marshal(plain)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if got := string(encoded); got != `{"n":5,"neg":-42}` {
		t.Errorf("marshalled = %s, want unquoted numbers", got)
	}
}

// TestPlainIntLiteralEdges pins the two edges where this conversion is not just
// "parse a small number", both of which a hand-rolled digit loop (the version
// this function replaced) got wrong or would get wrong:
//
//   - An arbitrary-precision literal — reachable via IntLiteral, e.g. a big hex
//     conversion — must round to the NEAREST float64. A left-to-right f*10+digit
//     loop accumulates its own rounding error and lands a ulp or two off.
//   - A literal past float64's max must stay ±Inf, NOT collapse to 0. ParseFloat
//     returns (±Inf, ErrRange) for these, so a bare `if err != nil { return 0 }`
//     would silently render an enormous number as zero. ±Inf re-encodes as
//     Infinity — wrong-ish but loud — and matches what numberToValue already does
//     for an overflowing float literal.
func TestPlainIntLiteralEdges(t *testing.T) {
	huge := "1" + strings.Repeat("0", 400) // 1e400: past float64 max
	tests := []struct {
		name string
		lit  string
		want float64
	}{
		{"exact", "5", 5},
		{"negative", "-42", -42},
		{"negative zero collapses", "-0", 0},
		{"2^53+1 rounds to even", "9007199254740993", 9007199254740992},
		{"30 digits rounds to nearest", "123456789012345678901234567890", 1.2345678901234568e+29},
		{"overflow stays +Inf", huge, math.Inf(1)},
		{"negative overflow stays -Inf", "-" + huge, math.Inf(-1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, ok := IntLiteral(tt.lit)
			if !ok {
				t.Fatalf("IntLiteral(%q) rejected the literal", tt.lit)
			}
			got := Plain(v)
			if got != any(tt.want) {
				t.Errorf("Plain(IntLiteral(%q)) = %v (%T), want %v", tt.lit, got, got, tt.want)
			}
		})
	}
}

// TestPlainScalarsPassThrough: the scalars the engine already understands are
// returned untouched, nil included (an RFC-7386 tombstone must survive).
func TestPlainScalarsPassThrough(t *testing.T) {
	for _, v := range []any{"s", true, false, float64(1.5), nil} {
		if got := Plain(v); got != v {
			t.Errorf("Plain(%#v) = %#v, want unchanged", v, got)
		}
	}
}

// TestPlainRoundTripsDecodedConfig is the end-to-end shape: a decoded JSONC
// value converts to something encoding/json re-emits identically, which is what
// makes a composed surface's bytes stable boot over boot.
func TestPlainRoundTripsDecodedConfig(t *testing.T) {
	const src = `{"a":1,"b":[1,2,{"c":"x"}],"d":{"e":null},"f":1.25,"g":true}`
	decoded, err := Decode([]byte(src))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	m, ok := decoded.(*OrderedMap)
	if !ok {
		t.Fatalf("Decode returned %T, want *OrderedMap", decoded)
	}
	encoded, err := json.Marshal(PlainMap(m))
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	// Compare through a stdlib decode of both, so key ORDER (which the plain
	// model does not preserve) doesn't make an equivalent value look different.
	var wantAny, gotAny any
	if err := json.Unmarshal([]byte(src), &wantAny); err != nil {
		t.Fatalf("unmarshal src: %v", err)
	}
	if err := json.Unmarshal(encoded, &gotAny); err != nil {
		t.Fatalf("unmarshal round-trip: %v", err)
	}
	if !reflect.DeepEqual(gotAny, wantAny) {
		t.Errorf("round-trip changed the value:\n got: %#v\nwant: %#v", gotAny, wantAny)
	}
}
