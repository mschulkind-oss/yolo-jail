package packsrc

import (
	"strings"
	"testing"
)

func TestParseGitAddresses(t *testing.T) {
	for _, tc := range []struct {
		raw, repo, path, ref string
	}{
		{"git+ssh://git@github.com/acme/mono//tools/agent-pack?ref=alice/rust-review",
			"ssh://git@github.com/acme/mono", "tools/agent-pack", "alice/rust-review"},
		{"git+https://gitlab.acme.internal/eng/mono//agents/pack?ref=v2.1.0",
			"https://gitlab.acme.internal/eng/mono", "agents/pack", "v2.1.0"},
		// No subdirectory: the pack is the repo root.
		{"git+https://github.com/acme/pack?ref=main",
			"https://github.com/acme/pack", "", "main"},
		// A full SHA is a legitimate ref, and the most pinned form.
		{"git+https://github.com/acme/mono//p?ref=6461be617ca2670db07dabc4d84707aed18e5fa9",
			"https://github.com/acme/mono", "p", "6461be617ca2670db07dabc4d84707aed18e5fa9"},
	} {
		got, err := Parse(tc.raw)
		if err != nil {
			t.Errorf("Parse(%q) errored: %v", tc.raw, err)
			continue
		}
		if got.Kind != KindGit {
			t.Errorf("%q: kind = %v, want git", tc.raw, got.Kind)
		}
		if got.Repo != tc.repo || got.Path != tc.path || got.Ref != tc.ref {
			t.Errorf("Parse(%q) = {repo:%q path:%q ref:%q}, want {%q %q %q}",
				tc.raw, got.Repo, got.Path, got.Ref, tc.repo, tc.path, tc.ref)
		}
	}
}

// Normalization matters because CacheKey is a store path and a lockfile identity:
// two spellings of one repo must not fetch and store twice.
func TestParseNormalizesRepo(t *testing.T) {
	a, err := Parse("git+https://github.com/acme/mono.git//p?ref=main")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse("git+https://github.com/acme/mono//p?ref=main")
	if err != nil {
		t.Fatal(err)
	}
	if a.Repo != b.Repo {
		t.Errorf("trailing .git not normalized: %q vs %q", a.Repo, b.Repo)
	}
	if a.CacheKey() != b.CacheKey() {
		t.Errorf("cache keys differ for the same repo: %q vs %q", a.CacheKey(), b.CacheKey())
	}
}

// ref is MANDATORY for git. An unpinned source silently changes under you, which is
// the top-ranked anti-pattern in the precedent survey — following a branch has to be
// asked for by name, not acquired by omission.
func TestParseRequiresRefForGit(t *testing.T) {
	for _, raw := range []string{
		"git+https://github.com/acme/pack",
		"git+https://github.com/acme/pack//sub",
		"git+https://github.com/acme/pack//sub?ref=",
	} {
		_, err := Parse(raw)
		if err == nil {
			t.Errorf("Parse(%q) should require a ref", raw)
			continue
		}
		if !strings.Contains(err.Error(), "ref") {
			t.Errorf("Parse(%q) error should mention ref: %v", raw, err)
		}
	}
}

func TestParseFileAddresses(t *testing.T) {
	a, err := Parse("file:///home/me/code/acme/tools/agent-pack")
	if err != nil {
		t.Fatal(err)
	}
	if !a.IsLocal() {
		t.Error("file:// should be local")
	}
	if a.Path != "/home/me/code/acme/tools/agent-pack" {
		t.Errorf("path = %q", a.Path)
	}
	// A local pack takes no ref — it is whatever is on disk, which is the point of
	// using one while authoring.
	if a.Ref != "" {
		t.Errorf("file:// should carry no ref, got %q", a.Ref)
	}
}

// A query on file:// is almost always a copy-paste from a git address. Silently
// ignoring it would leave the user thinking they pinned something.
func TestParseFileRejectsQuery(t *testing.T) {
	_, err := Parse("file:///home/me/pack?ref=main")
	if err == nil {
		t.Fatal("expected an error for a query on file://")
	}
	if !strings.Contains(err.Error(), "no query") {
		t.Errorf("error should explain: %v", err)
	}
}

// Traversal and git range-syntax guards. `..` in a subpath escapes the pack; `..` in
// a REF is git's revision-range syntax, which would check out something the address
// does not name.
func TestParseRejectsDotDot(t *testing.T) {
	for _, raw := range []string{
		"git+https://h/o/r//../../etc?ref=main",
		"git+https://h/o/r//p?ref=main..evil",
		"file:///home/me/../../etc/passwd",
	} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("Parse(%q) should reject '..'", raw)
		}
	}
}

// A ref is interpolated into git argv, so one that reads as an option must be
// refused here where the message can say so.
func TestParseRejectsOptionLikeRef(t *testing.T) {
	if _, err := Parse("git+https://h/o/r//p?ref=--upload-pack=evil"); err == nil {
		t.Error("a ref starting with '-' must be rejected")
	}
}

func TestParseRejectsBadSchemesAndQueries(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"", "empty"},
		{"/no/scheme", "no scheme"},
		{"http://example.com/pack", "unsupported scheme"},
		{"git+ftp://h/o/r//p?ref=main", "unsupported git transport"},
		{"git+https://h/o/r//p?ref=main&depth=1", "unknown query parameter"},
		{"git+https://?ref=main", "missing host/repository"},
	} {
		_, err := Parse(tc.raw)
		if err == nil {
			t.Errorf("Parse(%q) should fail", tc.raw)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%q) error %q missing %q", tc.raw, err, tc.want)
		}
	}
}

// CacheKey must distinguish sources that differ only by ref or subpath, or a
// checkout would be reused for the wrong content.
func TestCacheKeyDistinguishesRefAndPath(t *testing.T) {
	base := "git+https://github.com/acme/mono"
	keys := map[string]bool{}
	for _, raw := range []string{
		base + "//a?ref=main",
		base + "//a?ref=v2",
		base + "//b?ref=main",
	} {
		a, err := Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if keys[a.CacheKey()] {
			t.Errorf("cache key collision for %q: %q", raw, a.CacheKey())
		}
		keys[a.CacheKey()] = true
	}
}
