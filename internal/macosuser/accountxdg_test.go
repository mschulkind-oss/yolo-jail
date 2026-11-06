package macosuser

import (
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// MMP-D31: in the account mode HOME is the workspace's sidecar, which the invoking user owns, and
// the sandbox runs as the account, so nix's getHome refused it on every call ("$HOME (…) is not
// owned by you, falling back to … /Users/_yolojail", the Mac run of 2026-11-03) and read its user
// config from the account home. Nix asks getHome only for a base dir whose XDG variable is unset
// (measured on Linux with nix 2.34: HOME owned by another uid warns; with the four XDG variables
// set it does not), so the account mode names all four at their default spellings under HOME. A
// value an env layer composed wins, and the run-as-you mode, whose HOME is its own, sets none.
func TestAccountModeNamesTheXDGBaseDirsUnderTheWorkspaceHome(t *testing.T) {
	home := paths.LegacyHomeState("/Users/you/code/alpha")
	want := map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_DATA_HOME":   filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
	}
	plan := sessionTmpPlan(t, ModeAccount, nil)
	for key, w := range want {
		if got, ok := sandboxEnvFileValue(plan.EnvFileContent, key); !ok || got != w {
			t.Errorf("the account mode's %s = %q (set %v), want %q", key, got, ok, w)
		}
	}

	composed := jsonx.NewOrderedMap()
	composed.Set("XDG_CONFIG_HOME", "/Users/Shared/cfg")
	if got, _ := sandboxEnvFileValue(sessionTmpPlan(t, ModeAccount, composed).EnvFileContent, "XDG_CONFIG_HOME"); got != "/Users/Shared/cfg" {
		t.Errorf("a composed XDG_CONFIG_HOME was replaced in the account mode: %q", got)
	}

	self := sessionTmpPlan(t, ModeSelf, nil)
	for key := range want {
		if v, ok := sandboxEnvFileValue(self.EnvFileContent, key); ok {
			t.Errorf("the run-as-you mode sets %s=%q; its HOME is its own", key, v)
		}
	}
}
