package run

// legacyseed_test.go pins that the machine store seeds nothing into a new workspace but the
// Claude login (docs/design/base-home-legacy-state.md#27-the-seed).
//
// seedAgentDir copied every top-level regular file of <state>/home/.<dir> into each new
// workspace's overlay for every selected pack. Its only inputs since the base went read-only
// (2026-04-07) were legacy bytes from the shared-writable era, zero-byte mountpoint files and
// `.claude/claude.json` — which SyncClaudeJSONSeed already handles, through an allowlist. So
// it was the one channel through which a file left in the machine store by an older yolo, or
// by another workspace, reached a workspace that never asked for it; and no test referenced
// it, so deleting it made nothing fail. This is the test that proves it is gone.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestAFilePlantedInTheMachineStoreReachesNoWorkspace(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			ws := t.TempDir()

			planted := map[string]string{
				filepath.Join(paths.GlobalHome(), ".copilot", "config.json"):        "copilot",
				filepath.Join(paths.GlobalHome(), ".claude", "settings.local.json"): "claude",
			}
			for p := range planted {
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(`{"legacy": true}`), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			o := &Options{Workspace: ws}
			wsState := o.prepareWsState(nil, packsFixture(t, "claude", "copilot"), rt)

			for p, subdir := range planted {
				// Both spellings: podman's pack dirs are dot-stripped in wsState, Apple
				// Container's are at their dotted home paths (prepareWsState).
				for _, dir := range []string{subdir, "." + subdir} {
					seeded := filepath.Join(wsState, dir, filepath.Base(p))
					if _, err := os.Lstat(seeded); err == nil {
						t.Errorf("%s was copied out of the machine store into %s — the machine store "+
							"seeds a workspace with the Claude login and nothing else", p, seeded)
					}
				}
			}
		})
	}
}
