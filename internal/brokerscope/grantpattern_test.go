package brokerscope

import (
	"os"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Wildcard branch and tag grants (docs/design/git-broker.md GB-D25): a grant's name is git's own
// refspec pattern, one `*` anywhere in the short name matching any run of characters, `/`
// included; its repository may be "*"; and a `tag` grant mirrors `branch` under refs/tags/.

func TestValidGrantRefPattern(t *testing.T) {
	for _, ok := range []string{"main", "release/1.2", "*", "patch/*", "patch*", "v*", "a*b", "*/x", "über*"} {
		if !ValidGrantRefPattern(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "**", "*/*", "a*b*", "refs/heads/*", "*.lock", ".*", "-*", "a..*",
		"a b*", "a?*", "a[*", "*/", "/*", "a//*", "HEAD"} {
		if ValidGrantRefPattern(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRefPatternMatches(t *testing.T) {
	for _, c := range []struct {
		pattern, name string
		want          bool
	}{
		{"main", "main", true},
		{"main", "main2", false},
		{"*", "main", true},
		{"*", "agent/deep/x", true},
		{"patch/*", "patch/one", true},
		{"patch/*", "patch/one/two", true},
		{"patch/*", "patch", false},
		{"patch/*", "patches/one", false},
		{"patch*", "patches/one", true},
		{"v*", "v1.2.3", true},
		{"v*", "release-v1", false},
		{"a*b", "a/x/b", true},
		{"a*b", "ab", true},
		{"a*b", "a", false},
	} {
		if got := RefPatternMatches(c.pattern, c.name); got != c.want {
			t.Errorf("RefPatternMatches(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

// A branch grant may name a pattern and "*" for its repository; a tag grant names its tag the
// same way, in its own field, and spells and parses back like a branch grant.
func TestWildcardAndTagGrantsAreValid(t *testing.T) {
	for _, g := range []Grant{
		{Kind: "branch", Repo: AnyRepo, Branch: "main"},
		{Kind: "branch", Repo: "github.com/o/r", Branch: "patch/*"},
		{Kind: "branch", Repo: AnyRepo, Branch: "*"},
		{Kind: "tag", Repo: "github.com/o/r", Tag: "v*"},
		{Kind: "tag", Repo: AnyRepo, Tag: "v1.0"},
	} {
		if !ValidGrant(g) {
			t.Errorf("%+v refused", g)
		}
		if back, ok := ParseGrant(g.String()); !ok || back != g {
			t.Errorf("ParseGrant(%q) = %+v, %v", g.String(), back, ok)
		}
	}
	if got := (Grant{Kind: "tag", Repo: "github.com/o/r", Tag: "v*"}).String(); got != "tag github.com/o/r v*" {
		t.Errorf("String() = %q", got)
	}
	for _, bad := range []Grant{
		{Kind: "tag", Repo: "github.com/o/r"},
		{Kind: "tag", Repo: "github.com/o/r", Branch: "v1"},
		{Kind: "branch", Repo: "github.com/o/r", Tag: "v1"},
		{Kind: "branch", Repo: "github.com/o/r", Branch: "a*b*"},
		{Kind: "create-branch", Repo: "github.com/o/r", Tag: "v1"},
	} {
		if ValidGrant(bad) {
			t.Errorf("%+v accepted", bad)
		}
	}
}

// The scope file takes the new forms: a launch's write of a wildcard branch grant and a tag
// grant succeeds and reads back.
func TestScopeFileCarriesWildcardAndTagGrants(t *testing.T) {
	t.Setenv("HOME", resolvedTempDir(t))
	grants := []Grant{{Kind: "branch", Repo: AnyRepo, Branch: "patch/*"}, {Kind: "tag", Repo: "github.com/o/r", Tag: "v*"}}
	path, err := Write(File{Source: "git", LaunchID: "patterns", PID: os.Getpid(), Grants: grants})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := ReadFile(path)
	if err != nil || len(got.Grants) != 2 || got.Grants[0] != grants[0] || got.Grants[1] != grants[1] {
		t.Fatalf("read back %+v, %v", got.Grants, err)
	}
	if _, err := Write(File{Source: "git", LaunchID: "twostars", PID: os.Getpid(),
		Grants: []Grant{{Kind: "branch", Repo: AnyRepo, Branch: "*/*"}}}); err == nil {
		t.Fatal("a two-star pattern was written")
	}
	if _, err := os.Stat(paths.BrokerScopeFile("git", "twostars")); !os.IsNotExist(err) {
		t.Fatalf("a refused write left a scope file (err %v)", err)
	}
}

// A grant's note says in plain words what a wildcard admits; a grant naming one ref on one
// repository needs none.
func TestGrantNote(t *testing.T) {
	for _, c := range []struct {
		g    Grant
		want string
	}{
		{Grant{Kind: "branch", Repo: "github.com/o/r", Branch: "patch/*"},
			"may create, update, force-push and delete branches matching `patch/*` on github.com/o/r"},
		{Grant{Kind: "branch", Repo: AnyRepo, Branch: "main"},
			"may create, update, force-push and delete `main` on every remote in scope now and any approved later"},
		{Grant{Kind: "tag", Repo: AnyRepo, Tag: "v*"},
			"may create, update, force-push and delete tags matching `v*` on every remote in scope now and any approved later"},
		{Grant{Kind: "branch", Repo: "github.com/o/r", Branch: "main"}, ""},
		{Grant{Kind: "create-branch", Repo: AnyRepo}, ""},
		{Grant{Kind: KindDispatchWorkflow, Repo: "github.com/o/r", Workflow: AnyWorkflow},
			"may run every workflow, including ones added to the default branch later, on the default branch of github.com/o/r, unasked"},
		{Grant{Kind: KindDispatchWorkflow, Repo: AnyRepo, Workflow: "nightly.yml"},
			"may run `nightly.yml` on the default branch of every repository in scope now and any approved later, unasked"},
		{Grant{Kind: KindDispatchWorkflow, Repo: "github.com/o/r", Workflow: "nightly.yml"}, ""},
	} {
		if got := GrantNote(c.g); got != c.want {
			t.Errorf("GrantNote(%s) = %q, want %q", c.g, got, c.want)
		}
	}
}
