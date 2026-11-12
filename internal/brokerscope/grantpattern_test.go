package brokerscope

import (
	"encoding/json"
	"os"
	"regexp"
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
		{Grant{Kind: KindRuns, Repo: "github.com/o/r", Workflow: AnyWorkflow},
			"may run and re-run every workflow, including ones added to the default branch later, on the default branch of github.com/o/r, and cancel any of its runs, unasked"},
		{Grant{Kind: KindRuns, Repo: AnyRepo, Workflow: "nightly.yml"},
			"may run and re-run `nightly.yml` on the default branch of every repository in scope now and any approved later, and cancel any of its runs, unasked"},
		{Grant{Kind: KindRuns, Repo: "github.com/o/r", Workflow: "nightly.yml"}, ""},
	} {
		if got := GrantNote(c.g); got != c.want {
			t.Errorf("GrantNote(%s) = %q, want %q", c.g, got, c.want)
		}
	}
}

// A marked grant (RepoPattern) names a pattern repository: `<host>/<owner>/<repo>` with a named
// host, and an owner, a repository or both a whole `*` (GW-D80). Its String and ParseGrant
// spelling carries the `pattern:` prefix; every kind's name is spelled as it is for an unmarked grant.
func TestPatternGrantShapes(t *testing.T) {
	for _, g := range []Grant{
		{Kind: KindRead, Repo: "github.com/acme/*", RepoPattern: true},
		{Kind: KindRead, Repo: "github.com/*/*", RepoPattern: true},
		{Kind: KindRead, Repo: "git.example.com:2222/*/app", RepoPattern: true},
		{Kind: KindRuns, Repo: "github.com/acme/*", Workflow: "ci.yml", RepoPattern: true},
		{Kind: KindRuns, Repo: "github.com/acme/*", Workflow: AnyWorkflow, RepoPattern: true},
		{Kind: KindApproveRun, Repo: "github.com/*/lint", Actor: "octocat", RepoPattern: true},
		{Kind: KindBranch, Repo: "github.com/acme/*", Branch: "patch/*", RepoPattern: true},
		{Kind: KindTag, Repo: "github.com/acme/*", Tag: "v*", RepoPattern: true},
	} {
		if !ValidGrant(g) {
			t.Errorf("%+v refused", g)
			continue
		}
		if back, ok := ParseGrant(g.String()); !ok || back != g {
			t.Errorf("ParseGrant(%q) = %+v, %v, want %+v", g.String(), back, ok, g)
		}
	}
	for _, bad := range []Grant{
		{Kind: KindRead, Repo: "*", RepoPattern: true},
		{Kind: KindRead, Repo: "*/o/r", RepoPattern: true},
		{Kind: KindRead, Repo: "github.com/o/r", RepoPattern: true},
		{Kind: KindRead, Repo: "github.com/o*/*", RepoPattern: true},
		{Kind: KindRead, Repo: "github.com/*o/r", RepoPattern: true},
		{Kind: KindRead, Repo: "github.com/o/r*", RepoPattern: true},
		{Kind: KindRead, Repo: "github.com/o/*.git", RepoPattern: true},
		{Kind: KindRead, Repo: "github.com/*", RepoPattern: true},
		{Kind: KindRead, Repo: "github.com/*/*/x", RepoPattern: true},
		{Kind: KindRead, Repo: "o/*", RepoPattern: true},
		{Kind: KindRead, Repo: "pattern:github.com/o/*", RepoPattern: true},
		{Kind: KindRuns, Repo: "github.com/acme/*", RepoPattern: true},
	} {
		if ValidGrant(bad) {
			t.Errorf("%+v accepted", bad)
		}
		if _, ok := ParseGrant(bad.String()); ok {
			t.Errorf("ParseGrant(%q) accepted", bad.String())
		}
	}
	for _, bad := range []string{"read pattern:*", "read pattern:*/o/r", "read pattern:github.com/o/r",
		"read pattern:github.com/o*/r", "read pattern:pattern:github.com/o/*", "read pattern:",
		"runs pattern:github.com/acme/*"} {
		if _, ok := ParseGrant(bad); ok {
			t.Errorf("ParseGrant(%q) accepted", bad)
		}
	}
}

// The pattern prefix is no repository an older gate could have recorded: a literal `pattern:`
// repository is refused, marked or not, so every record's spelling keeps one meaning.
func TestPatternPrefixIsNoRepository(t *testing.T) {
	if PatternRepoPrefix != "pattern:" {
		t.Fatalf("PatternRepoPrefix = %q", PatternRepoPrefix)
	}
	for _, s := range []string{"pattern:github.com/o/r", "pattern:github.com/o/*"} {
		if ValidGrantRepo(s) {
			t.Errorf("ValidGrantRepo(%q) accepted", s)
		}
	}
	if ValidGrant(Grant{Kind: KindRead, Repo: "pattern:github.com/o/*"}) {
		t.Error("an unmarked literal pattern-prefixed repository accepted")
	}
	if g, ok := ParseGrant("read pattern:github.com/o/r"); ok {
		t.Errorf("a marked exact repository parsed: %+v", g)
	}
}

