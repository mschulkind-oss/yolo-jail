package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const repoScopeOrigin = "[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n"

func TestAReposTableIsReadAndAnUnknownKeyUnderItIsRefused(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, "[repos.infra]\nrepo = \"github.com/acme/infra\"\n", "")
	if _, err := ReadWorkspaceForGate(workspace); err != nil {
		t.Fatalf("a [repos.<name>] table with a repo line is refused: %v", err)
	}
	workspace = repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, "[repos.infra]\nrepo = \"github.com/acme/infra\"\nrepoz = \"x\"\n", "")
	if _, err := ReadWorkspaceForGate(workspace); err == nil || !strings.Contains(err.Error(), "repos.infra.repoz") {
		t.Fatalf("an unknown key under a repos table: %v", err)
	}
}

func TestANamedRepositoryInALocalFileIsReadOnly(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, repoScopeOriginLines,
		"[repos.notes]\nrepo = \"github.com/example/notes\"\n")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	s := check.Sources[0]
	if !containsGrant(s.Grants, brokerscope.Grant{Kind: brokerscope.KindRead, Repo: "github.com/example/notes"}) {
		t.Fatalf("the named repository is not in the read scope: %v", s.Grants)
	}
	if !readableScopeEntry(s.Entry, "example/notes", WorkspaceLocalTOMLConfigName) {
		t.Fatalf("the scope row does not name the local file: %#v", s.Entry)
	}
	want := []string{"approve-run github.com/acme/app github-actions[bot]", "runs github.com/acme/app *"}
	if got := repoScopeWrites(s.Grants); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("writes are %v, want origin's own and none on the named repository: %v", got, want)
	}
}

func TestANamedTablesRequestLowersToItsOwnRepositoryOnly(t *testing.T) {
	project := "[repos.tools]\nrepo = \"github.com/example/tools\"\n[repos.tools.requests.github]\nruns = [\"lint.yml\"]\n"
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, project, "")
	check, read := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	s := check.Sources[0]
	grant := brokerscope.Grant{Kind: brokerscope.KindRuns, Repo: "github.com/example/tools", Workflow: "lint.yml"}
	if got := repoScopeWrites(s.Grants); len(got) != 1 || got[0] != grant.String() {
		t.Fatalf("writes are %v, want only %s", got, grant.String())
	}
	if origin := s.GrantOrigins[grant.String()]; !strings.Contains(origin, WorkspaceTOMLConfigName) || !strings.Contains(origin, "repos.tools.requests.github.runs") {
		t.Fatalf("the grant's gate line ends with %q, want the file and repos.tools.requests.github.runs", origin)
	}
	var projected []string
	for _, w := range WorkspaceBrokerGrantsOf(read.Config, workspace, []ReadableBrokerDeclaration{*repoScopeBroker().Readable}) {
		for _, g := range w.Grants {
			projected = append(projected, g.String())
		}
	}
	if len(projected) != 1 || projected[0] != grant.String() {
		t.Fatalf("the workspace projection lowers %v, want only %s", projected, grant.String())
	}
}

func TestATableWithNoRepoLineMeansTheRemoteOfThatName(t *testing.T) {
	gitConfig := repoScopeOrigin + "[remote \"upstream\"]\n\turl = https://github.com/elsewhere/app.git\n"
	workspace := repoScopeWorkspace(t, repoScopeUserAll, gitConfig, "[repos.upstream.requests.github]\nruns = [\"ci.yml\"]\n", "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	if got := repoScopeWrites(check.Sources[0].Grants); len(got) != 1 || got[0] != "runs github.com/elsewhere/app ci.yml" {
		t.Fatalf("writes are %v, want the upstream remote's repository only", got)
	}
}

