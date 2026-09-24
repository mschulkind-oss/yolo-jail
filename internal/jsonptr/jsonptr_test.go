package jsonptr

import (
	"reflect"
	"strings"
	"testing"
)

// RFC 6901 §5's own examples, which are the grammar's reference cases: every one parses to
// the key it names and formats back to the exact string it came from.
func TestParseRFC6901Examples(t *testing.T) {
	cases := []struct {
		ptr  string
		want []string
	}{
		{"", nil},
		{"/foo", []string{"foo"}},
		{"/foo/0", []string{"foo", "0"}},
		{"/", []string{""}},
		{"/a~1b", []string{"a/b"}},
		{"/c%d", []string{"c%d"}},
		{"/e^f", []string{"e^f"}},
		{"/g|h", []string{"g|h"}},
		{`/i\j`, []string{`i\j`}},
		{`/k"l`, []string{`k"l`}},
		{"/ ", []string{" "}},
		{"/m~0n", []string{"m~n"}},
		// Not in §5, but the reason a pointer and not a dotted path: a dot is a key byte.
		{"/models/glm-5.3", []string{"models", "glm-5.3"}},
		{"/a//b", []string{"a", "", "b"}},
	}
	for _, tc := range cases {
		got, err := Parse(tc.ptr)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tc.ptr, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Parse(%q) = %#v, want %#v", tc.ptr, got, tc.want)
		}
		if back := Format(got); back != tc.ptr {
			t.Errorf("Format(Parse(%q)) = %q — the round trip must be exact", tc.ptr, back)
		}
	}
}

// Unescaping order is the RFC's (§4): "~1" first, then "~0". The wrong order turns "~01"
// into "/" — a pointer naming a different key than its author wrote.
func TestUnescapeOrder(t *testing.T) {
	got, err := Parse("/~01")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"~1"}) {
		t.Errorf("Parse(\"/~01\") = %#v, want [\"~1\"] (unescape ~1 before ~0)", got)
	}
	if f := Format([]string{"~1"}); f != "/~01" {
		t.Errorf("Format([\"~1\"]) = %q, want \"/~01\" (escape ~ before /)", f)
	}
	if f := Format([]string{"a/b~c"}); f != "/a~1b~0c" {
		t.Errorf("Format([\"a/b~c\"]) = %q, want \"/a~1b~0c\"", f)
	}
}

// A pointer is refused, not guessed at, when it is not RFC 6901: no leading "/", a bare
// trailing "~", or an escape the RFC does not define. Each error names the pointer.
func TestParseRefusesMalformed(t *testing.T) {
	for _, p := range []string{"packages", "packages/x", "/~", "/a~", "/~2", "/a~b", "/ok/~x"} {
		_, err := Parse(p)
		if err == nil {
			t.Errorf("Parse(%q) accepted a malformed pointer", p)
			continue
		}
		if !strings.Contains(err.Error(), p) {
			t.Errorf("Parse(%q) error %q does not name the pointer", p, err)
		}
	}
}

func TestFormatEmptyIsRoot(t *testing.T) {
	if got := Format(nil); got != "" {
		t.Errorf("Format(nil) = %q, want \"\" (the whole-document pointer)", got)
	}
}
