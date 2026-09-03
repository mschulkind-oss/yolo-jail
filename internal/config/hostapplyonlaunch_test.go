package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHostApplyOnLaunchEnabledDefaultsOff is OQ-HS2's first half, and it is the whole shape of
// the ruling: the feature is opt-IN, so an untouched machine must behave exactly as it does
// today — no launch and no command mentioning any of it (§11's fourth done-condition).
func TestHostApplyOnLaunchEnabledDefaultsOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userCfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	if HostApplyOnLaunchEnabled() {
		t.Error("with no user config = true, want false — the key is opt-in")
	}
	write(t, userCfgPath, `{"packs": []}`)
	if HostApplyOnLaunchEnabled() {
		t.Error("with the key absent = true, want false")
	}
	write(t, userCfgPath, `{"host_apply_on_launch": true}`)
	if !HostApplyOnLaunchEnabled() {
		t.Error("with the user config true = false, want true")
	}
	write(t, userCfgPath, `{"host_apply_on_launch": false}`)
	if HostApplyOnLaunchEnabled() {
		t.Error("with the user config false = true, want false")
	}
	// A broken user config fails CLOSED, for HostWrappersEnabled's reason and one step
	// further: this key licenses a WRITE into the real home, so an opt-in nobody could read
	// must not be honored.
	write(t, userCfgPath, `{"host_apply_on_launch": tru`)
	if HostApplyOnLaunchEnabled() {
		t.Error("with an unparseable user config = true, want false")
	}
}

// TestHostApplyOnLaunchIgnoresWorkspaceScope is the SECURITY test.
//
// /workspace is bind-mounted rw, so a repository — or an agent editing one — can set any
// workspace key. If this one were read from the merged config, cloning a repo and typing
// `claude` would render THAT repo's pack set into the user's real home. Reading user scope
// directly makes it inexpressible; this asserts the value is never consulted, not merely
// rejected.
func TestHostApplyOnLaunchIgnoresWorkspaceScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")

	ws := t.TempDir()
	t.Chdir(ws)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"host_apply_on_launch": true}`)
	userCfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, userCfgPath, `{"packs": []}`)

	if HostApplyOnLaunchEnabled() {
		t.Fatal("a WORKSPACE config enabled host_apply_on_launch — a cloned repository can now " +
			"have its own packs rendered into its user's real home the next time they type " +
			"`claude`")
	}
	write(t, userCfgPath, `{"host_apply_on_launch": false}`)
	if HostApplyOnLaunchEnabled() {
		t.Error("a workspace true overrode the user's explicit false")
	}
}

// TestValidateHostApplyOnLaunchWorkspaceScopeErrors is the defense-in-depth half: the value is
// already inert, and saying so loudly is what keeps it from LOOKING like it worked.
func TestValidateHostApplyOnLaunchWorkspaceScopeErrors(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"host_apply_on_launch": true}`)

	var errs []string
	validateHostApplyOnLaunch(decode(t, `{"host_apply_on_launch": true}`), ws, &errs)
	if len(errs) == 0 {
		t.Fatal("want an error for a workspace-scoped host_apply_on_launch")
	}
	if joined := strings.Join(errs, "\n"); !strings.Contains(joined, "user-scope only") {
		t.Errorf("errors = %v, want one saying 'user-scope only'", errs)
	}
}

func TestValidateHostApplyOnLaunchUserScopeQuiet(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	var errs []string
	validateHostApplyOnLaunch(decode(t, `{"host_apply_on_launch": true}`), ws, &errs)
	if len(errs) != 0 {
		t.Errorf("user-scoped host_apply_on_launch produced errors: %v", errs)
	}
}

func TestValidateHostApplyOnLaunchTypeCheck(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	var errs []string
	validateHostApplyOnLaunch(decode(t, `{"host_apply_on_launch": "yes"}`), ws, &errs)
	if len(errs) == 0 {
		t.Fatal("want a type error for a string host_apply_on_launch")
	}
	if !strings.Contains(errs[0], "expected a boolean") {
		t.Errorf("errs[0] = %q, want 'expected a boolean'", errs[0])
	}
	for _, text := range []string{`{"host_apply_on_launch": null}`, `{}`} {
		var e2 []string
		validateHostApplyOnLaunch(decode(t, text), ws, &e2)
		if len(e2) != 0 {
			t.Errorf("%s produced errors: %v", text, e2)
		}
	}
}

// TestValidateHostApplyOnLaunchIsReachedFromValidateConfig pins the CALL SITE, not the
// validator: deleting the `validateHostApplyOnLaunch(...)` line from ValidateConfig leaves
// every test above green while a workspace-scoped key goes back to looking accepted.
func TestValidateHostApplyOnLaunchIsReachedFromValidateConfig(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(ws)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"host_apply_on_launch": true}`)

	errs, _ := ValidateConfig(decode(t, `{"host_apply_on_launch": true}`), ws, nil)
	if !strings.Contains(strings.Join(errs, "\n"), hostApplyOnLaunchKey) {
		t.Errorf("ValidateConfig does not reach validateHostApplyOnLaunch — a workspace value "+
			"is inert and nothing says so; got %v", errs)
	}
}

// TestHostApplyOnLaunchIsAKnownKey: an unlisted top-level key is a hard "unknown key" error,
// so the schema set and the validator have to agree or the key is unusable.
func TestHostApplyOnLaunchIsAKnownKey(t *testing.T) {
	if _, known := knownTopLevelConfigKeys[hostApplyOnLaunchKey]; !known {
		t.Error("host_apply_on_launch is not in knownTopLevelConfigKeys")
	}
}
