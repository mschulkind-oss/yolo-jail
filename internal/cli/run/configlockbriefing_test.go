package run

// configlockbriefing_test.go pins that the jail briefing tells the agent its workspace config is
// read-only when the launch locked it. Any `workspace_readonly` entry binds that file `:ro`
// (workspaceConfigLockTarget), and a briefing that said "edit `/workspace/yolo-jail.jsonc`" sent the
// agent straight into EROFS. Driven through refreshJailBriefings, the briefing's call site, and read
// back from the file an agent reads.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBriefingSaysALockedWorkspaceConfigIsReadOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		readonly []any
		locked   bool
	}{
		{"workspace_readonly set", []any{".git/hooks"}, true},
		{"workspace_readonly unset", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			ws := t.TempDir()
			emptyLoopholeDirs(t)
			if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(`{}`), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg := appliedTestConfig()
			if tc.readonly != nil {
				cfg = appliedTestConfig("workspace_readonly", tc.readonly)
			}
			briefing := appliedBriefing(t, appliedOptions(t, ws, home, false), "podman", cfg)
			const lockedSays = "`/workspace/yolo-jail.jsonc` is **read-only**"
			const editSays = "edit `/workspace/yolo-jail.jsonc`"
			if got := strings.Contains(briefing, lockedSays); got != tc.locked {
				t.Errorf("briefing says the config is read-only = %v, want %v:\n%s", got, tc.locked, briefing)
			}
			if got := strings.Contains(briefing, editSays); got == tc.locked {
				t.Errorf("briefing tells the agent to edit the config = %v, want %v:\n%s", got, !tc.locked, briefing)
			}
		})
	}
}
