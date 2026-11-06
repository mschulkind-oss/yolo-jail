package brokerscope

import (
	"strings"
	"testing"
)

// An approve-run grant names one GitHub login, a bot's included, or `*` in its own field, and
// spells back through String and ParseGrant.
func TestApproveRunGrantShape(t *testing.T) {
	for _, c := range []struct {
		g  Grant
		ok bool
	}{
		{Grant{Kind: KindApproveRun, Repo: "github.com/o/r", Actor: "github-actions[bot]"}, true},
		{Grant{Kind: KindApproveRun, Repo: "github.com/o/r", Actor: "octocat"}, true},
		{Grant{Kind: KindApproveRun, Repo: "github.com/o/r", Actor: "Octo-Cat-2"}, true},
		{Grant{Kind: KindApproveRun, Repo: "*", Actor: "*"}, true},
		{Grant{Kind: KindApproveRun, Repo: "github.com/o/r", Actor: strings.Repeat("a", 39)}, true},
		{Grant{Kind: KindApproveRun, Repo: "github.com/o/r"}, false},
		{Grant{Kind: KindApproveRun, Repo: "github.com/o/r", Actor: strings.Repeat("a", 40)}, false},
		{Grant{Kind: KindApproveRun, Repo: "github.com/o/r", Actor: "-octocat"}, false},
		{Grant{Kind: KindApproveRun, Repo: "github.com/o/r", Actor: "octo cat"}, false},
		{Grant{Kind: KindApproveRun, Repo: "github.com/o/r", Actor: "octo_cat"}, false},
		{Grant{Kind: KindApproveRun, Repo: "github.com/o/r", Actor: "[bot]"}, false},
		{Grant{Kind: KindApproveRun, Repo: "github.com/o/r", Actor: "dependabot*"}, false},
		{Grant{Kind: KindApproveRun, Repo: "github.com/o/r", Actor: "octocat", Workflow: "ci.yml"}, false},
		{Grant{Kind: KindRuns, Repo: "github.com/o/r", Workflow: "ci.yml", Actor: "octocat"}, false},
		{Grant{Kind: "create-pr", Repo: "github.com/o/r", Actor: "octocat"}, false},
	} {
		if got := ValidGrant(c.g); got != c.ok {
			t.Errorf("ValidGrant(%+v) = %v, want %v", c.g, got, c.ok)
			continue
		}
		if !c.ok {
			continue
		}
		back, ok := ParseGrant(c.g.String())
		if !ok || back != c.g {
			t.Errorf("ParseGrant(%q) = %+v %v, want %+v", c.g.String(), back, ok, c.g)
		}
	}
	if !GrantTakesName(KindApproveRun) || GrantTakesRef(KindApproveRun) || GrantNameKey(KindApproveRun) != "actor" ||
		!ValidGrantName(KindApproveRun, "github-actions[bot]") || ValidGrantName(KindApproveRun, "a b") {
		t.Fatal("approve-run names an actor, not a ref")
	}
	if g := RefGrant(KindApproveRun, "github.com/o/r", "octocat"); g.Actor != "octocat" || g.Name() != "octocat" {
		t.Fatalf("RefGrant put the actor elsewhere: %+v", g)
	}
}

func TestApproveRunGrantNote(t *testing.T) {
	for _, c := range []struct {
		g    Grant
		want string
	}{
		{Grant{Kind: KindApproveRun, Repo: "github.com/o/r", Actor: AnyActor},
			"may approve workflow runs any actor triggered from github.com/o/r itself (never a fork's), unasked"},
		{Grant{Kind: KindApproveRun, Repo: AnyRepo, Actor: "github-actions[bot]"},
			"may approve workflow runs `github-actions[bot]` triggered from every repository in scope now and any approved later, each itself (never a fork's), unasked"},
		{Grant{Kind: KindApproveRun, Repo: "github.com/o/r", Actor: "github-actions[bot]"}, ""},
	} {
		if got := GrantNote(c.g); got != c.want {
			t.Errorf("GrantNote(%s) = %q, want %q", c.g, got, c.want)
		}
	}
}
