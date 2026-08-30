package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHostWrappersEnabledReadsUserConfigOnly is the SECURITY test, not a plumbing one.
//
// /workspace is bind-mounted rw, so an agent can edit the workspace config. If this key
// were read from the merged config, a repository could arrange for yolo to write
// executables into a directory at the FRONT of its user's PATH on the next apply. Reading
// the user config directly makes that inexpressible rather than merely refused, so this
// test asserts the workspace value is not merely rejected — it is never consulted.
func TestHostWrappersEnabledReadsUserConfigOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userCfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	// No user config at all -> not opted in.
	if HostWrappersEnabled() {
		t.Error("HostWrappersEnabled with no user config = true, want false")
	}

	// User config says true -> opted in.
	write(t, userCfgPath, `{"host_wrappers": true}`)
	if !HostWrappersEnabled() {
		t.Error("HostWrappersEnabled with user config true = false, want true")
	}

	// User config says false -> not opted in.
	write(t, userCfgPath, `{"host_wrappers": false}`)
	if HostWrappersEnabled() {
		t.Error("HostWrappersEnabled with user config false = true, want false")
	}

	// A broken user config fails CLOSED: an opt-in nobody could read was not given, and
	// defaulting a PATH claim to ON when the file is unparseable is the wrong direction.
	write(t, userCfgPath, `{"host_wrappers": tru`)
	if HostWrappersEnabled() {
		t.Error("HostWrappersEnabled with an unparseable user config = true, want false")
	}
}

// TestHostWrappersEnabledIgnoresWorkspaceScope is the half that proves the word "ONLY",
// and it is the one that matters. The test above writes only a user config, so reading
// the MERGED config would give the same answer and the boundary would look intact while
// being gone. This writes the opt-in ONLY into the agent-editable workspace config and
// asserts it is never seen — the attack the user-scope read exists to make impossible.
func TestHostWrappersEnabledIgnoresWorkspaceScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")

	ws := t.TempDir()
	t.Chdir(ws)
	// A repository — or an agent editing the live bind mount — turns it on.
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"host_wrappers": true}`)
	// The user never did.
	userCfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, userCfgPath, `{"packs": []}`)

	if HostWrappersEnabled() {
		t.Fatal("a WORKSPACE config enabled host_wrappers — a repository can now make yolo " +
			"write executables to the front of its user's PATH")
	}

	// And the user's own "false" is not overridden by a workspace "true" either.
	write(t, userCfgPath, `{"host_wrappers": false}`)
	if HostWrappersEnabled() {
		t.Error("a workspace true overrode the user's explicit false")
	}
}

// TestValidateHostWrappersWorkspaceScopeErrors is the defense-in-depth half: a workspace
// value is already inert, and saying so loudly is what keeps it from LOOKING like it
// worked.
func TestValidateHostWrappersWorkspaceScopeErrors(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "") // host behavior: the workspace re-read runs
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"host_wrappers": true}`)

	var errs []string
	validateHostWrappers(decode(t, `{"host_wrappers": true}`), ws, &errs)
	if len(errs) == 0 {
		t.Fatal("want an error for a workspace-scoped host_wrappers")
	}
	if joined := strings.Join(errs, "\n"); !strings.Contains(joined, "user-scope only") {
		t.Errorf("errors = %v, want one saying 'user-scope only'", errs)
	}
}

// TestValidateHostWrappersUserScopeQuiet: the same value in the user config is the
// supported spelling and must produce nothing.
func TestValidateHostWrappersUserScopeQuiet(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	var errs []string
	validateHostWrappers(decode(t, `{"host_wrappers": true}`), ws, &errs)
	if len(errs) != 0 {
		t.Errorf("user-scoped host_wrappers produced errors: %v", errs)
	}
}

func TestValidateHostWrappersTypeCheck(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	var errs []string
	validateHostWrappers(decode(t, `{"host_wrappers": "yes"}`), ws, &errs)
	if len(errs) == 0 {
		t.Fatal("want a type error for a string host_wrappers")
	}
	if !strings.Contains(errs[0], "expected a boolean") {
		t.Errorf("errs[0] = %q, want 'expected a boolean'", errs[0])
	}

	// null and absent are both "not set" and must be quiet.
	for _, text := range []string{`{"host_wrappers": null}`, `{}`} {
		var e2 []string
		validateHostWrappers(decode(t, text), ws, &e2)
		if len(e2) != 0 {
			t.Errorf("%s produced errors: %v", text, e2)
		}
	}
}

// TestHostWrappersIsAKnownKey: an unlisted top-level key is a hard "unknown key" error,
// so the schema set and the validator have to agree or the feature is unusable.
func TestHostWrappersIsAKnownKey(t *testing.T) {
	if _, known := knownTopLevelConfigKeys["host_wrappers"]; !known {
		t.Error("host_wrappers is not in knownTopLevelConfigKeys")
	}
}
