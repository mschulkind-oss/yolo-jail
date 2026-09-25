package codec

import (
	"math"
	"reflect"
	"testing"
)

// TestLookupCodec checks the registry resolves the documented names and
// rejects anything else, and that each resolved codec reports its own name.
func TestLookupCodec(t *testing.T) {
	for _, name := range []string{"json", "toml", "yaml", "lines", "raw"} {
		c, ok := LookupCodec(name)
		if !ok {
			t.Fatalf("LookupCodec(%q): not found", name)
		}
		if c.Name() != name {
			t.Errorf("LookupCodec(%q).Name() = %q", name, c.Name())
		}
	}
	if _, ok := LookupCodec(""); ok {
		t.Error("LookupCodec(\"\"): unexpectedly found")
	}
}

// TestNames pins Names() to the registry's actual contents. manifest derives its
// accepted-name set from this, so anything Names() reports must be resolvable by
// LookupCodec — that biconditional is the whole point of deriving it.
func TestNames(t *testing.T) {
	got := Names()
	if want := []string{"json", "lines", "raw", "toml", "yaml"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v (sorted)", got, want)
	}
	for _, n := range got {
		if _, ok := LookupCodec(n); !ok {
			t.Errorf("Names() reports %q but LookupCodec(%q) fails", n, n)
		}
	}
	if len(got) != len(registry) {
		t.Errorf("Names() has %d entries, registry has %d", len(got), len(registry))
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

// TestKindOf pins each codec's decoded shape, and that every registered codec
// has one. A codec without a Kind would silently compose as an object and take
// the deep-merge path, which is wrong for anything without keys.
func TestKindOf(t *testing.T) {
	want := map[string]Kind{
		"json":  KindObject,
		"toml":  KindObject,
		"yaml":  KindObject,
		"lines": KindArray,
		"raw":   KindScalar,
	}
	for _, name := range Names() {
		got, ok := KindOf(name)
		if !ok {
			t.Errorf("KindOf(%q): no kind registered for an implemented codec", name)
			continue
		}
		if got != want[name] {
			t.Errorf("KindOf(%q) = %v, want %v", name, got, want[name])
		}
	}
	if len(kinds) != len(registry) {
		t.Errorf("kinds has %d entries, registry has %d — every codec needs a Kind", len(kinds), len(registry))
	}
}

// TestKindDecodeAgreement is the real contract: KindOf(name) must describe what
// that codec's Decode actually produces. Asserted against genuinely decodable
// input per codec, so a future codec whose Kind is mislabeled fails here.
func TestKindDecodeAgreement(t *testing.T) {
	inputs := map[string]string{
		"json":  `{"a": 1}`,
		"toml":  "a = 1\n",
		"yaml":  "a: 1\n",
		"lines": "one\ntwo\n",
		"raw":   "anything at all\n",
	}
	for _, name := range Names() {
		c, ok := LookupCodec(name)
		if !ok {
			t.Fatalf("LookupCodec(%q): not found", name)
		}
		in, have := inputs[name]
		if !have {
			t.Fatalf("no test input for codec %q — add one", name)
		}
		v, err := c.Decode([]byte(in))
		if err != nil {
			t.Fatalf("%s.Decode(%q): %v", name, in, err)
		}
		k, _ := KindOf(name)
		if !k.Matches(v) {
			t.Errorf("%s: KindOf = %v but Decode produced %T", name, k, v)
		}
		if !k.Matches(k.ZeroValue()) {
			t.Errorf("%s: ZeroValue %#v does not match its own kind %v", name, k.ZeroValue(), k)
		}
	}
}

// TestTOMLNonFiniteFloats: TOML spells the non-finite floats `inf`, `+inf`, `-inf` and
// `nan`, and nothing else. The scalar encoder used to append `.0` to any FormatFloat
// result with no `.eE` in it, so a surface file holding `infinite = inf` decoded to +Inf
// and was written back as `infinite = +Inf.0` — invalid TOML, which the next `own` render
// then refused to rewrite (docs/design/config-ownership-and-promotion.md §11, live residue
// item 6). Round-tripped through Decode on purpose: the bytes must be TOML this codec can
// read back, not merely a string the test agrees with.
func TestTOMLNonFiniteFloats(t *testing.T) {
	for _, tc := range []struct {
		in     string
		golden string
		check  func(float64) bool
	}{
		{"a = inf\n", "a = inf\n", func(f float64) bool { return math.IsInf(f, 1) }},
		{"a = +inf\n", "a = inf\n", func(f float64) bool { return math.IsInf(f, 1) }},
		{"a = -inf\n", "a = -inf\n", func(f float64) bool { return math.IsInf(f, -1) }},
		{"a = nan\n", "a = nan\n", math.IsNaN},
		{"a = -nan\n", "a = nan\n", math.IsNaN},
	} {
		t.Run(tc.in, func(t *testing.T) {
			v, err := (TOML{}).Decode([]byte(tc.in))
			if err != nil {
				t.Fatalf("Decode(%q): %v", tc.in, err)
			}
			f, ok := v.(map[string]any)["a"].(float64)
			if !ok || !tc.check(f) {
				t.Fatalf("Decode(%q) = %#v, want the non-finite float", tc.in, v)
			}
			enc, err := (TOML{}).Encode(v)
			if err != nil {
				t.Fatalf("Encode(%#v): %v", v, err)
			}
			if string(enc) != tc.golden {
				t.Errorf("Encode = %q, want %q (TOML has no `+Inf.0` or `NaN.0`)", enc, tc.golden)
			}
			back, err := (TOML{}).Decode(enc)
			if err != nil {
				t.Fatalf("the encoded bytes %q are not valid TOML: %v", enc, err)
			}
			if f, ok := back.(map[string]any)["a"].(float64); !ok || !tc.check(f) {
				t.Errorf("Decode(Encode(v)) = %#v, want the same non-finite float", back)
			}
		})
	}
	// The same rule inside an array, which goes through the same scalar encoder.
	enc, err := (TOML{}).Encode(map[string]any{"xs": []any{math.Inf(1), math.Inf(-1), 1.5}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "xs = [inf, -inf, 1.5]\n"; string(enc) != want {
		t.Errorf("Encode(array) = %q, want %q", enc, want)
	}
}
