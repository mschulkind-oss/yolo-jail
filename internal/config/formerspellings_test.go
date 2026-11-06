package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// formerspellings_test.go pins that the readable config's former spelling of the runs
// capability, `dispatch_workflows`, still grants (docs/design/github-write-scopes.md GW-D78):
// in the user's `allow.github.on_request` and as the project's `requests.github` key, it reads
// as `runs`, and the gate carries the same runs grant the new spelling gives.
func TestFormerDispatchWorkflowsSpellingGrantsRuns(t *testing.T) {
	for _, c := range []struct{ name, user, project string }{
		{"both former", "on_request = [\"dispatch_workflows\"]", "dispatch_workflows = [\"release-request.yml\"]"},
		{"former ceiling, new request", "on_request = [\"dispatch_workflows\"]", "runs = [\"release-request.yml\"]"},
		{"new ceiling, former request", "on_request = [\"runs\"]", "dispatch_workflows = [\"release-request.yml\"]"},
		{"both spellings in each", "on_request = [\"runs\", \"dispatch_workflows\"]",
			"runs = [\"release-request.yml\"]\ndispatch_workflows = [\"nightly.yml\"]"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			writeReadableScopeUser(t, home, "[allow.github]\nalways = [\"read\"]\n"+c.user+"\n")
			workspace := t.TempDir()
			writeReadableScopeRemote(t, workspace, "[remote \"origin\"]\nurl = git@github.com:acme/repo.git\n")
			if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName),
				[]byte("[requests.github]\n"+c.project+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			read, err := ReadWorkspaceForGate(workspace)
			if err != nil {
				t.Fatal(err)
			}
			broker := readableScopeBroker("github", "github", "github-broker", "github.com", "read",
				[]string{"runs"}, map[string]string{"runs": "runs"})
			report, err := ScopeChanges(workspace, NewScopeCheck(workspace, []ScopeBroker{broker}, read))
			if err != nil {
				t.Fatal(err)
			}
			block := strings.Join(report.ScopeBlock, "\n")
			if !strings.Contains(block, "grant runs github.com/acme/repo release-request.yml") {
				t.Fatalf("the gate carries no runs grant for release-request.yml:\n%s", block)
			}
			if strings.Contains(c.project, "nightly.yml") && !strings.Contains(block, "grant runs github.com/acme/repo nightly.yml") {
				t.Fatalf("the former key's list was not merged into runs:\n%s", block)
			}
			if strings.Contains(block, "dispatch") {
				t.Fatalf("the gate names the former spelling:\n%s", block)
			}
		})
	}
}
