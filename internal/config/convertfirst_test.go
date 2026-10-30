package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// convertFirstGitGrants is the git broker's gate grants for a workspace with an origin on
// github.com/acme/repo, the user file userName holding user and the workspace file wsName ws.
func convertFirstGitGrants(t *testing.T, userName, user, wsName, ws string) []brokerscope.Grant {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, userName), []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	writeReadableScopeRemote(t, workspace, "[remote \"origin\"]\nurl = https://github.com/acme/repo.git\n")
	if err := os.WriteFile(filepath.Join(workspace, wsName), []byte(ws), 0o600); err != nil {
		t.Fatal(err)
	}
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	broker := readableScopeBroker("git", "git", "git-broker", "*", "fetch", []string{"branch"}, map[string]string{"branch": "push"})
	check := NewScopeCheck(workspace, []ScopeBroker{broker}, read)
	if check.Fatal != nil || len(check.Sources) != 1 {
		t.Fatalf("scope check fatal=%v sources=%#v", check.Fatal, check.Sources)
	}
	return check.Sources[0].Grants
}

var convertFirstMainGrant = brokerscope.Grant{Kind: "branch", Repo: "github.com/acme/repo", Branch: "main"}

// The readers `yolo allow` and `yolo request` write for honor the TOML vocabulary in a JSONC
// file, so neither verb refuses a JSONC file for its format alone.
func TestTheTOMLVocabularyTakesEffectInJSONCFiles(t *testing.T) {
	t.Run("allow in a JSONC user file", func(t *testing.T) {
		got := convertFirstGitGrants(t, "config.jsonc", `{"allow":{"git":{"always":["fetch"],"on_request":["push"]}}}`,
			WorkspaceTOMLConfigName, "[requests.git]\npush = [\"main\"]\n")
		if !containsGrant(got, convertFirstMainGrant) {
			t.Fatalf("a JSONC user file's allow was not honored: %v", got)
		}
	})
	t.Run("requests in a JSONC project file", func(t *testing.T) {
		got := convertFirstGitGrants(t, "config.toml", "[allow.git]\nalways = [\"fetch\"]\non_request = [\"push\"]\n",
			WorkspaceConfigName, `{"requests":{"git":{"push":["main"]}}}`)
		if !containsGrant(got, convertFirstMainGrant) {
			t.Fatalf("a JSONC project file's requests were not honored: %v", got)
		}
	})
}

// WHY `yolo allow` REFUSES A USER CONFIG HOLDING `brokered`: once any `allow` table exists, the
// legacy `brokered` user grants stop counting, so the push kind the file already allowed is gone.
func TestAnAllowTableSuppressesLegacyBrokeredUserGrants(t *testing.T) {
	ws := `{"brokered":{"git":{"grants":[{"kind":"branch","repo":"github.com/acme/repo","branch":"main"}]}}}`
	legacy := `{"brokered":{"git":{"grants":[{"kind":"read","repo":"*"}],"grant_kinds":["branch"]}}`
	if got := convertFirstGitGrants(t, "config.jsonc", legacy+`}`, WorkspaceConfigName, ws); !containsGrant(got, convertFirstMainGrant) {
		t.Fatalf("the legacy ceiling alone did not grant the push: %v", got)
	}
	mixed := legacy + `,"allow":{"git":{"always":["fetch"]}}}`
	if got := convertFirstGitGrants(t, "config.jsonc", mixed, WorkspaceConfigName, ws); containsGrant(got, convertFirstMainGrant) {
		t.Fatalf("the legacy grant survived an allow table; if mixing is now safe, yolo allow need not refuse: %v", got)
	}
}
