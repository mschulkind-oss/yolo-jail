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
