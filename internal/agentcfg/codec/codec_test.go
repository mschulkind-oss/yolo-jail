package codec

import (
	"reflect"
	"testing"
)

// TestLookupCodec checks the registry resolves the four documented names and
// rejects anything else, and that each resolved codec reports its own name.
func TestLookupCodec(t *testing.T) {
	for _, name := range []string{"json", "toml", "lines", "raw"} {
		c, ok := LookupCodec(name)
		if !ok {
			t.Fatalf("LookupCodec(%q): not found", name)
		}
		if c.Name() != name {
			t.Errorf("LookupCodec(%q).Name() = %q", name, c.Name())
		}
	}
	if _, ok := LookupCodec("yaml"); ok {
		t.Error("LookupCodec(\"yaml\"): unexpectedly found (not built yet)")
	}
	if _, ok := LookupCodec(""); ok {
		t.Error("LookupCodec(\"\"): unexpectedly found")
	}
}

// roundTrip is the shared table-driven contract: Decode(in) equals the expected
// value, Encode of that value equals the golden bytes, and decoding the golden
// bytes reproduces the same value (Encode->Decode stability).
type roundTrip struct {
	name   string
	in     string // input bytes to Decode
	value  any    // expected decoded value
	golden string // expected Encode output for `value`
}

func runRoundTrips(t *testing.T, c Codec, cases []roundTrip) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Decode([]byte(tc.in))
			if err != nil {
				t.Fatalf("Decode(%q): %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.value) {
				t.Fatalf("Decode(%q) = %#v, want %#v", tc.in, got, tc.value)
			}
			enc, err := c.Encode(tc.value)
			if err != nil {
				t.Fatalf("Encode(%#v): %v", tc.value, err)
			}
			if string(enc) != tc.golden {
				t.Fatalf("Encode(%#v) = %q, want golden %q", tc.value, enc, tc.golden)
			}
			// Encode -> Decode stability: the golden bytes decode back equal.
			back, err := c.Decode(enc)
			if err != nil {
				t.Fatalf("Decode(golden %q): %v", enc, err)
			}
			if !reflect.DeepEqual(back, tc.value) {
				t.Fatalf("Decode(Encode(v)) = %#v, want %#v", back, tc.value)
			}
			// Encode is deterministic: encoding twice yields identical bytes.
			enc2, err := c.Encode(tc.value)
			if err != nil {
				t.Fatalf("Encode (second): %v", err)
			}
			if string(enc2) != string(enc) {
				t.Fatalf("Encode not deterministic: %q vs %q", enc, enc2)
			}
		})
	}
}

func TestJSON(t *testing.T) {
	cases := []roundTrip{
		{
			name:   "scalars",
			in:     `{"b":true,"a":"x","n":3}`,
			value:  map[string]any{"a": "x", "b": true, "n": float64(3)},
			golden: "{\n  \"a\": \"x\",\n  \"b\": true,\n  \"n\": 3\n}",
		},
		{
			name:   "nested-and-array",
			in:     `{"z":[1,2],"y":{"k":"v"}}`,
			value:  map[string]any{"z": []any{float64(1), float64(2)}, "y": map[string]any{"k": "v"}},
			golden: "{\n  \"y\": {\n    \"k\": \"v\"\n  },\n  \"z\": [\n    1,\n    2\n  ]\n}",
		},
		{
			name:   "no-html-escape",
			in:     `{"cmd":"a && b < c"}`,
			value:  map[string]any{"cmd": "a && b < c"},
			golden: "{\n  \"cmd\": \"a && b < c\"\n}",
		},
		{
			name:   "empty-object",
			in:     `{}`,
			value:  map[string]any{},
			golden: "{}",
		},
		{
			name:   "top-level-array",
			in:     `["a","b"]`,
			value:  []any{"a", "b"},
			golden: "[\n  \"a\",\n  \"b\"\n]",
		},
	}
	runRoundTrips(t, JSON{}, cases)
}

func TestJSONDecodeError(t *testing.T) {
	if _, err := (JSON{}).Decode([]byte(`{bad`)); err == nil {
		t.Error("Decode of malformed JSON: expected error")
	}
}

