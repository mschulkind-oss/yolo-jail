package jsonx

import "testing"

func TestDecodePreservesKeyOrder(t *testing.T) {
	// A plain Go map decode would lose this order; OrderedMap must keep it.
	in := []byte(`{"z":1,"a":2,"m":3}`)
	v, err := Decode(in)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(*OrderedMap)
	if !ok {
		t.Fatalf("got %T, want *OrderedMap", v)
	}
	want := []string{"z", "a", "m"}
	got := m.Keys()
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("key order = %v, want %v", got, want)
		}
	}
	// Compact re-encode preserves order (sort_keys=False).
	out, err := DumpsCompact(v)
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"z": 1, "a": 2, "m": 3}` {
		t.Errorf("compact = %q", out)
	}
}

func TestDecodeIntVsFloat(t *testing.T) {
	// An integer literal must re-encode without ".0"; a float keeps its point.
	v, err := Decode([]byte(`{"i": 42, "f": 2.0}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := DumpsCompact(v)
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"i": 42, "f": 2.0}` {
		t.Errorf("int/float re-encode = %q, want {\"i\": 42, \"f\": 2.0}", out)
	}
}

func TestDecodeRejectsTrailingData(t *testing.T) {
	if _, err := Decode([]byte(`{} garbage`)); err == nil {
		t.Error("expected error on trailing data")
	}
}

// TestDecodeNumberNormalizations pins the audit-confirmed Python json parity:
// integer -0 collapses to 0, and a float literal that overflows float64
// re-encodes as Infinity (Python json.loads("1e400") == inf).
func TestDecodeNumberNormalizations(t *testing.T) {
	cases := []struct {
		in   string
		want string // DumpsCompact of the decoded value
	}{
		{`-0`, `0`},             // integer negative zero -> 0
		{`0`, `0`},              //
		{`-0.0`, `-0.0`},        // FLOAT negative zero is preserved
		{`1e400`, `Infinity`},   // overflow -> Infinity
		{`-1e400`, `-Infinity`}, // overflow -> -Infinity
		{`2e308`, `Infinity`},   //
		{`{"a": -0, "b": 1e400}`, `{"a": 0, "b": Infinity}`},
		{`[-0, 1e400, -1e400]`, `[0, Infinity, -Infinity]`},
	}
	for _, tc := range cases {
		v, err := Decode([]byte(tc.in))
		if err != nil {
			t.Errorf("Decode(%q) error: %v", tc.in, err)
			continue
		}
		got, err := DumpsCompact(v)
		if err != nil {
			t.Errorf("DumpsCompact after Decode(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Decode(%q) re-encoded = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestLedgeredDivergences documents (and guards the current behavior of) the
// accepted divergences in docs/design/go-port-divergences.md. If any of these
// starts matching Python, revisit the ledger entry.
func TestLedgeredDivergences(t *testing.T) {
	// D1: bare non-finite literals are not decoded (encoding/json rejects them).
	for _, lit := range []string{"Infinity", "-Infinity", "NaN", "[NaN, Infinity]"} {
		if _, err := Decode([]byte(lit)); err == nil {
			t.Errorf("Decode(%q): expected error (ledger D1); if now supported, update the ledger", lit)
		}
	}
	// D2: lone surrogate -> U+FFFD, re-encoded as the escaped replacement char.
	v, err := Decode([]byte(`"\ud800"`))
	if err != nil {
		t.Fatalf("Decode lone surrogate errored: %v", err)
	}
	got, _ := DumpsCompact(v)
	// encoding/json substitutes the lone surrogate with U+FFFD on decode;
	// ensure_ascii then emits it as the 6-char escape � (<= 0xffff).
	want := "\"" + "\\ufffd" + "\""
	if got != want {
		t.Errorf("lone surrogate re-encoded = %q, want %q (ledger D2)", got, want)
	}
}

func TestDumpsIndentNoSortKeys(t *testing.T) {
	// indent=2 but insertion order preserved (NOT sorted) — the
	// _write_tokens form. Keys z, a, m must stay in that order.
	m := NewOrderedMap()
	m.Set("z", IntValue(1))
	m.Set("a", IntValue(2))
	m.Set("m", IntValue(3))
	got, err := DumpsIndent(m, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"z\": 1,\n  \"a\": 2,\n  \"m\": 3\n}"
	if got != want {
		t.Errorf("DumpsIndent =\n%q\nwant\n%q", got, want)
	}
}

func TestIntValueEncodesAsInt(t *testing.T) {
	m := NewOrderedMap()
	m.Set("ts", IntValue(1700000000000))
	m.Set("n", IntValue(-5))
	got, _ := DumpsCompact(m)
	if got != `{"ts": 1700000000000, "n": -5}` {
		t.Errorf("IntValue encode = %q", got)
	}
}

func TestIntInspection(t *testing.T) {
	// Decoded integer literals are Python ints -> IsInt true.
	v, err := Decode([]byte(`{"n": 42, "neg": -7, "f": 1.5, "b": true, "s": "x"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	m := v.(*OrderedMap)
	nv, _ := m.Get("n")
	if !IsInt(nv) {
		t.Errorf("IsInt(42) = false, want true")
	}
	if got, ok := AsInt(nv); !ok || got != 42 {
		t.Errorf("AsInt(42) = %d,%v want 42,true", got, ok)
	}
	negv, _ := m.Get("neg")
	if got, ok := AsInt(negv); !ok || got != -7 {
		t.Errorf("AsInt(-7) = %d,%v want -7,true", got, ok)
	}
	// float, bool, string are NOT ints (bool decodes to Go bool, not jsonInt).
	for _, key := range []string{"f", "b", "s"} {
		val, _ := m.Get(key)
		if IsInt(val) {
			t.Errorf("IsInt(%s) = true, want false", key)
		}
		if _, ok := AsInt(val); ok {
			t.Errorf("AsInt(%s) ok = true, want false", key)
		}
	}
}

func TestDeleteRemovesKeyAndOrder(t *testing.T) {
	m := NewOrderedMap()
	m.Set("a", 1)
	m.Set("b", 2)
	m.Set("c", 3)
	m.Delete("b")
	if got := m.Keys(); len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Errorf("keys after delete = %v, want [a c]", m.Keys())
	}
	if _, ok := m.Get("b"); ok {
		t.Errorf("Get(b) still present after Delete")
	}
	m.Delete("missing") // no-op
	if m.Len() != 2 {
		t.Errorf("Len after no-op delete = %d, want 2", m.Len())
	}
}

func TestUpdateKeepsPosition(t *testing.T) {
	m := NewOrderedMap()
	m.Set("a", 1)
	m.Set("b", 2)
	m.Set("a", 3) // update, not re-insert
	if len(m.Keys()) != 2 || m.Keys()[0] != "a" {
		t.Errorf("keys = %v, want [a b]", m.Keys())
	}
	if v, _ := m.Get("a"); v != 3 {
		t.Errorf("a = %v, want 3", v)
	}
}
