package gitbroker

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// Wildcard branch grants and tag grants (docs/design/git-broker.md GB-D25): a branch grant's
// name may be a refspec pattern and its repository "*", and a tag grant covers tags the same way.

// second is a second remote of the workspace: the fake ssh serves the same bare repository, but
// its identity is another repository's.
const second = "git@other.test:o/r.git"

// A pattern covers every branch it matches, deeper paths included, and nothing else; the audit
// names the grant by its pattern.
func TestPatternBranchGrantCoversMatchingBranches(t *testing.T) {
	f := configBranchFixture(t, "patch/*")
	f.commit("one")
	f.mustPush("origin", "HEAD:refs/heads/patch/a/b")
	if got, want := f.upstreamRef("refs/heads/patch/a/b"), mustGit(t, f.ws, "rev-parse", "HEAD"); got != want {
		t.Fatalf("upstream patch/a/b %q, local HEAD %q", got, want)
	}
	if p := lastPush(t); p.Set != setWorkingSet || p.Refs[0].CoveredBy != "branch:patch/*" {
		t.Fatalf("pattern push audit %+v", p)
	}
	f.mustPush("origin", ":refs/heads/patch/a/b")
	for _, ref := range []string{"refs/heads/patches/x", "refs/heads/patch", "refs/tags/patch/x"} {
		out, err := f.git("push", "origin", "HEAD:"+ref)
		if err == nil || !strings.Contains(out, reasonNeedsApproval) {
			t.Fatalf("a push to %s under branch patch/*: %v\n%s", ref, err, out)
		}
	}
	if n := len(liveEntries(t)); n != 0 {
		t.Fatalf("a branch the config declares was recorded in the store (%d entries)", n)
	}
}

// A branch grant on every repository covers the branch on each remote in scope.
func TestAnyRepoBranchGrantCoversEveryRemote(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{repos: []string{origin, second},
		grants: []brokerscope.Grant{{Kind: "branch", Repo: brokerscope.AnyRepo, Branch: "main"}}})
	allowKinds(t, "branch")
	mustGit(t, f.ws, "remote", "add", "second", second)
	f.commit("one")
	f.mustPush("origin", "HEAD:main")
	f.commit("two")
	f.mustPush("second", "HEAD:main")
	if got, want := f.upstreamRef("refs/heads/main"), mustGit(t, f.ws, "rev-parse", "HEAD"); got != want {
		t.Fatalf("after the push to the second remote, upstream main %q, local HEAD %q", got, want)
	}
	if p := lastPush(t); p.Repo != "other.test/o/r" || p.Refs[0].CoveredBy != "branch:main" {
		t.Fatalf("second remote's audit %+v", p)
	}
	out, err := f.git("push", "second", "HEAD:refs/heads/other")
	if err == nil || !strings.Contains(out, reasonNeedsApproval) {
		t.Fatalf("a branch the grant does not name landed: %v\n%s", err, out)
	}
}

func tagFixture(t *testing.T, tag string) *fixture {
	t.Helper()
	f := newFixtureOpts(t, fixtureOpts{grants: []brokerscope.Grant{{Kind: "tag", Repo: fixtureRepo, Tag: tag}}})
	allowKinds(t, "tag")
	return f
}

// A tag grant covers creating, force-updating and deleting a matching tag, which never joins the
// working set; a tag it does not match still asks.
func TestTagGrantCoversMatchingTags(t *testing.T) {
	f := tagFixture(t, "v*")
	mustGit(t, f.ws, "tag", "v1")
	f.mustPush("origin", "refs/tags/v1")
	if f.upstreamRef("refs/tags/v1") == "" {
		t.Fatal("the granted tag is not upstream")
	}
	if p := lastPush(t); p.Set != setTag || p.Refs[0].CoveredBy != "tag:v*" {
		t.Fatalf("tag push audit %+v", p)
	}
	f.commit("one")
	mustGit(t, f.ws, "tag", "-f", "v1")
	f.mustPush("--force", "origin", "refs/tags/v1")
	if got, want := f.upstreamRef("refs/tags/v1"), mustGit(t, f.ws, "rev-parse", "HEAD"); got != want {
		t.Fatalf("after the force push, upstream v1 %q, local HEAD %q", got, want)
	}
	f.mustPush("origin", ":refs/tags/v1")
	if f.upstreamRef("refs/tags/v1") != "" {
		t.Fatal("v1 survived its delete")
	}
	if n := len(liveEntries(t)); n != 0 {
		t.Fatalf("a tag joined the working set (%d entries)", n)
	}
	mustGit(t, f.ws, "tag", "release-1")
	out, err := f.git("push", "origin", "refs/tags/release-1")
	refusedRequest(t, out, err)
	// A tag grant covers no branch of the same name.
	out, err = f.git("push", "origin", "HEAD:refs/heads/v2")
	if err == nil || !strings.Contains(out, reasonNeedsApproval) {
		t.Fatalf("a branch landed under a tag grant: %v\n%s", err, out)
	}
}

// A tag grant whose kind the user config does not list covers nothing, and the refusal names
// the verb that allows the kind.
func TestTagGrantKindNotListedRefuses(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{grants: []brokerscope.Grant{{Kind: "tag", Repo: fixtureRepo, Tag: "v*"}}})
	allowKinds(t, "branch")
	mustGit(t, f.ws, "tag", "v1")
	out, err := f.git("push", "origin", "refs/tags/v1")
	if err == nil || !strings.Contains(out, reasonNeedsApproval) {
		t.Fatalf("a tag push with the kind unlisted: %v\n%s", err, out)
	}
	convertThenAllow(t, out, grantTag)
	if f.upstreamRef("refs/tags/v1") != "" {
		t.Fatal("the tag reached the upstream")
	}
}

// A tag grant on every repository covers the tag on each remote in scope.
func TestAnyRepoTagGrantCoversEveryRemote(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{repos: []string{origin, second},
		grants: []brokerscope.Grant{{Kind: "tag", Repo: brokerscope.AnyRepo, Tag: "v*"}}})
	allowKinds(t, "tag")
	mustGit(t, f.ws, "remote", "add", "second", second)
	mustGit(t, f.ws, "tag", "v1")
	f.mustPush("second", "refs/tags/v1")
	if f.upstreamRef("refs/tags/v1") == "" {
		t.Fatal("the tag pushed through the second remote is not upstream")
	}
}
