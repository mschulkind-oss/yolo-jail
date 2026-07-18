package version

import "testing"

// normalizeCases pins the byte contract. Values are the observed Python
// output of src/cli/version.py:_git_describe_version's normalization tail
// (see the cross-language test for the live oracle).
var normalizeCases = []struct {
	raw  string
	want string
}{
	{"0.1.0", "0.1.0"},                                   // exactly on tag
	{"v0.1.0", "0.1.0"},                                  // leading v stripped
	{"0.1.0-dirty", "0.1.0+dirty"},                       // dirty on tag — WITH the +
	{"v0.1.0-dirty", "0.1.0+dirty"},                      // ...and with a leading v
	{"0.1.0-3-gabcdef1", "0.1.0+3.gabcdef1"},             // commits past tag
	{"0.1.0-3-gabcdef1-dirty", "0.1.0+3.gabcdef1.dirty"}, // commits + dirty
	{"v0.6.0-19-g661ac98", "0.6.0+19.g661ac98"},          // a real describe from this repo
	{"1.2.3-rc1", "1.2.3-rc1"},                           // hyphenated base, no g-hash: base rejoined
	{"deadbeef", "deadbeef"},                             // --always fallback (no tag)
	{"deadbeef-dirty", "deadbeef+dirty"},                 // ...dirty
}

func TestNormalize(t *testing.T) {
	for _, tc := range normalizeCases {
		if got := Normalize(tc.raw); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestIsDigits(t *testing.T) {
	cases := map[string]bool{
		"":     false,
		"0":    true,
		"19":   true,
		"g123": false,
		"1a":   false,
		"-1":   false,
	}
	for in, want := range cases {
		if got := isDigits(in); got != want {
			t.Errorf("isDigits(%q) = %v, want %v", in, got, want)
		}
	}
}