func TestARetargetedNameIsANewIdentityAtTheGate(t *testing.T) {
	project := "[repos.tools]\nrepo = \"github.com/example/tools\"\n[repos.tools.requests.github]\nruns = [\"lint.yml\"]\n"
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, project, "")
	check, _ := repoScopeCheck(t, workspace)
	if err := RecordApproval(workspace, decode(t, `{}`), check); err != nil {
		t.Fatal(err)
	}
	local := "[repos.tools]\nrepo = \"github.com/example/tools-fork\"\n"
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceLocalTOMLConfigName), []byte(local), 0o600); err != nil {
		t.Fatal(err)
	}
	check, _ = repoScopeCheck(t, workspace)
	report, err := ScopeChanges(workspace, check)
	if err != nil {
		t.Fatal(err)
	}
	block := strings.Join(report.ScopeBlock, "\n")
	if !report.ScopeChanged {
		t.Fatalf("a retargeted name did not ask again:\n%s", block)
	}
	for _, want := range []string{"  - example/tools ", "  + example/tools-fork ",
		"  - grant runs github.com/example/tools lint.yml", "  + grant runs github.com/example/tools-fork lint.yml"} {
		if !strings.Contains(block, want) {
			t.Errorf("the gate does not show %q:\n%s", want, block)
		}
	}
}

func TestAReposTableThatCannotBeResolvedIsAnErrorWithItsNextStep(t *testing.T) {
	for _, c := range []struct {
		name, project string
		want          []string
	}{
		{"no repo line and no remote of that name", "[repos.nowhere.requests.github]\nruns = [\"ci.yml\"]\n",
			[]string{"repos.nowhere", "origin", "yolo request repos nowhere"}},
		{"a repo with no host", "[repos.infra]\nrepo = \"acme/infra\"\n", []string{"repos.infra.repo", "github.com/acme/infra"}},
		{"origin's GitHub lines have one home", "[repos.origin.requests.github]\nruns = [\"ci.yml\"]\n",
			[]string{"repos.origin.requests.github", "[requests.github]"}},
		{"all is not a name", "[repos.all]\nrepo = \"github.com/acme/infra\"\n", []string{"repos.all"}},
		{"a table that says nothing", "[repos.upstream]\n", []string{"repos.upstream"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, c.project, "")
			read, err := ReadWorkspaceForGate(workspace)
			text := ""
			if err != nil {
				text = err.Error()
			} else if check := NewScopeCheck(workspace, []ScopeBroker{repoScopeBroker()}, read); check.Fatal != nil {
				text = check.Fatal.Error()
			}
			if text == "" {
				t.Fatal("no error")
			}
			for _, want := range c.want {
				if !strings.Contains(text, want) {
					t.Errorf("the error does not name %q: %s", want, text)
				}
			}
		})
	}
}

func TestAGitLineUnderANameIsNotBuiltYet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, repoScopeUserAll+"[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\"]\n")
	workspace := t.TempDir()
	writeReadableScopeRemote(t, workspace, repoScopeOrigin)
	project := "[repos.infra]\nrepo = \"github.com/acme/infra\"\n[repos.infra.requests]\npush = [\"main\"]\n"
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}
	brokers := []ScopeBroker{repoScopeBroker(),
		readableScopeBroker("git", "git", "git-broker", "*", "fetch", []string{"branch"}, map[string]string{"branch": "push"})}
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	check := NewScopeCheck(workspace, brokers, read)
	text := ""
	if check.Fatal != nil {
		text = check.Fatal.Error()
	}
	for _, want := range []string{"repos.infra.requests.push", "yolo request git push main"} {
		if !strings.Contains(text, want) {
			t.Errorf("the error does not name %q: %q", want, text)
		}
	}
}

func TestRequestsAndAllowUnderANameAreEachRefusedInTheOthersFile(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin,
		"[repos.infra]\nrepo = \"github.com/acme/infra\"\n[repos.infra.allow.github]\non_request = [\"runs\"]\n", "")
	if _, err := ReadWorkspaceForGate(workspace); err == nil || !strings.Contains(err.Error(), "repos.infra.allow") {
		t.Fatalf("a project file's ceiling under a name: %v", err)
	}
	user := repoScopeUserAll + "[repos.mine]\nrepo = \"github.com/acme/*\"\n[repos.mine.requests.github]\nruns = [\"ci.yml\"]\n"
	workspace = repoScopeWorkspace(t, user, repoScopeOrigin, repoScopeOriginLines, "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal == nil || !strings.Contains(check.Fatal.Error(), "repos.mine.requests") {
		t.Fatalf("a user config's request under a name: fatal %v", check.Fatal)
	}
}

