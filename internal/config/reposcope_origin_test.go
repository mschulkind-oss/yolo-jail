package config

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

func TestUnqualifiedGitHubRequestsLowerToOriginOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.github]\nalways = [\"read\"]\non_request = [\"runs\", \"approve_runs\"]\n")
	workspace := t.TempDir()
	writeReadableScopeRemote(t, workspace, "[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n[remote \"upstream\"]\n\turl = https://github.com/elsewhere/app.git\n")
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName), []byte("[requests.github]\nruns = [\"*\"]\napprove_runs = [\"github-actions[bot]\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	broker := repoScopeBroker()
	check := NewScopeCheck(workspace, []ScopeBroker{broker}, read)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	grants := check.Sources[0].Grants
	if !containsGrant(grants, brokerscope.Grant{Kind: brokerscope.KindRuns, Repo: "github.com/acme/app", Workflow: "*"}) {
		t.Fatalf("origin lost its runs grant: %v", grants)
	}
	if !containsGrant(grants, brokerscope.Grant{Kind: brokerscope.KindRead, Repo: "github.com/elsewhere/app"}) {
		t.Fatalf("the second remote left the read scope: %v", grants)
	}
	for _, g := range grants {
		if g.Kind != brokerscope.KindRead && g.Repo != "github.com/acme/app" {
			t.Errorf("an unqualified request reached a repository other than origin: %s", g.String())
		}
	}
}

func repoScopeBroker() ScopeBroker {
	kinds := map[string]string{"runs": "runs", "approve-run": "approve_runs"}
	broker := readableScopeBroker("github", "github", "github-broker", "github.com", "read", []string{"runs", "approve-run"}, kinds)
	// What packs/github declares: its unqualified requests are for origin.
	broker.Readable.Brokered.UnqualifiedRequests = "origin"
	return broker
}

// repoScopeWorkspace writes the user config, the clone's git config and the two workspace
// files ("" leaves one out), and returns the workspace.
func repoScopeWorkspace(t *testing.T, user, gitConfig, project, local string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, user)
	workspace := t.TempDir()
	if gitConfig != "" {
		writeReadableScopeRemote(t, workspace, gitConfig)
	}
	for name, body := range map[string]string{WorkspaceTOMLConfigName: project, WorkspaceLocalTOMLConfigName: local} {
		if body == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return workspace
}

func repoScopeCheck(t *testing.T, workspace string) (*ScopeCheck, *WorkspaceRead) {
	t.Helper()
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	return NewScopeCheck(workspace, []ScopeBroker{repoScopeBroker()}, read), read
}

func repoScopeWrites(grants []brokerscope.Grant) []string {
	var out []string
	for _, g := range grants {
		if g.Kind != brokerscope.KindRead {
			out = append(out, g.String())
		}
	}
	sort.Strings(out)
	return out
}

const repoScopeUserAll = "[allow.github]\nalways = [\"read\"]\non_request = [\"runs\", \"approve_runs\"]\n"
const repoScopeOriginLines = "[requests.github]\nruns = [\"*\"]\napprove_runs = [\"github-actions[bot]\"]\n"

func TestUnqualifiedGitHubRequestsWithoutAnOriginAreAnError(t *testing.T) {
	for _, c := range []struct{ name, gitConfig string }{
		{"the only remote is upstream", "[remote \"upstream\"]\n\turl = https://github.com/elsewhere/app.git\n"},
		{"origin is on another host", "[remote \"origin\"]\n\turl = https://gitlab.com/acme/app.git\n[remote \"upstream\"]\n\turl = https://github.com/elsewhere/app.git\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			workspace := repoScopeWorkspace(t, repoScopeUserAll, c.gitConfig, repoScopeOriginLines, "")
			check, _ := repoScopeCheck(t, workspace)
			if check.Fatal == nil {
				t.Fatalf("no error: an unqualified line fell back to another repository: %#v", check.Sources)
			}
			for _, want := range []string{"origin", "[repos.<name>]"} {
				if !strings.Contains(check.Fatal.Error(), want) {
					t.Errorf("the error does not name %q: %v", want, check.Fatal)
				}
			}
			for _, s := range check.Sources {
				if w := repoScopeWrites(s.Grants); len(w) != 0 {
					t.Errorf("a refused scope still carries %v", w)
				}
			}
		})
	}
}

