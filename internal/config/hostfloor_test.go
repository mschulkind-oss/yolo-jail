package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostfloor_test.go covers the `host_floor` key (docs/design/host-tool-provisioning.md OQ-HP1):
// read from user scope only, on by default (an empty wire, which the one reader defaults open),
// agent_updates' two shapes, and refused at workspace scope. Every validation assertion goes
// through ValidateConfig, the call site `yolo check` and a launch reach.

func hostFloorHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	userCfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfg), 0o755); err != nil {
		t.Fatal(err)
	}
	return userCfg
}

// TestHostFloorWireIsTheUserConfigsValueAndNothingElse: absent and unparseable are empty (on by
// default, OQ-HP1), both shapes pass verbatim, and a workspace value never reaches it — a cloned
// repository must not choose what yolo installs on the host.
func TestHostFloorWireIsTheUserConfigsValueAndNothingElse(t *testing.T) {
	userCfg := hostFloorHome(t)
	ws := t.TempDir()
	t.Chdir(ws)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"host_floor": false}`)
	if got := HostFloorWire(); got != "" {
		t.Errorf("with no user config = %q, want empty (on by default); a workspace value "+
			"must never be read", got)
	}
	write(t, userCfg, `{"host_floor": fals`)
	if got := HostFloorWire(); got != "" {
		t.Errorf("an unparseable user config = %q, want empty", got)
	}
	write(t, userCfg, `{"host_floor": {"*": true, "claude": false}}`)
	if got := HostFloorWire(); !strings.Contains(got, `"claude": false`) {
		t.Errorf("wire = %q, want the per-pack map verbatim", got)
	}
}

// TestValidateRefusesAWorkspaceHostFloorAndABadShape goes through ValidateConfig.
func TestValidateRefusesAWorkspaceHostFloorAndABadShape(t *testing.T) {
	hostFloorHome(t)
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"host_floor": false}`)
	errs, _ := ValidateConfig(decode(t, `{"host_floor": false}`), ws, nil)
	joined := strings.Join(errs, "\n")
	for _, want := range []string{"config.host_floor: user-scope only", paths.UserConfigPath()} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors lack %q:\n%s", want, joined)
		}
	}
	clean := t.TempDir()
	for _, ok := range []string{`{"host_floor": true}`, `{"host_floor": {"*": false, "pi": true}}`} {
		errs, _ := ValidateConfig(decode(t, ok), clean, nil)
		for _, e := range errs {
			if strings.Contains(e, "host_floor") {
				t.Errorf("%s refused: %s", ok, e)
			}
		}
	}
	for _, bad := range []string{`{"host_floor": "yes"}`, `{"host_floor": {"claude": "no"}}`} {
		errs, _ := ValidateConfig(decode(t, bad), clean, nil)
		if !strings.Contains(strings.Join(errs, "\n"), "config.host_floor:") {
			t.Errorf("%s was accepted: %v", bad, errs)
		}
	}
}
