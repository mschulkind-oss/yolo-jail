package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// grantprovenance_test.go covers where each grant line of the config-change gate says it came
// from (docs/design/github-write-scopes.md GW-D68): a grant the workspace asked for names the
// workspace file and its request key, and only a grant the user's own config holds says it is
// the user config's, for every workspace.

func TestAWorkspaceRequestedGrantIsNotLabeledAsUserConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.github]\nalways = [\"read\"]\non_request = [\"runs\"]\n")
	workspace := t.TempDir()
	writeReadableScopeRemote(t, workspace, "[remote \"origin\"]\nurl = git@github.com:acme/repo.git\n")
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceTOMLConfigName),
		[]byte("[requests.github]\nruns = [\"release-request.yml\"]\n"), 0o600); err != nil {
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
	var dispatch, readLine string
	for _, line := range report.ScopeBlock {
		switch {
		case strings.Contains(line, "grant runs"):
			dispatch = line
		case strings.Contains(line, "grant read "):
			readLine = line
		}
	}
	if dispatch == "" || readLine == "" {
		t.Fatalf("the block lacks the dispatch or read grant line:\n%s", strings.Join(report.ScopeBlock, "\n"))
	}
	if strings.Contains(dispatch, "user config") || strings.Contains(dispatch, "for every workspace") {
		t.Errorf("a workspace-requested grant is labeled as the user config's: %q", dispatch)
	}
	if want := "(" + WorkspaceTOMLConfigName + ": requests.github.runs)"; !strings.HasSuffix(dispatch, want) {
		t.Errorf("the workspace-requested grant line %q does not end with %q", dispatch, want)
	}
	if want := "(~/.config/yolo-jail/config.toml: allow.github.always, for every workspace)"; !strings.HasSuffix(readLine, want) {
		t.Errorf("the user-config read grant line %q does not end with %q", readLine, want)
	}
}