// A grant written before RepoPattern keeps its exact meaning: a `*` in its repository is spelled
// and stored as it always was, and reads back unmarked.
func TestLegacyWildcardRepoGrantKeepsItsSpelling(t *testing.T) {
	g := Grant{Kind: KindRuns, Repo: "github.com/o/*", Workflow: AnyWorkflow}
	if !ValidGrant(g) {
		t.Fatalf("%+v refused", g)
	}
	if got := g.String(); got != "runs github.com/o/* *" {
		t.Errorf("String() = %q", got)
	}
	if back, ok := ParseGrant("runs github.com/o/* *"); !ok || back != g || back.RepoPattern {
		t.Errorf("ParseGrant = %+v, %v, want %+v unmarked", back, ok, g)
	}
	var decoded Grant
	if err := json.Unmarshal([]byte(`{"kind":"runs","repo":"github.com/o/*","workflow":"*"}`), &decoded); err != nil || decoded != g {
		t.Errorf("legacy JSON decoded to %+v, %v", decoded, err)
	}
	if b, err := json.Marshal(g); err != nil || string(b) != `{"kind":"runs","repo":"github.com/o/*","workflow":"*"}` {
		t.Errorf("legacy JSON = %s, %v", b, err)
	}
}

// A marked grant's JSON carries repo_pattern; an unmarked one's omits it.
func TestRepoPatternJSON(t *testing.T) {
	b, err := json.Marshal(Grant{Kind: KindRead, Repo: "github.com/acme/*", RepoPattern: true})
	if err != nil || string(b) != `{"kind":"read","repo":"github.com/acme/*","repo_pattern":true}` {
		t.Errorf("marked JSON = %s, %v", b, err)
	}
	var back Grant
	if err := json.Unmarshal(b, &back); err != nil || !back.RepoPattern || back.Repo != "github.com/acme/*" {
		t.Errorf("marked JSON decoded to %+v, %v", back, err)
	}
}

// A marked grant's String is the pattern-prefixed repository, then the kind's name as usual, and
// ParseGrant of it sets the flag back.
func TestPatternGrantString(t *testing.T) {
	for _, c := range []struct {
		g    Grant
		want string
	}{
		{Grant{Kind: KindRead, Repo: "github.com/acme/*", RepoPattern: true}, "read pattern:github.com/acme/*"},
		{Grant{Kind: KindRuns, Repo: "github.com/acme/*", Workflow: "ci.yml", RepoPattern: true}, "runs pattern:github.com/acme/* ci.yml"},
		{Grant{Kind: KindApproveRun, Repo: "github.com/*/lint", Actor: "octocat", RepoPattern: true}, "approve-run pattern:github.com/*/lint octocat"},
		{Grant{Kind: KindBranch, Repo: "github.com/acme/*", Branch: "patch/*", RepoPattern: true}, "branch pattern:github.com/acme/* patch/*"},
	} {
		if got := c.g.String(); got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
		if back, ok := ParseGrant(c.want); !ok || back != c.g || !back.RepoPattern {
			t.Errorf("ParseGrant(%q) = %+v, %v, want %+v", c.want, back, ok, c.g)
		}
	}
}

// A scope file carries RepoPattern as its own key: a marked grant reads back marked, and a grant
// with a `*` written before the field stays unmarked in the same file.
func TestScopeFileCarriesRepoPattern(t *testing.T) {
	t.Setenv("HOME", resolvedTempDir(t))
	grants := []Grant{
		{Kind: KindRead, Repo: "github.com/acme/*", RepoPattern: true},
		{Kind: KindRuns, Repo: "github.com/o/*", Workflow: AnyWorkflow},
		{Kind: KindRuns, Repo: "github.com/acme/*", Workflow: "ci.yml", RepoPattern: true},
		{Kind: KindApproveRun, Repo: "github.com/acme/r", Actor: "octocat"},
	}
	path, err := Write(File{Source: "git", LaunchID: "repopattern", PID: os.Getpid(), Grants: grants})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := ReadFile(path)
	if err != nil || len(got.Grants) != len(grants) {
		t.Fatalf("read back %+v, %v", got.Grants, err)
	}
	for i := range grants {
		if got.Grants[i] != grants[i] {
			t.Errorf("grant %d read back %+v, want %+v", i, got.Grants[i], grants[i])
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(regexp.MustCompile(`"repo_pattern":\s*true`).FindAll(raw, -1)); n != 2 {
		t.Errorf("scope file holds %d marked grants, want 2: %s", n, raw)
	}
	if _, err := Write(File{Source: "git", LaunchID: "repopatternbad", PID: os.Getpid(),
		Grants: []Grant{{Kind: KindRead, Repo: "*", RepoPattern: true}}}); err == nil {
		t.Fatal("a marked AnyRepo grant was written")
	}
}

// A marked grant's note says its reach in plain words after the grant's own note; a legacy
// unmarked grant with no wildcard name keeps the note it always had.
func TestPatternGrantNoteSaysItsReach(t *testing.T) {
	for _, c := range []struct {
		g    Grant
		want string
	}{
		{Grant{Kind: KindRead, Repo: "github.com/acme/*", RepoPattern: true}, "every repository under acme on github.com"},
		{Grant{Kind: KindRuns, Repo: "github.com/acme/*", Workflow: "ci.yml", RepoPattern: true}, "every repository under acme on github.com"},
		{Grant{Kind: KindBranch, Repo: "github.com/acme/*", Branch: "patch/*", RepoPattern: true},
			"may create, update, force-push and delete branches matching `patch/*` on github.com/acme/*; every repository under acme on github.com"},
		{Grant{Kind: KindRuns, Repo: "github.com/acme/*", Workflow: AnyWorkflow, RepoPattern: true},
			"may run and re-run every workflow, including ones added to the default branch later, on the default branch of github.com/acme/*, and cancel any of its runs, unasked; every repository under acme on github.com"},
		{Grant{Kind: KindRead, Repo: "github.com/acme/*"}, ""},
	} {
		if got := GrantNote(c.g); got != c.want {
			t.Errorf("GrantNote(%s) = %q, want %q", c.g, got, c.want)
		}
	}
}