func TestTOML(t *testing.T) {
	cases := []roundTrip{
		{
			name: "scalars",
			in:   "b = true\na = \"x\"\nn = 3\nf = 1.5\n",
			value: map[string]any{
				"a": "x", "b": true, "n": int64(3), "f": 1.5,
			},
			golden: "a = \"x\"\nb = true\nf = 1.5\nn = 3\n",
		},
		{
			name: "scalar-array",
			in:   "tools = [\"go\", \"node\"]\n",
			value: map[string]any{
				"tools": []any{"go", "node"},
			},
			golden: "tools = [\"go\", \"node\"]\n",
		},
		{
			name: "nested-table",
			in:   "[env]\nPATH = \"/bin\"\n",
			value: map[string]any{
				"env": map[string]any{"PATH": "/bin"},
			},
			golden: "\n[env]\nPATH = \"/bin\"\n",
		},
		{
			name: "array-of-tables",
			in:   "[[server]]\nname = \"a\"\n[[server]]\nname = \"b\"\n",
			value: map[string]any{
				"server": []any{
					map[string]any{"name": "a"},
					map[string]any{"name": "b"},
				},
			},
			golden: "\n[[server]]\nname = \"a\"\n\n[[server]]\nname = \"b\"\n",
		},
		{
			name: "leaf-before-table-ordering",
			in:   "z = 1\n[a]\nk = \"v\"\n",
			value: map[string]any{
				"z": int64(1),
				"a": map[string]any{"k": "v"},
			},
			golden: "z = 1\n\n[a]\nk = \"v\"\n",
		},
	}
	runRoundTrips(t, TOML{}, cases)
}

func TestTOMLEncodeErrors(t *testing.T) {
	if _, err := (TOML{}).Encode([]any{"not", "a", "table"}); err == nil {
		t.Error("Encode of non-table top level: expected error")
	}
	if _, err := (TOML{}).Encode(map[string]any{"k": nil}); err == nil {
		t.Error("Encode of nil scalar: expected error (TOML has no null)")
	}
}

func TestTOMLDecodeError(t *testing.T) {
	if _, err := (TOML{}).Decode([]byte("a = = 1")); err == nil {
		t.Error("Decode of malformed TOML: expected error")
	}
}

func TestLines(t *testing.T) {
	cases := []roundTrip{
		{
			name:   "basic",
			in:     "a\nb\nc\n",
			value:  []any{"a", "b", "c"},
			golden: "a\nb\nc\n",
		},
		{
			name:   "no-trailing-newline-input",
			in:     "a\nb",
			value:  []any{"a", "b"},
			golden: "a\nb\n",
		},
		{
			name:   "empty",
			in:     "",
			value:  []any{},
			golden: "",
		},
		{
			name:   "lone-newline",
			in:     "\n",
			value:  []any{},
			golden: "",
		},
		{
			name:   "interior-blank-line",
			in:     "a\n\nb\n",
			value:  []any{"a", "", "b"},
			golden: "a\n\nb\n",
		},
	}
	runRoundTrips(t, Lines{}, cases)
}

func TestLinesCRLF(t *testing.T) {
	// A CRLF line's trailing \r is stripped so the value is clean; re-encode is
	// LF-only (canonical form).
	got, err := (Lines{}).Decode([]byte("a\r\nb\r\n"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := []any{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode CRLF = %#v, want %#v", got, want)
	}
}

func TestLinesEncodeError(t *testing.T) {
	if _, err := (Lines{}).Encode("not-a-slice"); err == nil {
		t.Error("Encode of non-slice: expected error")
	}
	if _, err := (Lines{}).Encode([]any{1, 2}); err == nil {
		t.Error("Encode of non-string elements: expected error")
	}
}

func TestRaw(t *testing.T) {
	cases := []roundTrip{
		{name: "text", in: "hello\nworld", value: "hello\nworld", golden: "hello\nworld"},
		{name: "empty", in: "", value: "", golden: ""},
		{name: "binary-ish", in: "\x00\x01<>&", value: "\x00\x01<>&", golden: "\x00\x01<>&"},
	}
	runRoundTrips(t, Raw{}, cases)
}

func TestRawEncodeError(t *testing.T) {
	if _, err := (Raw{}).Encode(42); err == nil {
		t.Error("Encode of non-string: expected error")
	}
}
