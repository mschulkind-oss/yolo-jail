package cli

import (
	"reflect"
	"testing"
)

// commentTexts returns the verbatim text of every span jsoncCommentSpans found — the
// least error-prone thing to assert exact boundaries against.
func commentTexts(text string) []string {
	var out []string
	for _, s := range jsoncCommentSpans(text) {
		out = append(out, text[s[0]:s[1]])
	}
	return out
}

func TestJSONCCommentSpans(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"no comments", `{"a": true}`, nil},
		{"line comment to newline", "// hi\n{\"a\": true}", []string{"// hi"}},
		{"trailing line comment", "{\"a\": true} // hi", []string{"// hi"}},
		{"line comment at EOF", "{\"a\": true} // hi", []string{"// hi"}},
		{"block comment", "{\"a\" /* x */: true}", []string{"/* x */"}},
		{"unterminated block comment", "{\"a\": /* x", []string{"/* x"}},
		{"two comments", "// a\n{\"b\": 1} /* c */", []string{"// a", "/* c */"}},
		{
			"slashes inside a string are not a comment",
			`{"url": "https://example//x", "a": true}`,
			nil,
		},
		{
			"escaped quote does not end the string",
			`{"q": "a\"//b", // real comment`,
			[]string{"// real comment"},
		},
		{
			"commented-out key is a comment, the live key is not",
			"// \"host_wrappers\": false,\n{\"host_wrappers\": true}",
			[]string{"// \"host_wrappers\": false,"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commentTexts(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("comments = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCoveredByIsHalfOpen pins the boundary: a position at a span's end is OUTSIDE it —
// setJSONCBool relies on that for a key that starts exactly after a block comment.
func TestCoveredByIsHalfOpen(t *testing.T) {
	spans := jsoncCommentSpans("a /* x */b")
	// indices:      0123456789 — the comment occupies [2,9), 'b' is at 9.
	if !coveredBy(spans, 2) {
		t.Error("the comment's first byte must be covered")
	}
	if !coveredBy(spans, 8) {
		t.Error("the comment's last byte must be covered")
	}
	if coveredBy(spans, 9) {
		t.Error("the byte after the comment must NOT be covered")
	}
}
