package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A user-scope-only key written in the workspace config is an error, and an error stops the
// launch and `yolo check` on the host, so the message may not say the value "has no effect".
func TestAUserScopeOnlyWorkspaceValueSaysItRefusesTheLaunch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	for key, value := range map[string]string{
		"host_wrappers": `false`, "host_apply_on_launch": `false`, "host_management": `"own"`,
		"agent_updates": `"off"`, "host_floor": `false`, "host_path": `[]`,
		"promotion_target": `"pack:workspace"`, "share_host_fonts": `false`,
		"shipped_skill_prefix": `"x"`,
	} {
		body := `{"` + key + `": ` + value + `}`
		ws := t.TempDir()
		if err := os.WriteFile(filepath.Join(ws, WorkspaceConfigName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		errs, _ := ValidateConfig(decode(t, body), ws, nil)
		var found string
		for _, e := range errs {
			if strings.HasPrefix(e, "config."+key+": user-scope only") {
				found = e
			}
		}
		if found == "" {
			t.Errorf("%s: no user-scope refusal in %v", key, errs)
			continue
		}
		if strings.Contains(found, "no effect") || !strings.Contains(found, "refuses the launch") {
			t.Errorf("%s: the refusal does not say the workspace value refuses the launch: %q", key, found)
		}
	}
}