// The gate and the migration proof's projection lower one workspace's ask to the same grants.
func TestTheWorkspaceGrantProjectionAgreesWithTheGate(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserAll,
		"[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n[remote \"upstream\"]\n\turl = https://github.com/elsewhere/app.git\n",
		repoScopeOriginLines, "")
	check, read := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	var projected []string
	for _, w := range WorkspaceBrokerGrantsOf(read.Config, workspace, []ReadableBrokerDeclaration{*repoScopeBroker().Readable}) {
		for _, g := range w.Grants {
			projected = append(projected, g.String())
		}
	}
	sort.Strings(projected)
	if got := repoScopeWrites(check.Sources[0].Grants); strings.Join(got, "\n") != strings.Join(projected, "\n") {
		t.Fatalf("the gate lowers\n%v\nand the workspace projection\n%v", got, projected)
	}
}

const repoScopeTwoRemotes = "[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n[remote \"upstream\"]\n\turl = https://github.com/elsewhere/app.git\n"

// A named-host source whose declaration does not opt in keeps today's reach on every remote.
func TestANamedHostSourceThatDoesNotOptInKeepsItsFanOut(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserAll, repoScopeTwoRemotes, repoScopeOriginLines, "")
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	broker := repoScopeBroker()
	broker.Readable.Brokered.UnqualifiedRequests = ""
	check := NewScopeCheck(workspace, []ScopeBroker{broker}, read)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	want := []string{"approve-run github.com/acme/app github-actions[bot]", "approve-run github.com/elsewhere/app github-actions[bot]",
		"runs github.com/acme/app *", "runs github.com/elsewhere/app *"}
	if got := repoScopeWrites(check.Sources[0].Grants); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("a source that did not opt in lowers %v, want %v", got, want)
	}
}

// When two remotes name the same repository, the origin grant is one grant, and the read grant
// covers the repository once: the alias does not widen or duplicate the grant.
func TestAnAliasedRemoteSharesOriginsGrantOnce(t *testing.T) {
	workspace := repoScopeWorkspace(t, repoScopeUserAll,
		"[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n[remote \"upstream\"]\n\turl = https://github.com/acme/app.git\n",
		repoScopeOriginLines, "")
	check, _ := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	want := []string{"approve-run github.com/acme/app github-actions[bot]", "runs github.com/acme/app *"}
	if got := repoScopeWrites(check.Sources[0].Grants); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("an aliased remote lowers %v, want %v", got, want)
	}
}

// The old projection keeps today's fan-out for the migration proof's before side; the new one
// is the gate's own lowering. Neither is the other.
func TestTheLegacyBaselineFansOutAndTheNewProjectionDoesNot(t *testing.T) {
	gitConfig := "[remote \"origin\"]\n\turl = https://github.com/acme/app.git\n[remote \"upstream\"]\n\turl = https://github.com/elsewhere/app.git\n"
	project := "[requests.github]\nrepos = [\"acme/extra\"]\nruns = [\"ci.yml\"]\n"
	workspace := repoScopeWorkspace(t, repoScopeUserAll, gitConfig, project, "")
	check, read := repoScopeCheck(t, workspace)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check: fatal %v, sources %#v", check.Fatal, check.Sources)
	}
	declarations := []ReadableBrokerDeclaration{*repoScopeBroker().Readable}
	strs := func(all []WorkspaceBrokerGrants) []string {
		var out []string
		for _, w := range all {
			for _, g := range w.Grants {
				out = append(out, g.String())
			}
		}
		sort.Strings(out)
		return out
	}
	before := strs(LegacyWorkspaceBrokerGrantsOf(read.Config, workspace, declarations))
	wantBefore := []string{"runs github.com/acme/app ci.yml", "runs github.com/acme/extra ci.yml", "runs github.com/elsewhere/app ci.yml"}
	if strings.Join(before, "\n") != strings.Join(wantBefore, "\n") {
		t.Fatalf("the legacy baseline lowers %v, want today's fan-out %v", before, wantBefore)
	}
	after := strs(WorkspaceBrokerGrantsOf(read.Config, workspace, declarations))
	if len(after) != 1 || after[0] != "runs github.com/acme/app ci.yml" {
		t.Fatalf("the new projection lowers %v, want origin's line only", after)
	}
	if got := repoScopeWrites(check.Sources[0].Grants); strings.Join(got, "\n") != strings.Join(after, "\n") {
		t.Fatalf("the gate lowers %v and the new projection %v", got, after)
	}
}