func TestAWildcardTableLowersOnePatternGrantPerLine(t *testing.T) {
	project := "[repos.fleet]\nrepo = \"github.com/acme/*\"\n[repos.fleet.requests.github]\nruns = [\"lint.yml\"]\n"
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, project, "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	s := check.Sources[0]
	if got := repoScopeWrites(s.Grants); len(got) != 1 || got[0] != "runs pattern:github.com/acme/* lint.yml" {
		t.Fatalf("writes are %v, want one pattern grant and nothing enumerated or on every repository", got)
	}
	if !readableScopeEntry(s.Entry, "acme/*", WorkspaceTOMLConfigName) {
		t.Fatalf("the pattern is not a scope row: %#v", s.Entry)
	}
	report, err := ScopeChanges(workspace, check)
	if err != nil {
		t.Fatal(err)
	}
	block := strings.Join(report.ScopeBlock, "\n")
	for _, line := range strings.Split(block, "\n") {
		if strings.Contains(line, "grant runs pattern:github.com/acme/* lint.yml") && !strings.Contains(line, "every repository under acme") {
			t.Errorf("the pattern grant's line does not say what it admits: %s", line)
		}
	}
	if !strings.Contains(block, "grant runs pattern:github.com/acme/* lint.yml") {
		t.Fatalf("the gate shows no pattern grant:\n%s", block)
	}
}

