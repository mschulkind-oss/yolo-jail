package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// Host `yolo check` says up front what a selected pack's broker may do (docs/design/boundary-broker.md
// BB-D108, BB-D109, BB-D110): with no read grant in the user config, a plain note, never a
// warning, that it grants nothing, naming the grant; with the grant, no note; and a switch a previous yolo wrote into the per-workspace
// file is taken out of it, its read grant kept for the workspace (docs/design/legacy-config-compat.md LC-D7).
func TestCheckSaysUpFrontWhatASelectedBrokerGrants(t *testing.T) {
	const note = "loophole gb is selected and grants nothing"
	t.Run("no grant", func(t *testing.T) {
		home := brokeredCheckHome(t)
		must(t, os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), []byte(`{}`), 0o644))
		out := runCheckOverConfigWith(t, `{}`, false, nil, onTheHost)
		for _, want := range []string{note, "brokered.gbsrc.grants", `{"kind": "read", "repo": "*"}`, "yolo config apply"} {
			if !strings.Contains(out, want) {
				t.Errorf("yolo check does not say %q:\n%s", want, out)
			}
		}
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, note) && strings.Contains(l, "[WARN]") {
				t.Errorf("the note is a warning: %s", l)
			}
		}
	})
	t.Run("the user config's read grant", func(t *testing.T) {
		home := brokeredCheckHome(t)
		cfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
		must(t, os.MkdirAll(filepath.Dir(cfg), 0o755))
		must(t, os.WriteFile(cfg, []byte(`{"brokered": {"gbsrc": {"grants": [{"kind": "read", "repo": "*"}]}}}`), 0o644))
		out := runCheckOverConfigWith(t, `{}`, false, nil, onTheHost)
		if strings.Contains(out, note) {
			t.Errorf("yolo check says a granted broker grants nothing:\n%s", out)
		}
	})
	t.Run("an old per-workspace switch", func(t *testing.T) {
		home := brokeredCheckHome(t)
		// No read grant in the user config: the switch's carried grant is the workspace's only one.
		must(t, os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), []byte(`{}`), 0o644))
		out := runCheckOverConfigWith(t, `{}`, false, func(w string) {
			_, err := config.SetWorkspaceLoophole(w, "gb", true)
			must(t, err)
		}, onTheHost)
		// yolo's own file, so yolo takes the switch out and keeps the read grant it gave this
		// workspace, said once, with no command for the user (LC-D7, OQ-LC2).
		for _, want := range []string{"Removed the old gb switch from ", "this workspace keeps the read grant it gave"} {
			if !strings.Contains(out, want) {
				t.Errorf("yolo check does not say %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "yolo loopholes disable") {
			t.Errorf("yolo check still hands the user a command for yolo's own file:\n%s", out)
		}
		// The up-front note reads this workspace's carried grant: the broker does not grant
		// nothing here, and the note says what it does run on.
		if strings.Contains(out, note) {
			t.Errorf("yolo check says the broker grants nothing in a workspace that keeps a carried read grant:\n%s", out)
		}
		if want := "runs on the read grant this workspace's approval record carries"; !strings.Contains(out, want) {
			t.Errorf("yolo check does not say %q:\n%s", want, out)
		}
	})
}
