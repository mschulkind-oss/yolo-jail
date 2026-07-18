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
