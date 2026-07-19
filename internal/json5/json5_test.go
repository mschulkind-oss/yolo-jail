package json5

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestDecodeGolden pins hand-picked cases (the hard-requirement features).
func TestDecodeGolden(t *testing.T) {
	cases := []struct {
		in   string
		want string // jsonx.DumpsCompact of the decoded value
	}{
		{`{}`, `{}`},
		{`{"a":1}`, `{"a": 1}`},
		{"// c\n{\"a\":1}", `{"a": 1}`},
		{`/* b */ {"a":1}`, `{"a": 1}`},
		{`{"a":1,}`, `{"a": 1}`},      // trailing comma
		{`[1,2,3,]`, `[1, 2, 3]`},     // trailing comma
		{`{'s':'q'}`, `{"s": "q"}`},   // single quotes
		{`{unq: 1}`, `{"unq": 1}`},    // unquoted key
		{`{"h": 0xff}`, `{"h": 255}`}, // hex
		{`{"p": +5}`, `{"p": 5}`},     // leading plus
		{`{"d": .5}`, `{"d": 0.5}`},   // leading dot
		{`{"t": 5.}`, `{"t": 5.0}`},   // trailing dot
	}
	for _, tc := range cases {
		v, err := Decode([]byte(tc.in))
		if err != nil {
			t.Errorf("Decode(%q) error: %v", tc.in, err)
			continue
		}
		got, err := jsonx.DumpsCompact(v)
		if err != nil {
			t.Errorf("DumpsCompact(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Decode(%q) -> %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDecodeRejectsMalformed(t *testing.T) {
	for _, in := range []string{`{"a": 1`, `[1 2 3]`, `{a b}`, `nul`, ``, `{} trailing`} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("Decode(%q) should have errored", in)
		}
	}
}