// A pattern write an older gate recorded matched no command. A named pattern table that lowers the
// same repository and workflow is a new grant: the gate shows it added, with its plain words, and
// asks, and the record never stands in for that approval (GW-D80).
func TestANamedPatternTableIsAskedForOverARecordedLegacyPatternGrant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.github]\nalways = [\"read\"]\non_request = [\"runs\"]\n")
	workspace := t.TempDir()
	writeReadableScopeRemote(t, workspace, "[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n")
	broker := readableScopeBroker("github", "github", "github-broker", "github.com", "read", []string{"runs"}, map[string]string{"runs": "runs"})
	check := func() *ScopeCheck {
		t.Helper()
		read, err := ReadWorkspaceForGate(workspace)
		if err != nil {
			t.Fatal(err)
		}
		c := NewScopeCheck(workspace, []ScopeBroker{broker}, read)
		if c.Fatal != nil || len(c.Sources) != 1 {
			t.Fatalf("scope check: fatal %v, sources %#v", c.Fatal, c.Sources)
		}
		return c
	}
	legacy := brokerscope.Grant{Kind: brokerscope.KindRuns, Repo: "github.com/acme/*", Workflow: "ci.yml"}

	// The old spelling, approved: the entry and a grant on it, as an older gate recorded them.
	old := filepath.Join(workspace, WorkspaceConfigName)
	if err := os.WriteFile(old, []byte(`{"packs":["github"],"brokered":{"github":{"repos":["acme/*"],"grants":[{"kind":"runs","repo":"github.com/acme/*","workflow":"ci.yml"}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before := check()
	if !containsGrant(before.Sources[0].Grants, legacy) {
		t.Fatalf("the legacy entry no longer lowers its inert grant: %v", before.Sources[0].Grants)
	}
	if err := RecordApproval(workspace, nil, before); err != nil {
		t.Fatal(err)
	}
	if got := grantStrings(ApprovedGrants(workspace, "github")); !inList(got, "runs github.com/acme/* ci.yml") {
		t.Fatalf("the record holds %v", got)
	}
	if rep, err := ScopeChanges(workspace, check()); err != nil || rep.ScopeChanged {
		t.Fatalf("the approved legacy scope asks again: %v\n%s", err, strings.Join(rep.ScopeBlock, "\n"))
	}

	// The same reach, written as a named pattern table.
	if err := os.Remove(old); err != nil {
		t.Fatal(err)
	}
	table := "[repos.fleet]\nrepo = \"github.com/acme/*\"\n\n[repos.fleet.requests.github]\nruns = [\"ci.yml\"]\n"
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte(table), 0o600); err != nil {
		t.Fatal(err)
	}
	after := check()
	approved := legacy
	approved.RepoPattern = true
	if !containsGrant(after.Sources[0].Grants, approved) || containsGrant(after.Sources[0].Grants, legacy) {
		t.Fatalf("the table lowered %v, want the grant marked as a pattern and no unmarked one", after.Sources[0].Grants)
	}
	rep, err := ScopeChanges(workspace, after)
	if err != nil || !rep.ScopeChanged {
		t.Fatalf("a recorded legacy grant stood in for the table's approval: changed %v, %v", rep.ScopeChanged, err)
	}
	added := false
	for _, line := range rep.ScopeBlock {
		if strings.Contains(line, "grant runs pattern:github.com/acme/* ci.yml") {
			added = strings.Contains(line, "added") && strings.Contains(line, "every repository under acme")
		}
	}
	if !added {
		t.Fatalf("the gate does not show the pattern grant added with what it admits:\n%s", strings.Join(rep.ScopeBlock, "\n"))
	}
	if err := RecordApproval(workspace, nil, after); err != nil {
		t.Fatal(err)
	}
	if rep, err := ScopeChanges(workspace, check()); err != nil || rep.ScopeChanged {
		t.Fatalf("the approved table asks again: %v\n%s", err, strings.Join(rep.ScopeBlock, "\n"))
	}
}

// A workspace whose only readable key is a repos table still takes the readable path, under a
// user config that grants read in the old spelling.
func TestAReposTableAloneSelectsTheReadableScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"brokered": {"github": {"grants": [{"kind": "read", "repo": "*"}]}}}`
	if err := os.WriteFile(filepath.Join(userDir, paths.UserConfigLegacyJSONCName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeReadableScopeRemote(t, workspace, repoScopeOrigin)
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte("[repos.notes]\nrepo = \"github.com/example/notes\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	if check.ReadableInput == nil {
		t.Fatal("a repos table alone fell to the legacy scope producer")
	}
	if !readableScopeEntry(check.Sources[0].Entry, "example/notes", WorkspaceTOMLConfigName) {
		t.Fatalf("the named repository is not in the scope: %#v", check.Sources[0].Entry)
	}
}

// A repo line's host is a host, not a path or an account: a repo line that does not start with a
// lowercase host is refused where it is written, naming the line and the form that resolves (GW-D76).
func TestARepoLineWithAMalformedHostIsRefused(t *testing.T) {
	for _, line := range []string{"GitHub.com/acme/infra", "user@github.com/acme/infra", "acme/infra/extra"} {
		t.Run(line, func(t *testing.T) {
			workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, "[repos.infra]\nrepo = \""+line+"\"\n", "")
			check, _ := repoScopeCheck(t, workspace)
			if check.Fatal == nil || !strings.Contains(check.Fatal.Error(), "repos.infra.repo") || !strings.Contains(check.Fatal.Error(), "github.com/acme/infra") {
				t.Fatalf("fatal %v, want a refusal naming repos.infra.repo and github.com/acme/infra", check.Fatal)
			}
		})
	}
}

// Pin beside the malformed-host refusal: a mixed-case owner and repository are GitHub's own spelling
// and resolve as the identity they name.
func TestARepoLineWithMixedCaseNamesIsAccepted(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, "[repos.infra]\nrepo = \"github.com/Acme/Infra\"\n", "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil {
		t.Fatalf("a mixed-case repo line is refused: %v", check.Fatal)
	}
}

func TestTheOldReposKeyIsRefusedNamingItsTableAndTheMigration(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeOrigin, "[requests.github]\nrepos = [\"other/lib\"]\n", "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal == nil {
		t.Fatal("requests.github.repos was accepted; it is retired (GW-D75)")
	}
	for _, want := range []string{"requests.github.repos", "[repos.<name>]", "github.com/other/lib", "yolo config migrate"} {
		if !strings.Contains(check.Fatal.Error(), want) {
			t.Fatalf("the refusal lacks %q: %v", want, check.Fatal)
		}
	}
	for _, s := range check.Sources {
		for _, g := range s.Grants {
			if g.Kind != brokerscope.KindRead {
				t.Fatalf("the refused key still granted %v", g)
			}
		}
	}
}
