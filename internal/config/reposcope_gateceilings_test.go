package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// repoScopeUserScoped is the user's broad read and one scoped ceiling: runs on the repositories
// github.com/acme/* admits, and nothing else (GW-D64).
const repoScopeUserScoped = "[allow.github]\nalways = [\"read\"]\n" +
	"[repos.jail]\nrepo = \"github.com/acme/*\"\n[repos.jail.allow.github]\non_request = [\"runs\"]\n"

// The project's table is named jail too, and points somewhere the user's jail does not admit.
const repoScopeProjectTwoRepos = "[requests.github]\nruns = [\"*\"]\n" +
	"[repos.jail]\nrepo = \"github.com/example/tools\"\n[repos.jail.requests.github]\nruns = [\"lint.yml\"]\n"

// A request under a name is skipped at the gate and reported with its source and table.
func repoScopeSkipped(check *ScopeCheck, path string) bool {
	for _, r := range check.ReadableInput.Requests {
		if r.Path == path && r.Status == ReadableRequestSkipped {
			return true
		}
	}
	return false
}

// repoScopeDecisionOrigins is the origins of the one grant made for grant, across the sources.
func repoScopeDecisionOrigins(check *ScopeCheck, grant brokerscope.Grant) []string {
	for _, s := range check.ReadableInput.Sources {
		for _, d := range s.Decisions {
			if d.Grant == grant {
				return d.Origins
			}
		}
	}
	return nil
}

func TestAScopedCeilingAdmitsARequestByRepositoryNeverByName(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserScoped, repoScopeOrigin, repoScopeProjectTwoRepos, "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	s := check.Sources[0]
	if got := repoScopeWrites(s.Grants); len(got) != 1 || got[0] != "runs github.com/acme/app *" {
		t.Fatalf("writes are %v, want only origin's, which the user's pattern admits", got)
	}
	if !repoScopeSkipped(check, "repos.jail.requests.github.runs") {
		t.Fatalf("the request outside every ceiling was not reported as skipped: %#v", check.ReadableInput.Requests)
	}
	for _, e := range s.Entry {
		if e.Repo != "example/tools" {
			t.Errorf("a user config's name put %q in this workspace's scope", e.Repo)
		}
	}
}

func TestAScopedCeilingAddsToTheBroadOne(t *testing.T) {
	user := strings.Replace(repoScopeUserScoped, "always = [\"read\"]\n", "always = [\"read\"]\non_request = [\"runs\"]\n", 1)
	workspace := repoScopeWorkspace(t, user, repoScopeOrigin, repoScopeProjectTwoRepos, "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	want := []string{"runs github.com/acme/app *", "runs github.com/example/tools lint.yml"}
	if got := repoScopeWrites(check.Sources[0].Grants); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("writes are %v, want %v: a repos table never narrows [allow.github]", got, want)
	}
}

// A workspace pattern is admitted only by a user ceiling whose every segment is the pattern's own
// `*` or the same literal: a literal owner or repository cannot admit a wildcard (GW-D80).
func TestAScopedCeilingAdmitsAPatternOnlyBySegmentSubset(t *testing.T) {
	project := "[repos.fleet]\nrepo = \"github.com/acme/*\"\n[repos.fleet.requests.github]\nruns = [\"lint.yml\"]\n"
	for _, c := range []struct {
		name, ceiling string
		want          bool
	}{
		{"a literal repository", "github.com/acme/app", false},
		{"a wildcard owner and a literal repository", "github.com/*/app", false},
		{"a wildcard owner with a wildcard repository", "github.com/*/*", true},
		{"the same owner pattern", "github.com/acme/*", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			user := "[allow.github]\nalways = [\"read\"]\n[repos.jail]\nrepo = \"" + c.ceiling + "\"\n[repos.jail.allow.github]\non_request = [\"runs\"]\n"
			workspace := repoScopeWorkspace(t, user, repoScopeOrigin, project, "")
			check, _ := repoScopeCheck(t, workspace)
			if check.Fatal != nil || len(check.Sources) != 1 {
				t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
			}
			approved := brokerscope.Grant{Kind: brokerscope.KindRuns, Repo: "github.com/acme/*", Workflow: "lint.yml", RepoPattern: true}
			if got := containsGrant(check.Sources[0].Grants, approved); got != c.want {
				t.Fatalf("pattern granted %t, want %t: writes %v", got, c.want, repoScopeWrites(check.Sources[0].Grants))
			}
			if skipped := repoScopeSkipped(check, "repos.fleet.requests.github.runs"); skipped == c.want {
				t.Fatalf("skipped %t with the grant %t: the request's status must say what the gate did", skipped, c.want)
			}
		})
	}
}

