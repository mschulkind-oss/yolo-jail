package version

import (
	"path/filepath"
	"testing"
)

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

// TestBuildVersionPrecedence pins the release-stamp contract (§2d of the
// distribution work): a binary stamped via -ldflags -X must report ITS OWN
// version even when invoked from inside a git repository — `git describe
// --always` succeeds in ANY repo, so describe-first would report the user's
// cwd repo, not the binary. These tests run inside the yolo-jail checkout,
// so live git describe IS available: buildVersion winning proves the order.
func TestBuildVersionPrecedence(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	orig := buildVersion
	defer func() { buildVersion = orig }()

	buildVersion = "v9.9.9-2-gfeedbee"
	if got, want := gitDescribe(""), "9.9.9+2.gfeedbee"; got != want {
		t.Errorf("stamped binary: gitDescribe() = %q, want normalized stamp %q", got, want)
	}

	// YOLO_VERSION still beats the stamp (in-jail banner parity contract) and
	// is returned VERBATIM, never normalized.
	t.Setenv("YOLO_VERSION", "0.1.0-dirty")
	if got, want := gitDescribe(""), "0.1.0-dirty"; got != want {
		t.Errorf("YOLO_VERSION over stamp: gitDescribe() = %q, want %q", got, want)
	}
	t.Setenv("YOLO_VERSION", "")

	// A literal "unknown" stamp (pre-fix scripts/build-go.sh stamped this on
	// describe failure) must NOT shadow live git describe in a known repo
	// root (this checkout).
	buildVersion = "unknown"
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if got := gitDescribe(repo); got == "unknown" || got == "" {
		t.Errorf("legacy 'unknown' stamp shadowed live describe: gitDescribe(repo) = %q", got)
	}

	// Unstamped + no repo root: NEVER describe the process cwd (it could be
	// any repo the user is standing in — the tests themselves run inside the
	// yolo-jail checkout, so a cwd describe would return a version here).
	buildVersion = ""
	if got := gitDescribe(""); got != "" {
		t.Errorf("unstamped no-root binary described the cwd: gitDescribe(\"\") = %q, want \"\"", got)
	}
	if got := Get(""); got != "unknown" {
		t.Errorf("Get(\"\") = %q, want \"unknown\"", got)
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
