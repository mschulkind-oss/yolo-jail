package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// agentupdates_test.go covers the HOST half of `agent_updates`
// (docs/design/program-delivery.md §3.5, OQ-PD12). The jail half — the precedence rule and
// the launcher's baked flag — is internal/entrypoint's.

// TestAgentUpdatesWireDefaultsToAbsent is trap 9, and it is the one thing about this key
// that is easy to copy wrong.
//
// Its two neighbours (host_wrappers, host_apply_on_launch) fail CLOSED: an unreadable
// config has not granted an opt-in. This key is an opt-OUT of a policy that is on by
// ruling, so nothing readable must ever produce "false" — the wire stays EMPTY and the jail
// reader defaults open. A faithful copy of hostapplyonlaunch.go would silently freeze every
// agent in every jail, which is both the state §3.5 exists to end and the one nobody would
// notice for weeks.
func TestAgentUpdatesWireDefaultsToAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userCfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	if got := AgentUpdatesWire(); got != "" {
		t.Errorf("with no user config = %q, want empty (the jail defaults OPEN)", got)
	}
	write(t, userCfgPath, `{"packs": []}`)
	if got := AgentUpdatesWire(); got != "" {
		t.Errorf("with the key absent = %q, want empty", got)
	}
	// An UNPARSEABLE config must also come back empty. This is the assertion that inverts
	// the precedent: the neighbouring keys treat "cannot read it" as "not granted".
	write(t, userCfgPath, `{"agent_updates": fals`)
	if got := AgentUpdatesWire(); got != "" {
		t.Errorf("with an unparseable user config = %q, want empty — an agent_updates nobody "+
			"could read must not freeze every agent", got)
	}
}

// TestAgentUpdatesWireCarriesBothShapes: a bool and a per-pack map both reach the jail
// verbatim. The wire is the value, not a normalization of it, so the precedence rule lives
// in exactly one place (the jail reader) instead of two.
func TestAgentUpdatesWireCarriesBothShapes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userCfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	write(t, userCfgPath, `{"agent_updates": false}`)
	if got := AgentUpdatesWire(); got != "false" {
		t.Errorf("wire = %q, want %q", got, "false")
	}
	write(t, userCfgPath, `{"agent_updates": {"*": true, "claude": false}}`)
	got := AgentUpdatesWire()
	for _, want := range []string{`"*"`, `"claude"`, "false"} {
		if !strings.Contains(got, want) {
			t.Errorf("wire = %q, missing %s", got, want)
		}
	}
}

// TestAgentUpdatesIgnoresWorkspaceScope is the SECURITY cell, and it is why the key is
// user-scope at all: /workspace is bind-mounted rw, so whatever runs in the jail can edit
// the workspace config. If this key were read from the merged config, an agent could freeze
// its own updates — the one setting it has a motive to change and no business changing.
//
// It asserts the value is never CONSULTED, not merely rejected.
func TestAgentUpdatesIgnoresWorkspaceScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")

	ws := t.TempDir()
	t.Chdir(ws)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"agent_updates": false}`)
	userCfgPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, userCfgPath, `{"packs": []}`)

	if got := AgentUpdatesWire(); got != "" {
		t.Fatalf("a WORKSPACE config reached the wire (%q) — whatever runs in the jail can "+
			"now freeze its own updates", got)
	}
	write(t, userCfgPath, `{"agent_updates": true}`)
	if got := AgentUpdatesWire(); got != "true" {
		t.Errorf("a workspace false overrode the user's explicit true: %q", got)
	}
}

// TestValidateAgentUpdatesWorkspaceScopeErrors is the defense-in-depth half: the value is
// already inert, and saying so loudly is what keeps it from LOOKING like it worked.
func TestValidateAgentUpdatesWorkspaceScopeErrors(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"agent_updates": false}`)

	var errs []string
	validateAgentUpdates(decode(t, `{"agent_updates": false}`), ws, &errs)
	if len(errs) == 0 {
		t.Fatal("want an error for a workspace-scoped agent_updates")
	}
	joined := strings.Join(errs, "\n")
	// The path is named, so the user knows WHERE to move it — asserted through
	// paths.UserConfigPath() rather than against a literal, because the test home is a
	// temp dir.
	for _, want := range []string{"user-scope only", paths.UserConfigPath()} {
		if !strings.Contains(joined, want) {
			t.Errorf("errors = %v, want one containing %q", errs, want)
		}
	}
}

// TestValidateAgentUpdatesShape: both accepted shapes pass, and the two ways of writing a
// setting that reads as "on" but is not a boolean are refused by name.
func TestValidateAgentUpdatesShape(t *testing.T) {
	ws := t.TempDir()
	t.Setenv("YOLO_VERSION", "")

	for _, ok := range []string{
		`{"agent_updates": true}`,
		`{"agent_updates": false}`,
		`{"agent_updates": {}}`,
		`{"agent_updates": {"*": false, "claude": true}}`,
	} {
		var errs []string
		validateAgentUpdates(decode(t, ok), ws, &errs)
		if len(errs) != 0 {
			t.Errorf("%s should validate, got %v", ok, errs)
		}
	}
	for _, bad := range []string{
		`{"agent_updates": "yes"}`,
		`{"agent_updates": ["claude"]}`,
		`{"agent_updates": {"claude": "no"}}`,
	} {
		var errs []string
		validateAgentUpdates(decode(t, bad), ws, &errs)
		if len(errs) == 0 {
			t.Errorf("%s should be refused — a non-boolean here is a setting that reads as "+
				"\"on\" and means nothing", bad)
		}
	}
}

// TestAgentUpdatesIsAKnownConfigKey is the call-site cell for the schema half: without the
// registration, every config carrying the key gets an "unknown key" error and the feature
// is unusable no matter how well the reader works.
func TestAgentUpdatesIsAKnownConfigKey(t *testing.T) {
	if _, known := knownTopLevelConfigKeys["agent_updates"]; !known {
		t.Error("agent_updates is missing from knownTopLevelConfigKeys — every config that " +
			"sets it would fail validation as an unknown key")
	}
}
