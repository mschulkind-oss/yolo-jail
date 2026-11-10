package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A broker's write authority reads its own `allow.<source>` table and no other
// (BrokeredGrantKindsUsingReadablePolicy, ownAllowanceOnly): a running broker rereads the user
// config at every call, and a newer yolo on the host can write another source's table in names
// this one does not declare. Each source's grants survive the other's unreadable table, and each
// still fails closed on its own. internal/gitbroker's
// TestAnotherSourcesUnreadableAllowanceDoesNotWithdrawTheGitBranchGrant pins the push path.
func TestABrokersWriteAuthorityReadsOnlyItsOwnAllowTable(t *testing.T) {
	const unknown = "a_capability_a_newer_yolo_declares"
	write := func(t *testing.T, gitOnRequest, githubOnRequest string) {
		t.Helper()
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		dir := filepath.Join(home, ".config", "yolo-jail")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		body := "packs = ['git', 'github']\n[allow.git]\nalways = ['fetch']\non_request = " + gitOnRequest +
			"\n[allow.github]\nalways = ['read']\non_request = " + githubOnRequest + "\n"
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, gitOnRequest, githubOnRequest string
		source, kind                        string
	}{
		{"git survives an unreadable allow.github", "['push']", "['" + unknown + "']", "git", "branch"},
		{"github survives an unreadable allow.git", "['push', '" + unknown + "']", "['create_pr']", "github", "create-pr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			write(t, tc.gitOnRequest, tc.githubOnRequest)
			kinds, canonical, err := BrokeredGrantKindsUsingReadablePolicy(tc.source)
			if err != nil || !canonical || !slices.Contains(kinds, tc.kind) {
				t.Fatalf("%s kinds = %v, canonical %t, err %v; want %q despite the other source's table", tc.source, kinds, canonical, err, tc.kind)
			}
		})
	}
	t.Run("an unreadable own table still fails closed", func(t *testing.T) {
		write(t, "['push', '"+unknown+"']", "['create_pr']")
		kinds, canonical, err := BrokeredGrantKindsUsingReadablePolicy("git")
		if err == nil || !canonical || len(kinds) != 0 || !strings.Contains(err.Error(), unknown) {
			t.Fatalf("git kinds = %v, canonical %t, err %v; want a canonical error naming %q", kinds, canonical, err, unknown)
		}
	})
}