// A user ceiling whose host is not the workspace's remote's host admits nothing here, and the
// request is skipped, not fatal.
func TestAScopedCeilingOnAnotherHostIsSkippedNotFatal(t *testing.T) {
	user := "[allow.github]\nalways = [\"read\"]\n[repos.jail]\nrepo = \"gitlab.example/acme/*\"\n[repos.jail.allow.github]\non_request = [\"runs\"]\n"
	workspace := repoScopeWorkspace(t, user, repoScopeOrigin, "[requests.github]\nruns = [\"*\"]\n", "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil {
		t.Fatalf("a ceiling on another host is fatal: %v", check.Fatal)
	}
	if got := repoScopeWrites(check.Sources[0].Grants); len(got) != 0 {
		t.Fatalf("writes are %v, want none: no ceiling admits origin on github.com", got)
	}
	if !repoScopeSkipped(check, "requests.github.runs") {
		t.Fatalf("the unadmitted unqualified request was not reported as skipped: %#v", check.ReadableInput.Requests)
	}
}

// A scoped ceiling admits a write only where the broker has its own read floor: without always
// read, the named request is skipped and no write is made (the read floor is not granted here).
func TestAScopedCeilingWithoutAReadFloorWritesNothing(t *testing.T) {
	user := "[repos.jail]\nrepo = \"github.com/acme/app\"\n[repos.jail.allow.github]\non_request = [\"runs\"]\n"
	workspace := repoScopeWorkspace(t, user, repoScopeOrigin, "[repos.jail]\nrepo = \"github.com/acme/app\"\n[repos.jail.requests.github]\nruns = [\"ci.yml\"]\n", "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil {
		t.Fatalf("scope check fatal: %v", check.Fatal)
	}
	if got := repoScopeWrites(check.Sources[0].Grants); len(got) != 0 {
		t.Fatalf("writes are %v with no read floor", got)
	}
}

// The grant a scoped ceiling makes names the table that admitted it, so the gate can say why.
func TestAScopedCeilingProvenanceNamesTheAdmittingTable(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserScoped, repoScopeOrigin, "[requests.github]\nruns = [\"*\"]\n", "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	origin := brokerscope.Grant{Kind: brokerscope.KindRuns, Repo: "github.com/acme/app", Workflow: "*"}
	if got := repoScopeDecisionOrigins(check, origin); !slices.Contains(got, "repos.jail.allow.github.on_request") {
		t.Fatalf("origins of the scoped grant are %v, want the admitting table named", got)
	}
}

// A scoped always token is a boolean kind's grant on each in-scope identity its table admits, and
// on no other: the table adds no identity of its own, and the other remote stays unwritten (GW-D64).
func TestAScopedAlwaysLowersOnlyOnItsAdmittedIdentities(t *testing.T) {
	user := "[allow.github]\nalways = [\"read\"]\n[repos.jail]\nrepo = \"github.com/acme/app\"\n[repos.jail.allow.github]\nalways = [\"create_pr\"]\n"
	gitConfig := repoScopeOrigin + "[remote \"upstream\"]\n\turl = https://github.com/elsewhere/app.git\n"
	workspace := repoScopeWorkspace(t, user, gitConfig, "", "")
	broker := readableScopeBroker("github", "github", "github-broker", "github.com", "read", []string{"create-pr"}, map[string]string{"create-pr": "create_pr"})
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	check := NewScopeCheck(workspace, []ScopeBroker{broker}, read)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	want := []string{"create-pr github.com/acme/app"}
	if got := repoScopeWrites(check.Sources[0].Grants); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("writes are %v, want %v", got, want)
	}
}
